package harness

import (
	"context"
	"encoding/json"

	"harness/record"
	"harness/task"
)

// ToolTaskDefinition is the durable pi.tool task. Its checkpoint carries the
// tool name, arguments, replay policy and an execution marker. A recovered
// unsafe call does not re-execute; a recovered safe call re-executes with the
// same stored arguments.
func ToolTaskDefinition(tools map[string]ToolDef) task.Definition {
	return task.Definition{
		Name: "pi.tool", Version: 1,
		Initial: func(input any) task.Checkpoint {
			return toolCheckpoint(input)
		},
		Phases: map[string]func(context.Context, *task.Runtime, task.Checkpoint) error{
			"execute": func(ctx context.Context, rt *task.Runtime, cp task.Checkpoint) error {
				return runToolPhase(ctx, rt, cp, tools)
			},
		},
	}
}

func toolCheckpoint(input any) task.Checkpoint {
	m, _ := input.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return task.Checkpoint{
		"phase": "execute", "name": m["name"], "args": m["args"],
		"replay": m["replay"], "callID": m["callID"], "started": false,
	}
}

func runToolPhase(ctx context.Context, rt *task.Runtime, cp task.Checkpoint, tools map[string]ToolDef) error {
	replay, _ := cp["replay"].(string)
	started, _ := cp["started"].(bool)
	if started && replay != "safe" {
		return rt.Done(ctx, task.Outcome{Status: "failed", Error: &record.TaskOutcomeError{Message: "interrupted"}})
	}
	name, _ := cp["name"].(string)
	tool, ok := tools[name]
	if !ok {
		return rt.Done(ctx, task.Outcome{Status: "failed", Error: &record.TaskOutcomeError{Message: "unknown tool"}})
	}
	if !started {
		next := task.Checkpoint{"phase": "execute", "name": name, "args": cp["args"], "replay": replay, "callID": cp["callID"], "started": true}
		if err := rt.Commit(ctx, next); err != nil {
			return err
		}
	}
	var args map[string]any
	if raw, ok := cp["args"].(json.RawMessage); ok {
		_ = json.Unmarshal(raw, &args)
	} else if b, err := json.Marshal(cp["args"]); err == nil {
		_ = json.Unmarshal(b, &args)
	}
	out, err := tool.Execute(ctx, args)
	if err != nil {
		out = "tool error: " + err.Error()
	}
	return rt.Done(ctx, task.Outcome{Status: "completed", Result: out})
}
