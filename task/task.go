package task

import (
	"context"
	"encoding/json"
	"fmt"

	"harness/protocol"
	"harness/record"
	"harness/session"
	"harness/storage"
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
	Phases  map[string]func(ctx context.Context, rt *Runtime, cp Checkpoint) error
	Abort   func(ctx context.Context, rt *Runtime, cp Checkpoint) error
}

// Runtime gives a running phase the task it belongs to.
type Runtime struct {
	session *session.Session
	ID      protocol.TaskID
}

func (r *Runtime) TaskID() protocol.TaskID { return r.ID }

// Commit stores a replacement checkpoint and keeps the task runnable.
func (r *Runtime) Commit(ctx context.Context, cp Checkpoint) error {
	cpJSON, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	return commitState(ctx, r.session, r.ID, record.TaskStateFields{Status: "running", Checkpoint: cpJSON})
}

// Wait parks the task until every task in on is terminal.
func (r *Runtime) Wait(ctx context.Context, on []protocol.TaskID, policy string, cp Checkpoint) error {
	cpJSON, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	onJSON, err := json.Marshal(on)
	if err != nil {
		return err
	}
	return commitState(ctx, r.session, r.ID, record.TaskStateFields{
		Status: "waiting", Checkpoint: cpJSON, On: onJSON, Policy: policy,
	})
}

// SleepUntil parks the task until the scheduler clock reaches until.
func (r *Runtime) SleepUntil(ctx context.Context, until float64, cp Checkpoint) error {
	cpJSON, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	return commitState(ctx, r.session, r.ID, record.TaskStateFields{
		Status: "sleeping", Checkpoint: cpJSON, Until: &until,
	})
}

// Done commits a terminal outcome.
func (r *Runtime) Done(ctx context.Context, out Outcome) error {
	var result json.RawMessage
	if out.Result != nil {
		b, err := json.Marshal(out.Result)
		if err != nil {
			return err
		}
		result = b
	}
	return commitState(ctx, r.session, r.ID, record.TaskStateFields{
		Status: "terminal",
		Outcome: &record.TaskOutcome{TaskOutcomeFields: record.TaskOutcomeFields{
			Status: out.Status, Result: result, Error: out.Error, Reason: out.Reason,
		}},
	})
}

func commitState(ctx context.Context, sess *session.Session, id protocol.TaskID, state record.TaskStateFields) error {
	_, err := sess.Commit(ctx, func(tx *session.Tx) error {
		rec, err := sess.Task(ctx, id)
		if err != nil || rec == nil {
			return fmt.Errorf("task %d not found", id)
		}
		rec.State = record.TaskState{TaskStateFields: state}
		tx.Task(*rec)
		return nil
	})
	return err
}

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

// Run drives one task through runnable phases until it becomes terminal or
// parks in a waiting or sleeping state.
func Run(ctx context.Context, sess *session.Session, def Definition, id protocol.TaskID) (*Outcome, error) {
	for {
		rec, err := sess.Task(ctx, id)
		if err != nil || rec == nil {
			return nil, fmt.Errorf("task %d not found", id)
		}
		switch rec.State.Status {
		case "completing", "terminal":
			return outcomeOf(rec), nil
		case "waiting", "sleeping":
			return nil, nil
		case "pending", "running":
			out, err := runPhase(ctx, sess, def, rec)
			if err != nil {
				return nil, err
			}
			if out != nil {
				return out, nil
			}
		}
	}
}

func runPhase(ctx context.Context, sess *session.Session, def Definition, rec *record.TaskRecord) (*Outcome, error) {
	var cp Checkpoint
	if err := json.Unmarshal(rec.State.Checkpoint, &cp); err != nil {
		return nil, err
	}
	phase, _ := cp["phase"].(string)
	handler := def.Phases[phase]
	if handler == nil {
		return nil, fmt.Errorf("task %q has no phase %q", def.Name, phase)
	}
	if err := handler(ctx, &Runtime{session: sess, ID: rec.ID}, cp); err != nil {
		return nil, err
	}
	updated, err := sess.Task(ctx, rec.ID)
	if err != nil || updated == nil {
		return nil, fmt.Errorf("task %d disappeared after phase", rec.ID)
	}
	if updated.State.Status == "terminal" || updated.State.Status == "completing" {
		return outcomeOf(updated), nil
	}
	return nil, nil
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

// Scheduler drives pending, waiting and sleeping tasks for a set of
// definitions. It is a minimal foreground scheduler: Resume makes one pass.
type Scheduler struct {
	Session *session.Session
	Defs    map[string]Definition
	Now     func() int64
}

func (s *Scheduler) Resume(ctx context.Context) error {
	for _, status := range []string{"pending", "running", "waiting", "sleeping"} {
		page, err := s.Session.ScanTasks(ctx, storage.TaskQuery{Status: status}, 1000, nil)
		if err != nil {
			return err
		}
		for _, rec := range page.Items {
			if err := s.resumeOne(ctx, rec); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Scheduler) resumeOne(ctx context.Context, rec record.TaskRecord) error {
	def, ok := s.Defs[rec.Kind]
	if !ok {
		return nil
	}
	switch rec.State.Status {
	case "pending", "running":
		_, err := Run(ctx, s.Session, def, rec.ID)
		return err
	case "waiting":
		var on []protocol.TaskID
		if err := json.Unmarshal(rec.State.On, &on); err != nil {
			return err
		}
		for _, id := range on {
			child, err := s.Session.Task(ctx, id)
			if err != nil {
				return err
			}
			if child == nil || (child.State.Status != "terminal" && child.State.Status != "completing") {
				return nil
			}
		}
		return wake(ctx, s.Session, rec)
	case "sleeping":
		if rec.State.Until == nil || s.Now() < int64(*rec.State.Until) {
			return nil
		}
		return wake(ctx, s.Session, rec)
	}
	return nil
}

func wake(ctx context.Context, sess *session.Session, rec record.TaskRecord) error {
	_, err := sess.Commit(ctx, func(tx *session.Tx) error {
		rec.State = record.TaskState{TaskStateFields: record.TaskStateFields{
			Status: "pending", Checkpoint: rec.State.Checkpoint,
		}}
		tx.Task(rec)
		return nil
	})
	return err
}
