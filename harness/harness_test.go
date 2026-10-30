package harness_test

import (
	"context"
	"reflect"
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
		Tools: map[string]harness.ToolDef{
			"upper": {Name: "upper", Replay: "safe", Execute: func(ctx context.Context, args map[string]any) (string, error) {
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
	tasks, err := sess.ScanTasks(ctx, storage.TaskQuery{Kind: "pi.tool"}, 10, nil)
	if err != nil || len(tasks.Items) != 1 {
		t.Fatalf("tool tasks %v %v", tasks, err)
	}
	toolTask := tasks.Items[0]
	if toolTask.State.Status != "terminal" || toolTask.State.Outcome == nil || toolTask.State.Outcome.Status != "completed" {
		t.Fatalf("tool task terminal %#v", toolTask.State)
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
	h := &harness.Harness{Session: sess, Model: model, Tools: map[string]harness.ToolDef{}}
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
	if h.Usage().ModelCalls != 1 {
		t.Fatalf("usage %#v", h.Usage())
	}
}

func TestCompactionSetsHead(t *testing.T) {
	ctx := context.Background()
	sess := session.New(storage.NewMemory())
	if _, err := sess.CreateConversation(ctx, record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	h := &harness.Harness{Session: sess, Model: &scripted{steps: []agent.Message{
		{Role: "assistant", Content: "a"},
		{Role: "assistant", Content: "b"},
	}}, Tools: map[string]harness.ToolDef{}}
	if _, err := h.Prompt(ctx, 1, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Prompt(ctx, 1, "two"); err != nil {
		t.Fatal(err)
	}
	if err := h.Compact(ctx, 1, 1, func(msgs []agent.Message) string { return "summary" }); err != nil {
		t.Fatal(err)
	}
	marker, err := sess.FindLatestHeadMarker(ctx, 1, nil)
	if err != nil || marker == nil || marker.Kind != "pi.compaction" {
		t.Fatalf("compaction marker %#v %v", marker, err)
	}
	page, err := sess.ScanEntries(ctx, storage.EntryQuery{ConversationID: 1, MinEntryID: marker.Head, Order: storage.Ascending}, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	// context after compaction: the compacted summary entry plus retained tail.
	if len(page.Items) < 2 {
		t.Fatalf("post-compaction context %d", len(page.Items))
	}
}

func TestEvents(t *testing.T) {
	ctx := context.Background()
	sess := session.New(storage.NewMemory())
	if _, err := sess.CreateConversation(ctx, record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	model := &scripted{steps: []agent.Message{{Role: "assistant", Content: "ok"}}}
	h := &harness.Harness{Session: sess, Model: model, Tools: map[string]harness.ToolDef{}}
	var got []string
	h.On("user", func(any) { got = append(got, "user") })
	h.On("assistant", func(any) { got = append(got, "assistant") })
	if _, err := h.Prompt(ctx, 1, "hi"); err != nil {
		t.Fatal(err)
	}
	want := []string{"user", "assistant"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v want %v", got, want)
	}
}
