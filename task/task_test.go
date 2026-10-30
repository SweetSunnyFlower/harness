package task_test

import (
	"context"
	"testing"

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
		Phases: map[string]func(context.Context, *task.Runtime, task.Checkpoint) (task.Checkpoint, *task.Outcome, error){
			"start": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) (task.Checkpoint, *task.Outcome, error) {
				return task.Checkpoint{"phase": "finish", "n": cp["n"]}, nil, nil
			},
			"finish": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) (task.Checkpoint, *task.Outcome, error) {
				return nil, &task.Outcome{Status: "completed", Result: cp["n"]}, nil
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
