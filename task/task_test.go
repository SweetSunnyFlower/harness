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
