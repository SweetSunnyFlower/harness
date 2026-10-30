package agent

import (
	"context"
	"fmt"
)

// Message is one chat message.
type Message struct {
	Role      string     // "user", "assistant", "tool"
	Content   string     // text content
	ToolCalls []ToolCall // assistant-only
	ToolID    string     // tool result: the call it answers
}

// ToolCall is a model-requested tool invocation.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// Tool is a callable capability the model may request.
type Tool struct {
	Name        string
	Description string
	Execute     func(ctx context.Context, args map[string]any) (string, error)
}

// Model generates the next assistant message.
type Model interface {
	Generate(ctx context.Context, messages []Message, tools []Tool) (Message, error)
}

// StreamingModel generates an assistant message while emitting content
// deltas through onDelta.
type StreamingModel interface {
	GenerateStream(ctx context.Context, messages []Message, tools []Tool, onDelta func(string)) (Message, error)
}

// Agent runs the model-tool loop: call the model, execute any requested
// tools, append their results, and repeat until the model answers without
// tool calls.
type Agent struct {
	Model Model
	Tools map[string]Tool
}

func (a *Agent) Run(ctx context.Context, messages []Message) (Message, error) {
	tools := make([]Tool, 0, len(a.Tools))
	for _, t := range a.Tools {
		tools = append(tools, t)
	}
	for {
		resp, err := a.Model.Generate(ctx, messages, tools)
		if err != nil {
			return Message{}, err
		}
		if len(resp.ToolCalls) == 0 {
			return resp, nil
		}
		messages = append(messages, resp)
		for _, call := range resp.ToolCalls {
			tool, ok := a.Tools[call.Name]
			if !ok {
				messages = append(messages, Message{Role: "tool", ToolID: call.ID, Content: fmt.Sprintf("unknown tool %q", call.Name)})
				continue
			}
			content, err := tool.Execute(ctx, call.Args)
			if err != nil {
				content = "tool error: " + err.Error()
			}
			messages = append(messages, Message{Role: "tool", ToolID: call.ID, Content: content})
		}
	}
}
