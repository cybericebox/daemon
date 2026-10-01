package notificationTypes

import (
	"encoding/json"
	"fmt"
	"regexp"
)

var templateVariablePattern = regexp.MustCompile(`\{\{\s*\.?(\w+)\s*\}\}`)

// ValidateTemplateVariables verifies that a template uses only variables which
// its code-owned notification type guarantees on the selected channel.
func ValidateTemplateVariables(t NotificationType, channel NotificationChannel, values ...string) error {
	if !Supports(t, channel) {
		return fmt.Errorf("notification type %q does not support channel %q", t, channel)
	}
	allowed := make(map[string]struct{}, len(Descriptors(t)))
	for _, descriptor := range Descriptors(t) {
		allowed[descriptor.Name] = struct{}{}
	}
	for _, value := range values {
		for _, match := range templateVariablePattern.FindAllStringSubmatch(value, -1) {
			if _, ok := allowed[match[1]]; !ok {
				return fmt.Errorf("notification type %q does not provide variable %q", t, match[1])
			}
		}
	}
	return nil
}

// ValidateEmailBodyVariables checks variable nodes and dynamic links in the
// Lexical JSON body. Literal rich text is deliberately not scanned: {{text}}
// in a paragraph is content, while a variable node is an explicit reference.
func ValidateEmailBodyVariables(t NotificationType, body json.RawMessage) error {
	var blocks []json.RawMessage
	if err := json.Unmarshal(body, &blocks); err != nil {
		return fmt.Errorf("decode email blocks: %w", err)
	}
	var values []string
	for _, raw := range blocks {
		collectEmailBlockVariables(raw, &values)
	}
	return ValidateTemplateVariables(t, NotificationChannelEmail, values...)
}

func collectEmailBlockVariables(raw json.RawMessage, values *[]string) {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	collectEmailNodeVariables(value, values)
}

func collectEmailNodeVariables(value any, values *[]string) {
	switch node := value.(type) {
	case map[string]any:
		typ, _ := node["type"].(string)
		if typ == "variable" {
			if name, ok := node["varName"].(string); ok {
				*values = append(*values, "{{"+name+"}}")
			}
		}
		if typ == "facts" {
			// {{variables}} in the label/value rows of a facts card.
			if items, ok := node["items"].([]any); ok {
				for _, item := range items {
					if row, ok := item.(map[string]any); ok {
						for _, key := range []string{"label", "value"} {
							if v, ok := row[key].(string); ok {
								*values = append(*values, v)
							}
						}
					}
				}
			}
		}
		if typ == "button" || typ == "link" {
			if url, ok := node["url"].(string); ok {
				*values = append(*values, url)
			}
		}
		for _, child := range node {
			collectEmailNodeVariables(child, values)
		}
	case []any:
		for _, child := range node {
			collectEmailNodeVariables(child, values)
		}
	}
}
