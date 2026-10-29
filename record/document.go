package record

import (
	"encoding/json"

	"harness/protocol"
)

type documentFields struct {
	ID        protocol.DocumentID `json:"id"`
	Kind      string              `json:"kind"`
	Key       string              `json:"key,omitempty"`
	CreatedAt protocol.Seq        `json:"createdAt"`
	RetiredAt *protocol.Seq       `json:"retiredAt,omitempty"`
	Scope     DocumentScope       `json:"scope"`
	History   string              `json:"history,omitempty"`
	Fork      string              `json:"fork,omitempty"`
}

type DocumentRecord struct {
	documentFields
	extra map[string]json.RawMessage
}

type documentCreateFields struct {
	ID      protocol.DocumentID `json:"id"`
	Kind    string              `json:"kind"`
	Key     string              `json:"key,omitempty"`
	Scope   DocumentScope       `json:"scope"`
	History string              `json:"history,omitempty"`
	Fork    string              `json:"fork,omitempty"`
}

type DocumentCreate struct {
	documentCreateFields
	extra map[string]json.RawMessage
}

func (r DocumentRecord) Validate() error {
	if !r.ID.Valid() || !r.KindValid() {
		return invalid("document id and kind must be valid")
	}
	if !r.CreatedAt.Valid() {
		return invalid("document createdAt must be a valid sequence")
	}
	if r.RetiredAt != nil && !r.RetiredAt.Valid() {
		return invalid("document retiredAt must be a valid sequence")
	}
	return r.validateScope()
}

func (r DocumentCreate) Validate() error {
	if !r.ID.Valid() || !r.KindValid() {
		return invalid("document id and kind must be valid")
	}
	return r.validateScope()
}

func (r DocumentRecord) KindValid() bool {
	return r.Kind != ""
}

func (r DocumentCreate) KindValid() bool {
	return r.Kind != ""
}

func (r DocumentRecord) validateScope() error {
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	if r.Scope.Kind == "conversation" {
		if !oneOf(r.History, "latest", "rewindable") {
			return invalid("unknown document history %q", r.History)
		}
		if !oneOf(r.Fork, "current", "initial", "asOf") {
			return invalid("unknown document fork %q", r.Fork)
		}
		if r.History == "latest" && r.Fork == "asOf" {
			return invalid("latest document cannot fork asOf")
		}
	}
	return nil
}

func (r DocumentCreate) validateScope() error {
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	if r.Scope.Kind == "conversation" {
		if !oneOf(r.History, "latest", "rewindable") {
			return invalid("unknown document history %q", r.History)
		}
		if !oneOf(r.Fork, "current", "initial", "asOf") {
			return invalid("unknown document fork %q", r.Fork)
		}
		if r.History == "latest" && r.Fork == "asOf" {
			return invalid("latest document cannot fork asOf")
		}
	}
	return nil
}

func (r DocumentRecord) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(r.documentFields, r.extra)
}

func (r *DocumentRecord) UnmarshalJSON(data []byte) error {
	return unmarshalWithExtra(data, &r.documentFields, &r.extra)
}

func (r DocumentCreate) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(r.documentCreateFields, r.extra)
}

func (r *DocumentCreate) UnmarshalJSON(data []byte) error {
	return unmarshalWithExtra(data, &r.documentCreateFields, &r.extra)
}

type documentScopeFields struct {
	Kind           string                   `json:"kind"`
	ConversationID *protocol.ConversationID `json:"conversationId,omitempty"`
	TaskID         *protocol.TaskID         `json:"taskId,omitempty"`
}

type DocumentScope struct {
	documentScopeFields
	extra map[string]json.RawMessage
}

func (s DocumentScope) Validate() error {
	if !oneOf(s.Kind, "session", "conversation", "task") {
		return invalid("unknown document scope kind %q", s.Kind)
	}
	switch s.Kind {
	case "session":
		if s.ConversationID != nil || s.TaskID != nil {
			return invalid("session document must not carry an owner id")
		}
	case "conversation":
		if s.ConversationID == nil || !s.ConversationID.Valid() || s.TaskID != nil {
			return invalid("conversation document must carry only a conversation id")
		}
	case "task":
		if s.TaskID == nil || !s.TaskID.Valid() || s.ConversationID != nil {
			return invalid("task document must carry only a task id")
		}
	}
	return nil
}

func (s DocumentScope) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(s.documentScopeFields, s.extra)
}

func (s *DocumentScope) UnmarshalJSON(data []byte) error {
	return unmarshalWithExtra(data, &s.documentScopeFields, &s.extra)
}
