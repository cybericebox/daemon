package exercise

import (
	"encoding/json"
	"regexp"
	"strings"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

var paragraphSplit = regexp.MustCompile(`\n{2,}`)

// archiveDescriptionKeyUnknown is the one place ErrArchiveDescriptionKeyUnknown is raised.
func archiveDescriptionKeyUnknown(key, task string) error {
	return exerciseModel.ErrArchiveDescriptionKeyUnknown.WithPublicContext("key", key).WithPublicContext("task", task).Err()
}

// plainTextToDocument turns a plain-text task description into the stored editor document: a blank
// line starts a paragraph, a newline is a line break, {{key}} is a variable naming a placeholder of
// the task and \{{ is a literal {{. Blank text gives no document.
func plainTextToDocument(text string, placeholders []exerciseModel.Placeholder, taskName string) (json.RawMessage, error) {
	text = strings.Trim(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	known := make(map[string]struct{}, len(placeholders))
	for _, placeholder := range placeholders {
		known[placeholder.Key] = struct{}{}
	}
	paragraphs := []any{}
	for _, block := range paragraphSplit.Split(text, -1) {
		if strings.TrimSpace(block) == "" {
			continue
		}
		children := []any{}
		for i, line := range strings.Split(block, "\n") {
			if i > 0 {
				children = append(children, map[string]any{"type": "linebreak", "version": 1})
			}
			inline, err := plainTextInline(line, known, taskName)
			if err != nil {
				return nil, err
			}
			children = append(children, inline...)
		}
		paragraphs = append(paragraphs, map[string]any{
			"type": "paragraph", "version": 1, "direction": nil, "format": "", "indent": 0,
			"textFormat": 0, "textStyle": "", "children": children,
		})
	}
	return json.Marshal(map[string]any{"root": map[string]any{
		"type": "root", "version": 1, "direction": nil, "format": "", "indent": 0, "children": paragraphs,
	}})
}

// plainTextInline splits one line into text and variable nodes.
func plainTextInline(line string, known map[string]struct{}, taskName string) ([]any, error) {
	var nodes []any
	var buf strings.Builder
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		nodes = append(nodes, map[string]any{
			"type": "text", "version": 1, "text": buf.String(), "detail": 0, "format": 0, "mode": "normal", "style": "",
		})
		buf.Reset()
	}
	for len(line) > 0 {
		switch {
		case strings.HasPrefix(line, `\{{`):
			buf.WriteString("{{")
			line = line[3:]
		case strings.HasPrefix(line, "{{"):
			end := strings.Index(line, "}}")
			if end < 0 {
				return nil, archiveInvalid("description has an unclosed {{", "task", taskName)
			}
			key := strings.TrimSpace(line[2:end])
			if _, ok := known[key]; !ok {
				return nil, archiveDescriptionKeyUnknown(key, taskName)
			}
			flush()
			nodes = append(nodes, map[string]any{"type": "variable", "version": 1, "varName": key, "formats": []any{}})
			line = line[end+2:]
		default:
			buf.WriteByte(line[0])
			line = line[1:]
		}
	}
	flush()
	return nodes, nil
}
