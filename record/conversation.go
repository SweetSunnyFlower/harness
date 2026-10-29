package record

import (
	"encoding/json"

	"harness/protocol"
)

type ConversationFields struct {
	ID     protocol.ConversationID `json:"id"`
	Parent *ConversationParent     `json:"parent,omitempty"`
	Owner  *ConversationOwner      `json:"owner,omitempty"`
}

type ConversationParent struct {
	ConversationID protocol.ConversationID `json:"conversationId"`
	At             protocol.EntryID        `json:"at"`
}

type ConversationOwner struct {
	ConversationID protocol.ConversationID `json:"conversationId"`
	TaskID         protocol.TaskID         `json:"taskId"`
}

type ConversationRecord struct {
	ConversationFields
	extra map[string]json.RawMessage
}

func (r ConversationRecord) Validate() error {
	if !r.ID.Valid() {
		return invalid("conversation id must be a positive safe integer")
	}
	if r.Parent != nil {
		if !r.Parent.ConversationID.Valid() || !r.Parent.At.Valid() {
			return invalid("conversation parent ids must be valid")
		}
	}
	if r.Owner != nil {
		if !r.Owner.ConversationID.Valid() || !r.Owner.TaskID.Valid() {
			return invalid("conversation owner ids must be valid")
		}
	}
	return nil
}

func (r ConversationRecord) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(r.ConversationFields, r.extra)
}

func (r *ConversationRecord) UnmarshalJSON(data []byte) error {
	return unmarshalWithExtra(data, &r.ConversationFields, &r.extra)
}
