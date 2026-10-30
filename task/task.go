package task

import (
	"context"
	"encoding/json"
	"fmt"

	"harness/protocol"
	"harness/record"
	"harness/session"
)

// Checkpoint is the durable state a task resumes from. It must carry a string
// "phase" key that selects the next handler.
type Checkpoint map[string]any

// Outcome is a terminal result committed for a task.
type Outcome struct {
	Status string // completed | failed | aborted | orphaned | faulted
	Result any
	Reason string
	Error  *record.TaskOutcomeError
}

// Definition is an executable durable state machine.
type Definition struct {
	Name    string
	Version float64
	Initial func(input any) Checkpoint
	Phases  map[string]func(ctx context.Context, rt *Runtime, cp Checkpoint) (Checkpoint, *Outcome, error)
	Abort   func(ctx context.Context, rt *Runtime, cp Checkpoint) error
}

// Runtime gives a running phase the task it belongs to.
type Runtime struct {
	session *session.Session
	ID      protocol.TaskID
}

func (r *Runtime) TaskID() protocol.TaskID { return r.ID }

// Create writes a pending task with its initial checkpoint.
func Create(ctx context.Context, sess *session.Session, def Definition, id protocol.TaskID, conversationID protocol.ConversationID, input any) error {
	cp := def.Initial(input)
	cpJSON, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return err
	}
	_, err = sess.Commit(ctx, func(tx *session.Tx) error {
		tx.Task(record.TaskRecord{TaskFields: record.TaskFields{
			ID: id, ConversationID: conversationID, Kind: def.Name, Version: def.Version,
			Input: inputJSON, Background: false, AbortRequested: false,
			State: record.TaskState{TaskStateFields: record.TaskStateFields{Status: "pending", Checkpoint: cpJSON}},
		}})
		return nil
	})
	return err
}

// Run drives a task to a terminal state by dispatching its phases in order.
func Run(ctx context.Context, sess *session.Session, def Definition, id protocol.TaskID) (*Outcome, error) {
	for {
		rec, err := sess.Task(ctx, id)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return nil, fmt.Errorf("task %d does not exist", id)
		}
		switch rec.State.Status {
		case "pending", "running":
			var cp Checkpoint
			if err := json.Unmarshal(rec.State.Checkpoint, &cp); err != nil {
				return nil, err
			}
			phase, _ := cp["phase"].(string)
			handler := def.Phases[phase]
			if handler == nil {
				return nil, fmt.Errorf("task %q has no phase %q", def.Name, phase)
			}
			next, outcome, err := handler(ctx, &Runtime{session: sess, ID: id}, cp)
			if err != nil {
				return nil, err
			}
			if outcome != nil {
				if err := commitTerminal(ctx, sess, rec, *outcome); err != nil {
					return nil, err
				}
				return outcome, nil
			}
			nextJSON, err := json.Marshal(next)
			if err != nil {
				return nil, err
			}
			if err := commitRunning(ctx, sess, rec, nextJSON); err != nil {
				return nil, err
			}
		case "completing", "terminal":
			return outcomeOf(rec), nil
		default:
			return nil, fmt.Errorf("task %d has unknown state %q", id, rec.State.Status)
		}
	}
}

func commitRunning(ctx context.Context, sess *session.Session, rec *record.TaskRecord, checkpoint json.RawMessage) error {
	_, err := sess.Commit(ctx, func(tx *session.Tx) error {
		rec.State = record.TaskState{TaskStateFields: record.TaskStateFields{Status: "running", Checkpoint: checkpoint}}
		tx.Task(*rec)
		return nil
	})
	return err
}

func commitTerminal(ctx context.Context, sess *session.Session, rec *record.TaskRecord, out Outcome) error {
	var result json.RawMessage
	if out.Result != nil {
		b, err := json.Marshal(out.Result)
		if err != nil {
			return err
		}
		result = b
	}
	_, err := sess.Commit(ctx, func(tx *session.Tx) error {
		rec.State = record.TaskState{TaskStateFields: record.TaskStateFields{
			Status: "terminal",
			Outcome: &record.TaskOutcome{TaskOutcomeFields: record.TaskOutcomeFields{
				Status: out.Status, Result: result, Error: out.Error, Reason: out.Reason,
			}},
		}}
		tx.Task(*rec)
		return nil
	})
	return err
}

func outcomeOf(rec *record.TaskRecord) *Outcome {
	if rec.State.Outcome == nil {
		return &Outcome{Status: "faulted"}
	}
	o := rec.State.Outcome
	var result any
	if len(o.Result) > 0 {
		_ = json.Unmarshal(o.Result, &result)
	}
	return &Outcome{Status: o.Status, Result: result, Reason: o.Reason, Error: o.Error}
}
