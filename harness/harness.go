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
	Tools   map[string]agent.Tool
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
		resp, err := h.Model.Generate(ctx, messages, toolList(h.Tools))
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
			tool, ok := h.Tools[call.Name]
			content := fmt.Sprintf("unknown tool %q", call.Name)
			if ok {
				if out, err := tool.Execute(ctx, call.Args); err == nil {
					content = out
				} else {
					content = "tool error: " + err.Error()
				}
			}
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
	page, err := h.Session.ScanEntries(ctx, storage.EntryQuery{ConversationID: conversationID, Order: storage.Ascending}, 10000, nil)
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

func toolList(tools map[string]agent.Tool) []agent.Tool {
	out := make([]agent.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, t)
	}
	return out
}
