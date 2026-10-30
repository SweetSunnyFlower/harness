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
	state := record.TaskStateFields{
		Status: "terminal",
		Outcome: &record.TaskOutcome{TaskOutcomeFields: record.TaskOutcomeFields{
			Status: out.Status, Result: result, Error: out.Error, Reason: out.Reason,
		}},
	}
	live, err := hasLiveOwned(ctx, r.session, r.ID)
	if err != nil {
		return err
	}
	if live {
		state.Status = "completing"
	}
	return commitState(ctx, r.session, r.ID, state)
}

func hasLiveOwned(ctx context.Context, sess *session.Session, id protocol.TaskID) (bool, error) {
	page, err := sess.ScanTasks(ctx, storage.TaskQuery{}, 10000, nil)
	if err != nil {
		return false, err
	}
	for _, rec := range page.Items {
		if rec.Owner != nil && *rec.Owner == id && rec.State.Status != "terminal" && rec.State.Status != "completing" {
			return true, nil
		}
	}
	return false, nil
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
	return CreateOwned(ctx, sess, def, id, conversationID, nil, input)
}

// CreateOwned writes a pending task with an owning task, for structured
// concurrency and cascading abort.
func CreateOwned(ctx context.Context, sess *session.Session, def Definition, id protocol.TaskID, conversationID protocol.ConversationID, owner *protocol.TaskID, input any) error {
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
			Input: inputJSON, Owner: owner, Background: false, AbortRequested: false,
			State: record.TaskState{TaskStateFields: record.TaskStateFields{Status: "pending", Checkpoint: cpJSON}},
		}})
		return nil
	})
	return err
}

// CreateBackground writes a conversation-owned background task, which is
// excluded from ordinary cascading aborts.
func CreateBackground(ctx context.Context, sess *session.Session, def Definition, id protocol.TaskID, conversationID protocol.ConversationID, input any) error {
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
			Input: inputJSON, Background: true, AbortRequested: false,
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
	for _, status := range []string{"pending", "running", "waiting", "sleeping", "completing"} {
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
		_, err := runPhase(ctx, s.Session, def, &rec)
		return err
	case "waiting":
		var on []protocol.TaskID
		if err := json.Unmarshal(rec.State.On, &on); err != nil {
			return err
		}
		if rec.State.Policy == "failFast" {
			for _, id := range on {
				child, err := s.Session.Task(ctx, id)
				if err != nil {
					return err
				}
				if child == nil {
					return fmt.Errorf("task %d not found", id)
				}
				if child.State.Status != "terminal" && child.State.Status != "completing" {
					continue
				}
				if child.State.Outcome == nil || (child.State.Outcome.Status != "failed" && child.State.Outcome.Status != "faulted") {
					continue
				}
				for _, other := range on {
					if other != id {
						if err := s.abortOne(ctx, other); err != nil {
							return err
						}
					}
				}
				return wake(ctx, s.Session, rec)
			}
			return nil
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
	case "completing":
		live, err := hasLiveOwned(ctx, s.Session, rec.ID)
		if err != nil {
			return err
		}
		if live {
			return nil
		}
		_, err = s.Session.Commit(ctx, func(tx *session.Tx) error {
			rec.State = record.TaskState{TaskStateFields: record.TaskStateFields{
				Status: "terminal", Outcome: rec.State.Outcome,
			}}
			tx.Task(rec)
			return nil
		})
		return err
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

// Recover reconciles tasks left "running" by a previous process. Each such
// task has its restart count incremented; at restartLimit it ends faulted,
// otherwise it returns to pending for the scheduler to run again.
func Recover(ctx context.Context, sess *session.Session, restartLimit int) error {
	page, err := sess.ScanTasks(ctx, storage.TaskQuery{Status: "running"}, 10000, nil)
	if err != nil {
		return err
	}
	for _, rec := range page.Items {
		restarts := 0.0
		if rec.Restarts != nil {
			restarts = *rec.Restarts
		}
		restarts++
		if restartLimit > 0 && int(restarts) >= restartLimit {
			msg := "restart_limit"
			if err := commitState(ctx, sess, rec.ID, record.TaskStateFields{
				Status: "terminal",
				Outcome: &record.TaskOutcome{TaskOutcomeFields: record.TaskOutcomeFields{
					Status: "faulted",
					Error:  &record.TaskOutcomeError{Message: msg},
				}},
			}); err != nil {
				return err
			}
			continue
		}
		if err := commitState(ctx, sess, rec.ID, record.TaskStateFields{
			Status: "pending", Checkpoint: rec.State.Checkpoint,
		}); err != nil {
			return err
		}
		// Persist the incremented restart count.
		if err := setRestarts(ctx, sess, rec.ID, restarts); err != nil {
			return err
		}
	}
	return nil
}

func setRestarts(ctx context.Context, sess *session.Session, id protocol.TaskID, restarts float64) error {
	_, err := sess.Commit(ctx, func(tx *session.Tx) error {
		rec, err := sess.Task(ctx, id)
		if err != nil || rec == nil {
			return fmt.Errorf("task %d not found", id)
		}
		rec.Restarts = &restarts
		tx.Task(*rec)
		return nil
	})
	return err
}

// Abort aborts a task and, bottom-up, every live task it owns.
func (s *Scheduler) Abort(ctx context.Context, id protocol.TaskID) error {
	owned, err := s.collectOwned(ctx, id)
	if err != nil {
		return err
	}
	for i := len(owned) - 1; i >= 0; i-- {
		if err := s.abortOne(ctx, owned[i]); err != nil {
			return err
		}
	}
	return s.abortOne(ctx, id)
}

func (s *Scheduler) collectOwned(ctx context.Context, id protocol.TaskID) ([]protocol.TaskID, error) {
	page, err := s.Session.ScanTasks(ctx, storage.TaskQuery{}, 10000, nil)
	if err != nil {
		return nil, err
	}
	var out []protocol.TaskID
	var walk func(protocol.TaskID)
	walk = func(parent protocol.TaskID) {
		for _, rec := range page.Items {
			if rec.Owner != nil && *rec.Owner == parent {
				out = append(out, rec.ID)
				walk(rec.ID)
			}
		}
	}
	walk(id)
	return out, nil
}

func (s *Scheduler) abortOne(ctx context.Context, id protocol.TaskID) error {
	rec, err := s.Session.Task(ctx, id)
	if err != nil || rec == nil {
		return fmt.Errorf("task %d not found", id)
	}
	switch rec.State.Status {
	case "terminal", "completing":
		return nil
	}
	def, ok := s.Defs[rec.Kind]
	if ok && def.Abort != nil {
		var cp Checkpoint
		if err := json.Unmarshal(rec.State.Checkpoint, &cp); err == nil {
			if err := def.Abort(ctx, &Runtime{session: s.Session, ID: id}, cp); err != nil {
				return err
			}
			return nil
		}
	}
	return commitState(ctx, s.Session, id, record.TaskStateFields{
		Status: "terminal",
		Outcome: &record.TaskOutcome{TaskOutcomeFields: record.TaskOutcomeFields{
			Status: "aborted",
		}},
	})
}
