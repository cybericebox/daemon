package testutil

import (
	"encoding/json"
	"regexp"
)

var variable = regexp.MustCompile(`\{\{([a-z][a-zA-Z0-9.]*)\}\}`)

// RichText constructs a wire-format Lexical fixture from literal test copy.
func RichText(value string) json.RawMessage {
	children := make([]map[string]any, 0)
	previous := 0
	for _, match := range variable.FindAllStringSubmatchIndex(value, -1) {
		if match[0] > previous {
			children = append(children, map[string]any{"type": "text", "version": 1, "text": value[previous:match[0]], "format": 0})
		}
		children = append(children, map[string]any{"type": "variable", "version": 1, "varName": value[match[2]:match[3]]})
		previous = match[1]
	}
	if previous < len(value) {
		children = append(children, map[string]any{"type": "text", "version": 1, "text": value[previous:], "format": 0})
	}
	encoded, err := json.Marshal(map[string]any{"root": map[string]any{
		"type": "root", "version": 1, "children": []any{map[string]any{"type": "paragraph", "version": 1, "children": children}},
	}})
	if err != nil {
		panic(err)
	}
	return encoded
}

func Document(id, value string) []byte {
	return document(id, value, "", "")
}

func DocumentWithBinding(id, value, name, format string) []byte {
	return document(id, value, name, format)
}

func document(id, value, name, format string) []byte {
	block := map[string]any{"id": id, "type": "text", "richText": RichText(value)}
	if name != "" {
		block["variables"] = []any{map[string]any{"name": name, "format": format}}
	}
	encoded, err := json.Marshal(map[string]any{"blocks": []any{block}})
	if err != nil {
		panic(err)
	}
	return encoded
}
