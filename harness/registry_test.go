package harness_test

import (
	"testing"

	"harness/harness"
)

func TestRegistryResolution(t *testing.T) {
	r := harness.NewRegistry()
	r.Install(harness.Extension{
		Name:     "a",
		Tools:    map[string]harness.ToolDef{"read": {Name: "read"}},
		Sections: []string{"section-a"},
	})
	r.Install(harness.Extension{
		Name:     "b",
		Tools:    map[string]harness.ToolDef{"read": {Name: "read-override"}, "write": {Name: "write"}},
		Sections: []string{"section-b"},
	})
	tools := r.Tools()
	if len(tools) != 2 {
		t.Fatalf("tools %d", len(tools))
	}
	if tools["read"].Name != "read-override" {
		t.Fatalf("override %#v", tools["read"])
	}
	if r.SystemPrompt() != "section-a\nsection-b" {
		t.Fatalf("prompt %q", r.SystemPrompt())
	}
	r.Remove("a")
	if r.SystemPrompt() != "section-b" {
		t.Fatalf("after remove %q", r.SystemPrompt())
	}
}
