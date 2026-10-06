package inAppModel

import (
	"encoding/json"
	"errors"
	"strings"
)

// ValidLink reports whether link may be shown as an in-app link or button target: empty, an
// app-relative path ("/x", not "//host" or "/\host"), or an https URL. A template (before it is
// rendered) may also start with a variable ("{{event_url}}"), whose value is checked again when the
// notification is sent: a template written by an event manager must never become javascript:, data:
// or an http link a participant clicks.
func ValidLink(link string, template bool) bool {
	link = strings.TrimSpace(link)
	switch {
	case link == "":
		return true
	case template && strings.HasPrefix(link, "{{"):
		return true
	case strings.HasPrefix(link, "/"):
		return !strings.HasPrefix(link, "//") && !strings.HasPrefix(link, "/\\")
	}
	return strings.HasPrefix(strings.ToLower(link), "https://") && !strings.ContainsAny(link, " \t\r\n\x00")
}

var errActionsShape = errors.New("actions must be a list of {label, href}")

// actionHrefs is every action target of an Actions document (a JSON list of objects with an "href").
func actionHrefs(actions json.RawMessage) ([]map[string]any, error) {
	if len(strings.TrimSpace(string(actions))) == 0 || strings.TrimSpace(string(actions)) == "null" {
		return nil, nil
	}
	var list []map[string]any
	if err := json.Unmarshal(actions, &list); err != nil {
		return nil, errActionsShape
	}
	return list, nil
}

// ValidActions reports whether every action of the document points to a valid link.
func ValidActions(actions json.RawMessage, template bool) bool {
	list, err := actionHrefs(actions)
	if err != nil {
		return false
	}
	for _, action := range list {
		if href, ok := action["href"].(string); ok && !ValidLink(href, template) {
			return false
		}
	}
	return true
}

// SafeActions drops the actions whose target is not a valid link (what is left of a stored template
// at send time, after its variables are known). An unreadable document yields none.
func SafeActions(actions json.RawMessage) json.RawMessage {
	list, err := actionHrefs(actions)
	if err != nil {
		return nil
	}
	if list == nil {
		return actions
	}
	kept := make([]map[string]any, 0, len(list))
	for _, action := range list {
		if href, ok := action["href"].(string); ok && !ValidLink(href, false) {
			continue
		}
		kept = append(kept, action)
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return nil
	}
	return out
}
