package task_test

import (
	"context"
	"testing"

	"harness/protocol"
	"harness/record"
	"harness/session"
	"harness/storage"
	"harness/task"
)

func TestCounterTaskRunsToCompletion(t *testing.T) {
	ctx := context.Background()
	sess := session.New(storage.NewMemory())
	if _, err := sess.CreateConversation(ctx, record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	def := task.Definition{
		Name:    "app.counter",
		Version: 1,
		Initial: func(input any) task.Checkpoint {
			return task.Checkpoint{"phase": "start", "n": input}
		},
		Phases: map[string]func(context.Context, *task.Runtime, task.Checkpoint) error{
			"start": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Commit(ctx, task.Checkpoint{"phase": "finish", "n": cp["n"]})
			},
			"finish": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Done(ctx, task.Outcome{Status: "completed", Result: cp["n"]})
			},
		},
	}
	if err := task.Create(ctx, sess, def, 3, 1, float64(7)); err != nil {
		t.Fatal(err)
	}
	out, err := task.Run(ctx, sess, def, 3)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "completed" || out.Result != float64(7) {
		t.Fatalf("outcome %#v", out)
	}
	rec, err := sess.Task(ctx, 3)
	if err != nil || rec == nil || rec.State.Status != "terminal" {
		t.Fatalf("terminal state %v %v", rec, err)
	}
}

func TestSchedulerWaitAndSleep(t *testing.T) {
	ctx := context.Background()
	sess := session.New(storage.NewMemory())
	if _, err := sess.CreateConversation(ctx, record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	child := task.Definition{
		Name: "app.child", Version: 1,
		Initial: func(any) task.Checkpoint { return task.Checkpoint{"phase": "run"} },
		Phases: map[string]func(context.Context, *task.Runtime, task.Checkpoint) error{
			"run": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Done(ctx, task.Outcome{Status: "completed", Result: "ok"})
			},
		},
	}
	parent := task.Definition{
		Name: "app.parent", Version: 1,
		Initial: func(any) task.Checkpoint { return task.Checkpoint{"phase": "spawn"} },
		Phases: map[string]func(context.Context, *task.Runtime, task.Checkpoint) error{
			"spawn": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Commit(ctx, task.Checkpoint{"phase": "wait"})
			},
			"wait": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Wait(ctx, []protocol.TaskID{3}, "allSettled", task.Checkpoint{"phase": "sleep"})
			},
			"sleep": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.SleepUntil(ctx, 10, task.Checkpoint{"phase": "done"})
			},
			"done": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Done(ctx, task.Outcome{Status: "completed"})
			},
		},
	}
	if err := task.Create(ctx, sess, child, 3, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := task.Create(ctx, sess, parent, 4, 1, nil); err != nil {
		t.Fatal(err)
	}
	clock := int64(0)
	sched := &task.Scheduler{Session: sess, Defs: map[string]task.Definition{"app.child": child, "app.parent": parent}, Now: func() int64 { return clock }}
	if err := sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	// child terminal; parent spawned, saw child done, woke, and is pending sleep.
	parentRec, err := sess.Task(ctx, 4)
	if err != nil || parentRec == nil || parentRec.State.Status != "pending" {
		t.Fatalf("parent pending sleep %v %v", parentRec, err)
	}
	if err := sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	// parent now sleeping until 10.
	parentRec, _ = sess.Task(ctx, 4)
	if parentRec.State.Status != "sleeping" {
		t.Fatalf("parent sleeping, got %s", parentRec.State.Status)
	}
	clock = 20
	if err := sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	parentRec, _ = sess.Task(ctx, 4)
	if parentRec.State.Status != "pending" {
		t.Fatalf("parent woke to pending, got %s", parentRec.State.Status)
	}
	if err := sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	parentRec, _ = sess.Task(ctx, 4)
	if parentRec.State.Status != "terminal" || parentRec.State.Outcome == nil || parentRec.State.Outcome.Status != "completed" {
		t.Fatalf("parent terminal, got %#v", parentRec.State)
	}
}

func TestRecoverRestartLimit(t *testing.T) {
	ctx := context.Background()
	sess := session.New(storage.NewMemory())
	if _, err := sess.CreateConversation(ctx, record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	def := task.Definition{
		Name: "app.recover", Version: 1,
		Initial: func(any) task.Checkpoint { return task.Checkpoint{"phase": "run"} },
		Phases: map[string]func(context.Context, *task.Runtime, task.Checkpoint) error{
			"run": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Done(ctx, task.Outcome{Status: "completed"})
			},
		},
	}
	if err := task.Create(ctx, sess, def, 5, 1, nil); err != nil {
		t.Fatal(err)
	}
	forceRunning := func() {
		if _, err := sess.Commit(ctx, func(tx *session.Tx) error {
			rec, _ := sess.Task(ctx, 5)
			rec.State = record.TaskState{TaskStateFields: record.TaskStateFields{Status: "running", Checkpoint: []byte(`{"phase":"run"}`)}}
			tx.Task(*rec)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	forceRunning()
	if err := task.Recover(ctx, sess, 3); err != nil {
		t.Fatal(err)
	}
	rec, _ := sess.Task(ctx, 5)
	if rec.State.Status != "pending" || rec.Restarts == nil || *rec.Restarts != 1 {
		t.Fatalf("recover 1 %#v", rec)
	}
	forceRunning()
	if err := task.Recover(ctx, sess, 3); err != nil {
		t.Fatal(err)
	}
	rec, _ = sess.Task(ctx, 5)
	if rec.Restarts == nil || *rec.Restarts != 2 {
		t.Fatalf("recover 2 %#v", rec)
	}
	forceRunning()
	if err := task.Recover(ctx, sess, 3); err != nil {
		t.Fatal(err)
	}
	rec, _ = sess.Task(ctx, 5)
	if rec.State.Status != "terminal" || rec.State.Outcome == nil || rec.State.Outcome.Status != "faulted" {
		t.Fatalf("restart limit %#v", rec.State)
	}
}

func TestCascadingAbort(t *testing.T) {
	ctx := context.Background()
	sess := session.New(storage.NewMemory())
	if _, err := sess.CreateConversation(ctx, record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	def := task.Definition{
		Name: "app.work", Version: 1,
		Initial: func(any) task.Checkpoint { return task.Checkpoint{"phase": "run"} },
		Phases: map[string]func(context.Context, *task.Runtime, task.Checkpoint) error{
			"run": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Commit(ctx, task.Checkpoint{"phase": "run"})
			},
		},
		Abort: func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
			return rt.Done(ctx, task.Outcome{Status: "aborted"})
		},
	}
	parent := protocol.TaskID(3)
	if err := task.Create(ctx, sess, def, parent, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := task.CreateOwned(ctx, sess, def, 4, 1, &parent, nil); err != nil {
		t.Fatal(err)
	}
	sched := &task.Scheduler{Session: sess, Defs: map[string]task.Definition{"app.work": def}}
	if err := sched.Abort(ctx, parent); err != nil {
		t.Fatal(err)
	}
	for _, id := range []protocol.TaskID{4, 3} {
		rec, err := sess.Task(ctx, id)
		if err != nil || rec == nil || rec.State.Status != "terminal" || rec.State.Outcome == nil || rec.State.Outcome.Status != "aborted" {
			t.Fatalf("task %d aborted %#v %v", id, rec, err)
		}
	}
}

func TestFailFastWaiting(t *testing.T) {
	ctx := context.Background()
	sess := session.New(storage.NewMemory())
	if _, err := sess.CreateConversation(ctx, record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	failing := task.Definition{
		Name: "app.failing", Version: 1,
		Initial: func(any) task.Checkpoint { return task.Checkpoint{"phase": "run"} },
		Phases: map[string]func(context.Context, *task.Runtime, task.Checkpoint) error{
			"run": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Done(ctx, task.Outcome{Status: "failed", Error: &record.TaskOutcomeError{Message: "boom"}})
			},
		},
	}
	work := task.Definition{
		Name: "app.work", Version: 1,
		Initial: func(any) task.Checkpoint { return task.Checkpoint{"phase": "run"} },
		Phases: map[string]func(context.Context, *task.Runtime, task.Checkpoint) error{
			"run": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Commit(ctx, task.Checkpoint{"phase": "run"})
			},
		},
	}
	parent := task.Definition{
		Name: "app.parent", Version: 1,
		Initial: func(any) task.Checkpoint { return task.Checkpoint{"phase": "wait"} },
		Phases: map[string]func(context.Context, *task.Runtime, task.Checkpoint) error{
			"wait": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Wait(ctx, []protocol.TaskID{3, 4}, "failFast", task.Checkpoint{"phase": "done"})
			},
			"done": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Done(ctx, task.Outcome{Status: "completed"})
			},
		},
	}
	if err := task.Create(ctx, sess, failing, 3, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := task.Create(ctx, sess, work, 4, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := task.Create(ctx, sess, parent, 5, 1, nil); err != nil {
		t.Fatal(err)
	}
	sched := &task.Scheduler{Session: sess, Defs: map[string]task.Definition{"app.failing": failing, "app.work": work, "app.parent": parent}}
	// First pass: children run (3 fails, 4 stays running), parent waits failFast.
	if err := sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	// Second pass: failFast sees child 3 failed, aborts 4, wakes parent.
	if err := sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	child4, _ := sess.Task(ctx, 4)
	if child4.State.Status != "terminal" || child4.State.Outcome == nil || child4.State.Outcome.Status != "aborted" {
		t.Fatalf("child 4 aborted %#v", child4.State)
	}
	if err := sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	parentRec, _ := sess.Task(ctx, 5)
	if parentRec.State.Status != "terminal" || parentRec.State.Outcome == nil || parentRec.State.Outcome.Status != "completed" {
		t.Fatalf("parent completed %#v", parentRec.State)
	}
}

func TestCompletingUntilChildrenFinish(t *testing.T) {
	ctx := context.Background()
	sess := session.New(storage.NewMemory())
	if _, err := sess.CreateConversation(ctx, record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	child := task.Definition{
		Name: "app.child", Version: 1,
		Initial: func(any) task.Checkpoint { return task.Checkpoint{"phase": "run"} },
		Phases: map[string]func(context.Context, *task.Runtime, task.Checkpoint) error{
			"run": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.SleepUntil(ctx, 1, task.Checkpoint{"phase": "done"})
			},
			"done": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Done(ctx, task.Outcome{Status: "completed"})
			},
		},
	}
	parent := task.Definition{
		Name: "app.parent", Version: 1,
		Initial: func(any) task.Checkpoint { return task.Checkpoint{"phase": "finish"} },
		Phases: map[string]func(context.Context, *task.Runtime, task.Checkpoint) error{
			"finish": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return rt.Done(ctx, task.Outcome{Status: "completed"})
			},
		},
	}
	parentID := protocol.TaskID(3)
	if err := task.CreateOwned(ctx, sess, child, 5, 1, &parentID, nil); err != nil {
		t.Fatal(err)
	}
	if err := task.Create(ctx, sess, parent, parentID, 1, nil); err != nil {
		t.Fatal(err)
	}
	clock := int64(0)
	sched := &task.Scheduler{Session: sess, Defs: map[string]task.Definition{"app.child": child, "app.parent": parent}, Now: func() int64 { return clock }}
	// Parent finishes first but owns a live child, so it stays completing.
	if err := sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	parentRec, _ := sess.Task(ctx, 3)
	if parentRec.State.Status != "completing" || parentRec.State.Outcome == nil {
		t.Fatalf("parent completing %#v", parentRec.State)
	}
	// Child sleeps until 1; parent remains completing while the child is live.
	clock = 1
	if err := sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	parentRec, _ = sess.Task(ctx, 3)
	if parentRec.State.Status != "terminal" || parentRec.State.Outcome == nil || parentRec.State.Outcome.Status != "completed" {
		t.Fatalf("parent terminal %#v", parentRec.State)
	}
}
