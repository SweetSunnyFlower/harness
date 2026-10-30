package harness

import (
	"context"
	"encoding/json"
	"fmt"

	"harness/agent"
	"harness/protocol"
	"harness/record"
	"harness/session"
	"harness/storage"
)

// Harness runs a durable agent over a Session: every model and tool message
// becomes an entry, so a turn can be recovered and resumed from storage.
type Harness struct {
	Session *session.Session
	Model   agent.Model
	Tools   map[string]ToolDef
}

// ToolDef is a tool plus its durable replay policy.
type ToolDef struct {
	Name        string
	Description string
	Replay      string // "safe" | "unsafe"
	Execute     func(ctx context.Context, args map[string]any) (string, error)
}

// Prompt appends a user input and runs the generation loop to completion.
func (h *Harness) Prompt(ctx context.Context, conversationID protocol.ConversationID, input string) (string, error) {
	if err := h.appendEntry(ctx, conversationID, "pi.user", agent.Message{Role: "user", Content: input}); err != nil {
		return "", err
	}
	for {
		messages, err := h.messages(ctx, conversationID)
		if err != nil {
			return "", err
		}
		resp, err := h.Model.Generate(ctx, messages, h.modelTools())
		if err != nil {
			return "", err
		}
		if err := h.appendEntry(ctx, conversationID, "pi.assistant", resp); err != nil {
			return "", err
		}
		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}
		for _, call := range resp.ToolCalls {
			content := h.executeTool(ctx, conversationID, call)
			if err := h.appendEntry(ctx, conversationID, "pi.tool-result", agent.Message{Role: "tool", ToolID: call.ID, Content: content}); err != nil {
				return "", err
			}
		}
	}
}

// Submit admits a user input with optional deduplication, runs the generation
// to completion, and settles the submission as done with its answer entry.
func (h *Harness) Submit(ctx context.Context, conversationID protocol.ConversationID, input, requestID string) (protocol.SubmissionID, error) {
	if requestID != "" {
		if existing, err := h.Session.SubmissionByRequest(ctx, conversationID, requestID); err == nil && existing != nil {
			return existing.ID, nil
		}
	}
	id, err := h.Session.MintID(ctx)
	if err != nil {
		return 0, err
	}
	if _, err := h.Session.Commit(ctx, func(tx *session.Tx) error {
		tx.Submission(record.SubmissionRecord{SubmissionFields: record.SubmissionFields{
			ID: protocol.SubmissionID(id), ConversationID: conversationID, RequestID: requestID,
			Type: "input", Status: "queued",
		}})
		return nil
	}); err != nil {
		return 0, err
	}
	if _, err := h.Prompt(ctx, conversationID, input); err != nil {
		return 0, err
	}
	page, err := h.Session.ScanEntries(ctx, storage.EntryQuery{ConversationID: conversationID, Order: storage.Descending}, 1, nil)
	if err != nil || len(page.Items) == 0 {
		return 0, fmt.Errorf("no answer entry after generation")
	}
	answerID := page.Items[0].ID
	userPage, err := h.Session.ScanEntries(ctx, storage.EntryQuery{ConversationID: conversationID, Order: storage.Ascending}, 1, nil)
	if err != nil || len(userPage.Items) == 0 {
		return 0, fmt.Errorf("no user entry")
	}
	entryID := userPage.Items[0].ID
	sub, err := h.Session.Submission(ctx, protocol.SubmissionID(id))
	if err != nil || sub == nil {
		return 0, fmt.Errorf("submission %d missing", id)
	}
	sub.Status = "done"
	sub.Entry = &entryID
	sub.Answer = &answerID
	if _, err := h.Session.Commit(ctx, func(tx *session.Tx) error {
		tx.Submission(*sub)
		return nil
	}); err != nil {
		return 0, err
	}
	return protocol.SubmissionID(id), nil
}

func (h *Harness) appendEntry(ctx context.Context, conversationID protocol.ConversationID, kind string, msg agent.Message) error {
	id, err := h.Session.MintID(ctx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = h.Session.Commit(ctx, func(tx *session.Tx) error {
		tx.Entry(record.EntryRecord{EntryFields: record.EntryFields{
			ID: protocol.EntryID(id), ConversationID: conversationID, Kind: kind, Data: data,
		}})
		return nil
	})
	return err
}

func (h *Harness) messages(ctx context.Context, conversationID protocol.ConversationID) ([]agent.Message, error) {
	marker, err := h.Session.FindLatestHeadMarker(ctx, conversationID, nil)
	if err != nil {
		return nil, err
	}
	var minID *protocol.EntryID
	if marker != nil && marker.Head != nil {
		minID = marker.Head
	}
	page, err := h.Session.ScanEntries(ctx, storage.EntryQuery{ConversationID: conversationID, MinEntryID: minID, Order: storage.Ascending}, 10000, nil)
	if err != nil {
		return nil, err
	}
	var out []agent.Message
	for _, e := range page.Items {
		if len(e.Data) == 0 {
			continue
		}
		var msg agent.Message
		if err := json.Unmarshal(e.Data, &msg); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

// Compact summarizes the oldest entries into one pi.compaction entry and sets
// its head to the first retained entry, so model context starts there.
func (h *Harness) Compact(ctx context.Context, conversationID protocol.ConversationID, keep int, summarize func([]agent.Message) string) error {
	page, err := h.Session.ScanEntries(ctx, storage.EntryQuery{ConversationID: conversationID, Order: storage.Ascending}, 10000, nil)
	if err != nil {
		return err
	}
	if len(page.Items) <= keep || keep < 1 {
		return nil
	}
	compacted := page.Items[:len(page.Items)-keep]
	retained := page.Items[len(page.Items)-keep:]
	var msgs []agent.Message
	for _, e := range compacted {
		if len(e.Data) == 0 {
			continue
		}
		var msg agent.Message
		if err := json.Unmarshal(e.Data, &msg); err != nil {
			return err
		}
		msgs = append(msgs, msg)
	}
	summary := summarize(msgs)
	id, err := h.Session.MintID(ctx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(agent.Message{Role: "assistant", Content: summary})
	if err != nil {
		return err
	}
	head := retained[0].ID
	_, err = h.Session.Commit(ctx, func(tx *session.Tx) error {
		tx.Entry(record.EntryRecord{EntryFields: record.EntryFields{
			ID: protocol.EntryID(id), ConversationID: conversationID, Kind: "pi.compaction",
			Data: data, Head: &head,
		}})
		return nil
	})
	return err
}

func (h *Harness) modelTools() []agent.Tool {
	out := make([]agent.Tool, 0, len(h.Tools))
	for _, t := range h.Tools {
		out = append(out, agent.Tool{Name: t.Name, Description: t.Description})
	}
	return out
}

// executeTool durably records the call intent before running the tool, then
// writes the tool task's result.
func (h *Harness) executeTool(ctx context.Context, conversationID protocol.ConversationID, call agent.ToolCall) string {
	taskID, err := h.Session.MintID(ctx)
	if err != nil {
		return "tool error: " + err.Error()
	}
	tool, ok := h.Tools[call.Name]
	replay := "unsafe"
	if ok {
		replay = tool.Replay
	}
	argsJSON, _ := json.Marshal(call.Args)
	checkpoint := map[string]any{"name": call.Name, "args": json.RawMessage(argsJSON), "replay": replay, "callID": call.ID}
	cpJSON, _ := json.Marshal(checkpoint)
	if _, err := h.Session.Commit(ctx, func(tx *session.Tx) error {
		tx.Task(record.TaskRecord{TaskFields: record.TaskFields{
			ID: protocol.TaskID(taskID), ConversationID: conversationID, Kind: "pi.tool", Version: 1,
			Input: json.RawMessage(argsJSON), Background: false, AbortRequested: false,
			State: record.TaskState{TaskStateFields: record.TaskStateFields{Status: "pending", Checkpoint: cpJSON}},
		}})
		return nil
	}); err != nil {
		return "tool error: " + err.Error()
	}
	if !ok {
		return fmt.Sprintf("unknown tool %q", call.Name)
	}
	out, err := tool.Execute(ctx, call.Args)
	if err != nil {
		out = "tool error: " + err.Error()
	}
	resultJSON, _ := json.Marshal(map[string]any{"output": out})
	h.Session.Commit(ctx, func(tx *session.Tx) error {
		rec, err := h.Session.Task(ctx, protocol.TaskID(taskID))
		if err != nil || rec == nil {
			return fmt.Errorf("tool task %d missing", taskID)
		}
		rec.State = record.TaskState{TaskStateFields: record.TaskStateFields{
			Status: "terminal",
			Outcome: &record.TaskOutcome{TaskOutcomeFields: record.TaskOutcomeFields{
				Status: "completed", Result: resultJSON,
			}},
		}}
		tx.Task(*rec)
		return nil
	})
	return out
}
