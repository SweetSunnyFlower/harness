package agent_test

import (
	"context"
	"testing"

	"harness/agent"
)

type scriptedModel struct {
	steps []agent.Message
}

func (m *scriptedModel) Generate(ctx context.Context, messages []agent.Message, tools []agent.Tool) (agent.Message, error) {
	next := m.steps[0]
	m.steps = m.steps[1:]
	return next, nil
}

func TestAgentToolLoop(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{steps: []agent.Message{
		{Role: "assistant", ToolCalls: []agent.ToolCall{{ID: "c1", Name: "echo", Args: map[string]any{"text": "hi"}}}},
		{Role: "assistant", Content: "done"},
	}}
	var echoArgs map[string]any
	a := &agent.Agent{
		Model: model,
		Tools: map[string]agent.Tool{
			"echo": {Name: "echo", Execute: func(ctx context.Context, args map[string]any) (string, error) {
				echoArgs = args
				return args["text"].(string), nil
			}},
		},
	}
	out, err := a.Run(ctx, []agent.Message{{Role: "user", Content: "echo hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "done" {
		t.Fatalf("final %q", out.Content)
	}
	if echoArgs == nil || echoArgs["text"] != "hi" {
		t.Fatalf("echo args %v", echoArgs)
	}
	if len(model.steps) != 0 {
		t.Fatal("model steps not consumed")
	}
}

func TestUnknownTool(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{steps: []agent.Message{
		{Role: "assistant", ToolCalls: []agent.ToolCall{{ID: "c1", Name: "missing"}}},
		{Role: "assistant", Content: "done"},
	}}
	a := &agent.Agent{Model: model, Tools: map[string]agent.Tool{}}
	out, err := a.Run(ctx, []agent.Message{{Role: "user", Content: "x"}})
	if err != nil || out.Content != "done" {
		t.Fatalf("%v %v", out, err)
	}
}
