package record

import (
	"encoding/json"
	"math"

	"harness/protocol"
)

type taskFields struct {
	Trace            json.RawMessage         `json:"trace,omitempty"`
	Links            json.RawMessage         `json:"links,omitempty"`
	ID               protocol.TaskID         `json:"id"`
	ConversationID   protocol.ConversationID `json:"conversationId"`
	Kind             string                  `json:"kind"`
	Version          float64                 `json:"version"`
	Input            json.RawMessage         `json:"input"`
	Owner            *protocol.TaskID        `json:"owner,omitempty"`
	Background       bool                    `json:"background"`
	AbortRequested   bool                    `json:"abortRequested"`
	AbortReason      string                  `json:"abortReason,omitempty"`
	AbandonOnRestart bool                    `json:"abandonOnRestart,omitempty"`
	StartedAt        *float64                `json:"startedAt,omitempty"`
	EndedAt          *float64                `json:"endedAt,omitempty"`
	Restarts         *float64                `json:"restarts,omitempty"`
	State            TaskState               `json:"state"`
	Memos            json.RawMessage         `json:"memos,omitempty"`
}

type TaskRecord struct {
	taskFields
	extra map[string]json.RawMessage
}

func (r TaskRecord) Validate() error {
	if !r.ID.Valid() || !r.ConversationID.Valid() {
		return invalid("task ids must be valid")
	}
	if r.Kind == "" {
		return invalid("task kind must be non-empty")
	}
	if r.Version != math.Trunc(r.Version) || r.Version < 1 {
		return invalid("task version must be a positive integer")
	}
	if r.Owner != nil && !r.Owner.Valid() {
		return invalid("task owner must be a valid task id")
	}
	if !present(r.Input) {
		return invalid("task input must be present")
	}
	if err := r.State.Validate(); err != nil {
		return err
	}
	return nil
}

func (r TaskRecord) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(r.taskFields, r.extra)
}

func (r *TaskRecord) UnmarshalJSON(data []byte) error {
	return unmarshalWithExtra(data, &r.taskFields, &r.extra)
}

type taskStateFields struct {
	Status     string          `json:"status"`
	Checkpoint json.RawMessage `json:"checkpoint,omitempty"`
	On         json.RawMessage `json:"on,omitempty"`
	Policy     string          `json:"policy,omitempty"`
	Until      *float64        `json:"until,omitempty"`
	Outcome    *TaskOutcome    `json:"outcome,omitempty"`
}

type TaskState struct {
	taskStateFields
	extra map[string]json.RawMessage
}

func (s TaskState) Validate() error {
	if !oneOf(s.Status, "pending", "running", "waiting", "sleeping", "completing", "terminal") {
		return invalid("unknown task state status %q", s.Status)
	}
	switch s.Status {
	case "pending", "running":
		if !present(s.Checkpoint) {
			return invalid("%s task must carry a checkpoint", s.Status)
		}
	case "waiting":
		if !present(s.Checkpoint) || !present(s.On) || !oneOf(s.Policy, "failFast", "allSettled") {
			return invalid("waiting task must carry checkpoint, on and policy")
		}
	case "sleeping":
		if !present(s.Checkpoint) || s.Until == nil {
			return invalid("sleeping task must carry checkpoint and until")
		}
	case "completing", "terminal":
		if s.Outcome == nil {
			return invalid("%s task must carry an outcome", s.Status)
		}
	}
	if s.Outcome != nil {
		if err := s.Outcome.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (s TaskState) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(s.taskStateFields, s.extra)
}

func (s *TaskState) UnmarshalJSON(data []byte) error {
	return unmarshalWithExtra(data, &s.taskStateFields, &s.extra)
}

type taskOutcomeFields struct {
	Status string            `json:"status"`
	Result json.RawMessage   `json:"result,omitempty"`
	Error  *TaskOutcomeError `json:"error,omitempty"`
	Reason string            `json:"reason,omitempty"`
}

type TaskOutcome struct {
	taskOutcomeFields
	extra map[string]json.RawMessage
}

func (o TaskOutcome) Validate() error {
	if !oneOf(o.Status, "completed", "failed", "aborted", "orphaned", "faulted") {
		return invalid("unknown task outcome status %q", o.Status)
	}
	switch o.Status {
	case "completed":
		if !present(o.Result) {
			return invalid("completed outcome must carry a result")
		}
	case "failed":
		if o.Error == nil {
			return invalid("failed outcome must carry an error")
		}
	case "aborted":
		if o.Error != nil {
			return invalid("aborted outcome must not carry an error")
		}
	case "orphaned":
		if o.Reason == "" || present(o.Result) || o.Error != nil {
			return invalid("orphaned outcome must carry only a reason")
		}
	case "faulted":
		if o.Error == nil {
			return invalid("faulted outcome must carry an error")
		}
	}
	if o.Error != nil {
		if o.Error.Message == "" {
			return invalid("task outcome error must carry a message")
		}
	}
	return nil
}

func (o TaskOutcome) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(o.taskOutcomeFields, o.extra)
}

func (o *TaskOutcome) UnmarshalJSON(data []byte) error {
	return unmarshalWithExtra(data, &o.taskOutcomeFields, &o.extra)
}

type TaskOutcomeError struct {
	Message string          `json:"message"`
	Detail  json.RawMessage `json:"detail,omitempty"`
}
