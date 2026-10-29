package record

import (
	"encoding/json"

	"harness/protocol"
)

type entryFields struct {
	ID             protocol.EntryID        `json:"id"`
	ConversationID protocol.ConversationID `json:"conversationId"`
	Kind           string                  `json:"kind"`
	Model          json.RawMessage         `json:"model,omitempty"`
	Data           json.RawMessage         `json:"data,omitempty"`
	Head           *protocol.EntryID       `json:"head,omitempty"`
	Edits          json.RawMessage         `json:"edits,omitempty"`
	ByTaskID       *protocol.TaskID        `json:"byTaskId,omitempty"`
}

type EntryRecord struct {
	entryFields
	extra map[string]json.RawMessage
}

func (r EntryRecord) Validate() error {
	if !r.ID.Valid() || !r.ConversationID.Valid() {
		return invalid("entry ids must be valid")
	}
	if r.Kind == "" {
		return invalid("entry kind must be non-empty")
	}
	if r.Head != nil && !r.Head.Valid() {
		return invalid("entry head must be a valid entry id")
	}
	if r.ByTaskID != nil && !r.ByTaskID.Valid() {
		return invalid("entry byTaskId must be a valid task id")
	}
	return nil
}

func (r EntryRecord) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(r.entryFields, r.extra)
}

func (r *EntryRecord) UnmarshalJSON(data []byte) error {
	return unmarshalWithExtra(data, &r.entryFields, &r.extra)
}
