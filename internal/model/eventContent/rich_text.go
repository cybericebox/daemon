package eventContentModel

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

const maxRichTextBytes = 256 * 1024
const maxRichTextDepth = 32

// validateRichText checks the small subset of Lexical JSON that the event
// editor can produce. A variable is content, but an empty element is not.
func validateRichText(raw json.RawMessage, declared map[string]struct{}) (map[string]struct{}, error) {
	if len(raw) == 0 || len(raw) > maxRichTextBytes {
		return nil, errors.New("rich text is missing or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil || document == nil {
		return nil, errors.New("rich text is invalid")
	}
	if decoder.More() || len(document) != 1 {
		return nil, errors.New("rich text root is invalid")
	}
	root, ok := document["root"].(map[string]any)
	if !ok || root["type"] != "root" {
		return nil, errors.New("rich text root is invalid")
	}
	names := map[string]struct{}{}
	hasContent, err := validateRichTextNode(root, 0, declared, names)
	if err != nil {
		return nil, err
	}
	if !hasContent {
		return nil, errors.New("rich text is required")
	}
	return names, nil
}

func validateRichTextNode(node map[string]any, depth int, declared, names map[string]struct{}) (bool, error) {
	if depth > maxRichTextDepth {
		return false, errors.New("rich text is too deep")
	}
	typeName, ok := node["type"].(string)
	if !ok {
		return false, errors.New("rich text node type is invalid")
	}
	if version, ok := node["version"].(json.Number); ok && version.String() != "1" {
		return false, errors.New("rich text node version is unsupported")
	}
	switch typeName {
	case "root", "paragraph", "heading", "quote", "code", "list", "listitem", "link":
		if typeName == "heading" {
			tag, ok := node["tag"].(string)
			if !ok || len(tag) != 2 || tag[0] != 'h' || tag[1] < '1' || tag[1] > '6' {
				return false, errors.New("rich text heading is unsupported")
			}
		}
		if typeName == "list" {
			listType, ok := node["listType"].(string)
			if !ok || listType != "bullet" && listType != "number" {
				return false, errors.New("rich text list is unsupported")
			}
		}
		if typeName == "link" {
			href, ok := node["url"].(string)
			if !ok || !validContentHref(href) {
				return false, errors.New("rich text link is unsafe")
			}
		}
		children, ok := node["children"].([]any)
		if !ok {
			return false, errors.New("rich text children are invalid")
		}
		hasContent := false
		for _, child := range children {
			object, ok := child.(map[string]any)
			if !ok {
				return false, errors.New("rich text child is invalid")
			}
			found, err := validateRichTextNode(object, depth+1, declared, names)
			if err != nil {
				return false, err
			}
			hasContent = hasContent || found
		}
		return hasContent, nil
	case "text":
		value, ok := node["text"].(string)
		if !ok {
			return false, errors.New("rich text text node is invalid")
		}
		if format, exists := node["format"]; exists {
			flags, ok := format.(json.Number)
			if !ok {
				return false, errors.New("rich text format is invalid")
			}
			parsed, err := flags.Int64()
			if err != nil || parsed < 0 || parsed&^31 != 0 {
				return false, errors.New("rich text format is unsupported")
			}
		}
		return strings.TrimSpace(value) != "", nil
	case "linebreak":
		return false, nil
	case "variable":
		name, ok := node["varName"].(string)
		if !ok || EventContentVariables[name] == "" {
			return false, errors.New("rich text variable is unsupported")
		}
		if _, ok := declared[name]; !ok {
			return false, errors.New("rich text variable must be declared by its block")
		}
		if rawFormats, exists := node["formats"]; exists {
			formats, ok := rawFormats.([]any)
			if !ok {
				return false, errors.New("rich text variable formats are invalid")
			}
			for _, format := range formats {
				switch format {
				case "bold", "italic", "underline", "strikethrough", "code":
				default:
					return false, errors.New("rich text variable format is unsupported")
				}
			}
		}
		names[name] = struct{}{}
		return true, nil
	default:
		return false, errors.New("rich text node type is unsupported")
	}
}
