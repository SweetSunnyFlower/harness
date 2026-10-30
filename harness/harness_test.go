package harness_test

import (
	"context"
	"testing"

	"harness/agent"
	"harness/harness"
	"harness/record"
	"harness/session"
	"harness/storage"
)

type scripted struct {
	steps []agent.Message
}

func (s *scripted) Generate(ctx context.Context, messages []agent.Message, tools []agent.Tool) (agent.Message, error) {
	next := s.steps[0]
	s.steps = s.steps[1:]
	return next, nil
}

func TestDurableGeneration(t *testing.T) {
	ctx := context.Background()
	sess := session.New(storage.NewMemory())
	if _, err := sess.CreateConversation(ctx, record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	model := &scripted{steps: []agent.Message{
		{Role: "assistant", ToolCalls: []agent.ToolCall{{ID: "c1", Name: "upper", Args: map[string]any{"s": "hi"}}}},
		{Role: "assistant", Content: "HI"},
	}}
	h := &harness.Harness{
		Session: sess,
		Model:   model,
		Tools: map[string]agent.Tool{
			"upper": {Name: "upper", Execute: func(ctx context.Context, args map[string]any) (string, error) {
				return args["s"].(string) + "!", nil
			}},
		},
	}
	answer, err := h.Prompt(ctx, 1, "hi")
	if err != nil || answer != "HI" {
		t.Fatalf("answer %q %v", answer, err)
	}
	page, err := sess.ScanEntries(ctx, storage.EntryQuery{ConversationID: 1, Order: storage.Ascending}, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 4 {
		t.Fatalf("entries %d want 4", len(page.Items))
	}
	kinds := make([]string, len(page.Items))
	for i, e := range page.Items {
		kinds[i] = e.Kind
	}
	want := []string{"pi.user", "pi.assistant", "pi.tool-result", "pi.assistant"}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds %v want %v", kinds, want)
		}
	}
}

func TestSubmitDeduplication(t *testing.T) {
	ctx := context.Background()
	sess := session.New(storage.NewMemory())
	if _, err := sess.CreateConversation(ctx, record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	model := &scripted{steps: []agent.Message{
		{Role: "assistant", Content: "ok"},
		{Role: "assistant", Content: "again"},
	}}
	h := &harness.Harness{Session: sess, Model: model, Tools: map[string]agent.Tool{}}
	id1, err := h.Submit(ctx, 1, "hi", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := h.Submit(ctx, 1, "hi", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("dedup ids %d %d", id1, id2)
	}
	if len(model.steps) != 1 {
		t.Fatalf("duplicate submission must not run generation again: %d steps left", len(model.steps))
	}
	sub, err := sess.Submission(ctx, id1)
	if err != nil || sub == nil || sub.Status != "done" || sub.Answer == nil {
		t.Fatalf("submission %#v %v", sub, err)
	}
}
