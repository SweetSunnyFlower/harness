package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"harness/harness"
)

// Edit is one exact-text replacement.
type Edit struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

func DetectLineEnding(content string) string {
	crlf := strings.Index(content, "\r\n")
	lf := strings.Index(content, "\n")
	if lf == -1 || crlf == -1 {
		return "\n"
	}
	if crlf < lf {
		return "\r\n"
	}
	return "\n"
}

func NormalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

func RestoreLineEndings(text, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

func NormalizeForFuzzyMatch(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	text = strings.Join(lines, "\n")
	replacer := strings.NewReplacer(
		"\u2018", "'", "\u2019", "'", "\u201A", "'", "\u201B", "'",
		"\u201C", `"`, "\u201D", `"`, "\u201E", `"`, "\u201F", `"`,
		"\u2010", "-", "\u2011", "-", "\u2012", "-", "\u2013", "-",
		"\u2014", "-", "\u2015", "-", "\u2212", "-",
		"\u00A0", " ", "\u2002", " ", "\u2003", " ", "\u2004", " ",
		"\u2005", " ", "\u2006", " ", "\u2007", " ", "\u2008", " ",
		"\u2009", " ", "\u200A", " ", "\u202F", " ", "\u205F", " ",
		"\u3000", " ",
	)
	return replacer.Replace(text)
}

func StripBom(content string) (string, string) {
	if strings.HasPrefix(content, "\uFEFF") {
		return "\uFEFF", content[1:]
	}
	return "", content
}

type fuzzyMatch struct {
	found                 bool
	index                 int
	matchLength           int
	usedFuzzyMatch        bool
	contentForReplacement string
}

func fuzzyFindText(content, oldText string) fuzzyMatch {
	if idx := strings.Index(content, oldText); idx != -1 {
		return fuzzyMatch{found: true, index: idx, matchLength: len(oldText), contentForReplacement: content}
	}
	fuzzyContent := NormalizeForFuzzyMatch(content)
	fuzzyOldText := NormalizeForFuzzyMatch(oldText)
	if idx := strings.Index(fuzzyContent, fuzzyOldText); idx != -1 {
		return fuzzyMatch{found: true, index: idx, matchLength: len(fuzzyOldText), usedFuzzyMatch: true, contentForReplacement: fuzzyContent}
	}
	return fuzzyMatch{contentForReplacement: content}
}

// ApplyEditsToNormalizedContent applies exact-text replacements to LF-only
// content, matching pi's edit tool semantics. It returns the base content and
// the edited content, or an error describing the first failed edit.
func ApplyEditsToNormalizedContent(normalizedContent string, edits []Edit, path string) (string, string, error) {
	type normalizedEdit struct{ oldText, newText string }
	normalized := make([]normalizedEdit, len(edits))
	for i, edit := range edits {
		normalized[i] = normalizedEdit{oldText: NormalizeToLF(edit.OldText), newText: NormalizeToLF(edit.NewText)}
	}
	for i, edit := range normalized {
		if edit.oldText == "" {
			return "", "", editError(path, i, len(normalized), emptyOldText)
		}
	}

	initial := make([]fuzzyMatch, len(normalized))
	usedFuzzy := false
	for i, edit := range normalized {
		initial[i] = fuzzyFindText(normalizedContent, edit.oldText)
		usedFuzzy = usedFuzzy || initial[i].usedFuzzyMatch
	}
	base := normalizedContent
	if usedFuzzy {
		base = NormalizeForFuzzyMatch(normalizedContent)
	}

	var matched []matchedEdit
	for i, edit := range normalized {
		m := fuzzyFindText(base, edit.oldText)
		if !m.found {
			return "", "", editError(path, i, len(normalized), notFound)
		}
		occurrences := strings.Count(NormalizeForFuzzyMatch(base), NormalizeForFuzzyMatch(edit.oldText))
		if occurrences > 1 {
			return "", "", editError(path, i, len(normalized), duplicate, occurrences)
		}
		matched = append(matched, matchedEdit{editIndex: i, matchIndex: m.index, matchLength: m.matchLength, newText: edit.newText})
	}
	sort.Slice(matched, func(a, b int) bool { return matched[a].matchIndex < matched[b].matchIndex })
	for i := 1; i < len(matched); i++ {
		prev, cur := matched[i-1], matched[i]
		if prev.matchIndex+prev.matchLength > cur.matchIndex {
			return "", "", fmt.Errorf("edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions.", prev.editIndex, cur.editIndex, path)
		}
	}

	newContent := applyReplacements(base, matched)
	if usedFuzzy {
		newContent = applyReplacementsPreservingUnchangedLines(normalizedContent, base, matched)
	}
	if normalizedContent == newContent {
		return "", "", editError(path, 0, len(normalized), noChange)
	}
	return normalizedContent, newContent, nil
}

type editErrorKind int

const (
	notFound editErrorKind = iota
	duplicate
	emptyOldText
	noChange
)

func editError(path string, editIndex, total int, kind editErrorKind, extra ...int) error {
	single := total == 1
	prefix := "edits[" + fmt.Sprint(editIndex) + "]"
	switch kind {
	case notFound:
		if single {
			return fmt.Errorf("Could not find the exact text in %s. The old text must match exactly including all whitespace and newlines.", path)
		}
		return fmt.Errorf("Could not find %s in %s. The oldText must match exactly including all whitespace and newlines.", prefix, path)
	case duplicate:
		occurrences := 0
		if len(extra) > 0 {
			occurrences = extra[0]
		}
		if single {
			return fmt.Errorf("Found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique.", occurrences, path)
		}
		return fmt.Errorf("Found %d occurrences of %s in %s. Each oldText must be unique. Please provide more context to make it unique.", occurrences, prefix, path)
	case emptyOldText:
		if single {
			return fmt.Errorf("oldText must not be empty in %s.", path)
		}
		return fmt.Errorf("%s.oldText must not be empty in %s.", prefix, path)
	default:
		if single {
			return fmt.Errorf("No changes made to %s. The replacement produced identical content.", path)
		}
		return fmt.Errorf("No changes made to %s. The replacements produced identical content.", path)
	}
}

func applyReplacements(content string, matched []matchedEdit) string {
	return applyReplacementList(content, matchedToRepls(matched), 0)
}

func applyReplacementList(content string, replacements []replacement, offset int) string {
	sorted := append([]replacement{}, replacements...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].matchIndex > sorted[b].matchIndex })
	result := content
	for _, r := range sorted {
		idx := r.matchIndex - offset
		result = result[:idx] + r.newText + result[idx+r.matchLength:]
	}
	return result
}

func applyReplacementsPreservingUnchangedLines(originalContent, baseContent string, matched []matchedEdit) string {
	originalLines := splitLinesWithEndings(originalContent)
	baseLines := lineSpans(baseContent)
	if len(originalLines) != len(baseLines) {
		return applyReplacements(baseContent, matched)
	}

	type group struct {
		startLine, endLine int
		replacements       []matchedEdit
	}
	sorted := append([]matchedEdit{}, matched...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].matchIndex < sorted[b].matchIndex })
	var groups []group
	for _, replacement := range sorted {
		startLine, endLine := replacementLineRange(baseLines, replacement)
		if n := len(groups); n > 0 && startLine < groups[n-1].endLine {
			groups[n-1].endLine = max(groups[n-1].endLine, endLine)
			groups[n-1].replacements = append(groups[n-1].replacements, replacement)
			continue
		}
		groups = append(groups, group{startLine: startLine, endLine: endLine, replacements: []matchedEdit{replacement}})
	}

	originalLineIndex := 0
	var result strings.Builder
	for _, g := range groups {
		result.WriteString(joinLines(originalLines[originalLineIndex:g.startLine]))
		groupStart := baseLines[g.startLine].start
		groupEnd := baseLines[g.endLine-1].end
		slice := baseContent[groupStart:groupEnd]
		result.WriteString(applyReplacementList(slice, matchedToRepls(g.replacements), groupStart))
		originalLineIndex = g.endLine
	}
	result.WriteString(joinLines(originalLines[originalLineIndex:]))
	return result.String()
}

type matchedEdit struct {
	editIndex, matchIndex, matchLength int
	newText                            string
}

type lineSpan struct{ start, end int }

func lineSpans(content string) []lineSpan {
	lines := splitLinesWithEndings(content)
	offset := 0
	spans := make([]lineSpan, len(lines))
	for i, line := range lines {
		spans[i] = lineSpan{start: offset, end: offset + len(line)}
		offset = spans[i].end
	}
	return spans
}

func splitLinesWithEndings(content string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, content[start:i+1])
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}

func joinLines(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line)
	}
	return b.String()
}

func replacementLineRange(lines []lineSpan, replacement matchedEdit) (int, int) {
	start := replacement.matchIndex
	end := replacement.matchIndex + replacement.matchLength
	startLine := -1
	for i, line := range lines {
		if start >= line.start && start < line.end {
			startLine = i
			break
		}
	}
	if startLine == -1 {
		return 0, 1
	}
	endLine := startLine
	for endLine < len(lines) && lines[endLine].end < end {
		endLine++
	}
	if endLine >= len(lines) {
		return startLine, len(lines)
	}
	return startLine, endLine + 1
}

func matchedToRepls(matched []matchedEdit) []replacement {
	out := make([]replacement, len(matched))
	for i, m := range matched {
		out[i] = replacement{matchIndex: m.matchIndex, matchLength: m.matchLength, newText: m.newText}
	}
	return out
}

type replacement struct {
	matchIndex, matchLength int
	newText                 string
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// EditTool edits a file with one or more exact-text replacements.
func EditTool() harness.ToolDef {
	return harness.ToolDef{
		Name:        "edit",
		Description: "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file.",
		Replay:      "unsafe",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			if path == "" {
				return "", fmt.Errorf("path required")
			}
			edits := prepareEdits(args["edits"], args)
			if len(edits) == 0 {
				return "", fmt.Errorf("edits must contain at least one replacement")
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("could not edit file: %s: %w", path, err)
			}
			bom, content := StripBom(string(raw))
			ending := DetectLineEnding(content)
			normalized := NormalizeToLF(content)
			base, next, err := ApplyEditsToNormalizedContent(normalized, edits, path)
			if err != nil {
				return "", err
			}
			final := bom + RestoreLineEndings(next, ending)
			if err := os.WriteFile(path, []byte(final), 0o644); err != nil {
				return "", err
			}
			diff := GenerateDiffString(base, next)
			_ = diff
			return fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), path), nil
		},
	}
}

func prepareEdits(rawEdits any, args map[string]any) []Edit {
	var edits []Edit
	if s, ok := rawEdits.(string); ok {
		var parsed any
		if jsonUnmarshal([]byte(s), &parsed) == nil {
			if arr, ok := parsed.([]any); ok {
				for _, item := range arr {
					if e := editFromAny(item); e != nil {
						edits = append(edits, *e)
					}
				}
			} else if e := editFromAny(parsed); e != nil {
				edits = append(edits, *e)
			}
		}
	} else if arr, ok := rawEdits.([]any); ok {
		for _, item := range arr {
			if e := editFromAny(item); e != nil {
				edits = append(edits, *e)
			}
		}
	} else if e := editFromAny(rawEdits); e != nil {
		edits = append(edits, *e)
	}
	oldText, oldOK := args["oldText"].(string)
	newText, newOK := args["newText"].(string)
	if oldOK && newOK {
		edits = append(edits, Edit{OldText: oldText, NewText: newText})
	}
	return edits
}

func editFromAny(v any) *Edit {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	oldText, oldOK := m["oldText"].(string)
	newText, newOK := m["newText"].(string)
	if !oldOK || !newOK {
		return nil
	}
	return &Edit{OldText: oldText, NewText: newText}
}

// GenerateDiffString returns a compact line-oriented diff for display.
func GenerateDiffString(oldContent, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	ops := diffLines(oldLines, newLines)
	var b strings.Builder
	for _, op := range ops {
		switch op.kind {
		case ' ':
			b.WriteString(" ")
			b.WriteString(op.text)
		case '-':
			b.WriteString("-")
			b.WriteString(op.text)
		case '+':
			b.WriteString("+")
			b.WriteString(op.text)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

type diffOp struct {
	kind byte
	text string
}

func diffLines(a, b []string) []diffOp {
	// Longest-common-subsequence diff over lines. Edit inputs are small, so
	// the quadratic table is fine and far easier to verify than a Myers walk.
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			ops = append(ops, diffOp{kind: ' ', text: a[i]})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			ops = append(ops, diffOp{kind: '-', text: a[i]})
			i++
		} else {
			ops = append(ops, diffOp{kind: '+', text: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{kind: '-', text: a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{kind: '+', text: b[j]})
	}
	return ops
}

func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
