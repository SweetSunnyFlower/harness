package harness_test

import (
	"context"
	"testing"

	"harness/harness"
	"harness/protocol"
	"harness/record"
	"harness/session"
	"harness/storage"
	"harness/task"
)

func TestToolTaskRecoverySemantics(t *testing.T) {
	ctx := context.Background()
	sess := session.New(storage.NewMemory())
	if _, err := sess.CreateConversation(ctx, record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	executions := 0
	tools := map[string]harness.ToolDef{
		"safe":   {Name: "safe", Replay: "safe", Execute: func(context.Context, map[string]any) (string, error) { executions++; return "ok", nil }},
		"unsafe": {Name: "unsafe", Replay: "unsafe", Execute: func(context.Context, map[string]any) (string, error) { executions++; return "side effect", nil }},
	}
	def := harness.ToolTaskDefinition(tools)
	input := map[string]any{"name": "unsafe", "args": map[string]any{}, "replay": "unsafe", "callID": "c1"}
	if err := task.Create(ctx, sess, def, 3, 1, input); err != nil {
		t.Fatal(err)
	}
	sched := &task.Scheduler{Session: sess, Defs: map[string]task.Definition{"pi.tool": def}}
	// First run: marks started and executes the unsafe tool.
	if err := sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	rec, _ := sess.Task(ctx, 3)
	if rec.State.Status != "terminal" || rec.State.Outcome == nil || rec.State.Outcome.Status != "completed" {
		t.Fatalf("first run terminal %#v", rec.State)
	}
	if executions != 1 {
		t.Fatalf("executions %d want 1", executions)
	}

	// Simulate a crash: reopen the task as running with the same started checkpoint,
	// then recover — an unsafe tool must not execute again.
	forceRunning(t, sess, 3, `{"phase":"execute","name":"unsafe","args":{},"replay":"unsafe","callID":"c1","started":true}`)
	if err := task.Recover(ctx, sess, 3); err != nil {
		t.Fatal(err)
	}
	if err := sched.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	rec, _ = sess.Task(ctx, 3)
	if rec.State.Outcome == nil || rec.State.Outcome.Status != "failed" || rec.State.Outcome.Error == nil || rec.State.Outcome.Error.Message != "interrupted" {
		t.Fatalf("unsafe recovered %#v", rec.State)
	}
	if executions != 1 {
		t.Fatalf("unsafe re-executed: %d", executions)
	}
}

func forceRunning(t *testing.T, sess *session.Session, id protocol.TaskID, checkpoint string) {
	t.Helper()
	if _, err := sess.Commit(context.Background(), func(tx *session.Tx) error {
		rec, _ := sess.Task(context.Background(), id)
		rec.State = record.TaskState{TaskStateFields: record.TaskStateFields{Status: "running", Checkpoint: []byte(checkpoint)}}
		tx.Task(*rec)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
