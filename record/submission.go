package record

import (
	"encoding/json"

	"harness/protocol"
)

type SubmissionFields struct {
	Trace          json.RawMessage         `json:"trace,omitempty"`
	ID             protocol.SubmissionID   `json:"id"`
	ConversationID protocol.ConversationID `json:"conversationId"`
	RequestID      string                  `json:"requestId,omitempty"`
	Type           string                  `json:"type"`
	Status         string                  `json:"status"`
	Entry          *protocol.EntryID       `json:"entry,omitempty"`
	Answer         *protocol.EntryID       `json:"answer,omitempty"`
	Reason         string                  `json:"reason,omitempty"`
	Detail         json.RawMessage         `json:"detail,omitempty"`
}

type SubmissionRecord struct {
	SubmissionFields
	extra map[string]json.RawMessage
}

func (r SubmissionRecord) Validate() error {
	if !r.ID.Valid() || !r.ConversationID.Valid() {
		return invalid("submission ids must be valid")
	}
	if !oneOf(r.Type, "input", "write", "continue") {
		return invalid("unknown submission type %q", r.Type)
	}
	if !oneOf(r.Status, "queued", "placed", "done", "unanswered") {
		return invalid("unknown submission status %q", r.Status)
	}
	hasEntry := r.Entry != nil
	hasAnswer := r.Answer != nil
	switch r.Type {
	case "input":
		switch r.Status {
		case "queued":
			if hasEntry || hasAnswer {
				return invalid("queued input must not carry entry or answer")
			}
		case "placed":
			if !hasEntry || hasAnswer {
				return invalid("placed input must carry entry and no answer")
			}
		case "done":
			if !hasEntry || !hasAnswer {
				return invalid("done input must carry entry and answer")
			}
		case "unanswered":
			if r.Reason == "" {
				return invalid("unanswered input must carry a reason")
			}
		}
	case "write":
		switch r.Status {
		case "queued":
			if hasEntry || hasAnswer {
				return invalid("queued write must not carry entry or answer")
			}
		case "done":
			if !hasEntry || hasAnswer {
				return invalid("done write must carry entry and no answer")
			}
		case "unanswered":
			if hasEntry || hasAnswer || r.Reason == "" {
				return invalid("unanswered write must carry only a reason")
			}
		}
	case "continue":
		switch r.Status {
		case "placed":
			if hasEntry || hasAnswer {
				return invalid("placed continue must not carry entry or answer")
			}
		case "done":
			if hasEntry || !hasAnswer {
				return invalid("done continue must carry answer and no entry")
			}
		case "unanswered":
			if hasEntry || hasAnswer || r.Reason == "" {
				return invalid("unanswered continue must carry only a reason")
			}
		}
	}
	return nil
}

func (r SubmissionRecord) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(r.SubmissionFields, r.extra)
}

func (r *SubmissionRecord) UnmarshalJSON(data []byte) error {
	return unmarshalWithExtra(data, &r.SubmissionFields, &r.extra)
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
