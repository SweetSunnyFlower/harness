package tools

import (
	"sort"
	"strings"

	"harness/harness"
)

// Search ranks tools by how well their name and description match the query.
// Name matches outrank description matches; ties keep name order.
func Search(toolMap map[string]harness.ToolDef, query string, limit int) []harness.ToolDef {
	query = strings.ToLower(strings.TrimSpace(query))
	tokens := strings.Fields(query)
	type scored struct {
		tool  harness.ToolDef
		score int
	}
	var items []scored
	for name, tool := range toolMap {
		nameLower := strings.ToLower(name)
		descLower := strings.ToLower(tool.Description)
		score := 0
		if query != "" {
			if strings.Contains(nameLower, query) {
				score += 100
			}
			if strings.Contains(descLower, query) {
				score += 20
			}
			for _, token := range tokens {
				if token != "" && strings.Contains(nameLower, token) {
					score += 5
				}
				if token != "" && strings.Contains(descLower, token) {
					score += 1
				}
			}
		}
		if score == 0 && query != "" {
			continue
		}
		items = append(items, scored{tool: tool, score: score})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].score > items[j].score })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]harness.ToolDef, 0, len(items))
	for _, item := range items {
		out = append(out, item.tool)
	}
	return out
}
