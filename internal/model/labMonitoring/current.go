package labMonitoring

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
)

// Current is the merged CURRENT state of one team lab group. Observations are
// an immutable history of deltas; this is what "now" looks like after folding
// every delta since the last snapshot.
type Current struct {
	EventID      uuid.UUID
	EventName    string
	EventTeamID  uuid.UUID
	TeamName     string
	Moderators   bool
	LabGroupName string
	AgentID      string
	Sequence     int64
	ObservedAt   time.Time
	UpdatedAt    time.Time
	Payload      json.RawMessage
}

// RecentWindow is how long an inactive event's state stays listable when the
// caller asks for recent events.
const RecentWindow = 7 * 24 * time.Hour

// resource collections of a monitoring payload, in output order.
var collections = []string{"groups", "labs", "clients", "policies"}

// deletedKind maps the agent's MonitoringDeletedKey.kind to a collection.
var deletedKind = map[string]string{
	"lab_group":     "groups",
	"lab":           "labs",
	"client":        "clients",
	"access_policy": "policies",
}

// Sanitize drops every field that must never reach an administrator view:
// lab specs and device environment (task flags), client public keys and VPN
// configs. It works on a decoded protojson payload and returns the same map.
func Sanitize(payload map[string]any) map[string]any {
	for _, item := range objects(payload["labs"]) {
		delete(item, "specJson")
		delete(item, "env")
	}
	for _, item := range objects(payload["clients"]) {
		delete(item, "publicKey")
		if status, ok := item["status"].(map[string]any); ok {
			delete(status, "config")
		}
	}
	return payload
}

// SanitizePayload is Sanitize over raw JSON; undecodable input yields an empty
// object rather than leaking unknown content.
func SanitizePayload(raw json.RawMessage) json.RawMessage {
	payload := map[string]any{}
	if err := json.Unmarshal(raw, &payload); err != nil || payload == nil {
		return json.RawMessage(`{}`)
	}
	encoded, err := json.Marshal(Sanitize(payload))
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

// Merge folds one per-group monitoring update into the current state. A
// snapshot replaces the state; a delta upserts the reported resources by their
// identity and drops the deleted ones. The result is sanitized.
func Merge(current json.RawMessage, update json.RawMessage, snapshot bool) (json.RawMessage, error) {
	next := map[string]any{}
	if !snapshot && len(current) > 0 {
		if err := json.Unmarshal(current, &next); err != nil {
			return nil, fmt.Errorf("decode current monitoring state: %w", err)
		}
		if next == nil {
			next = map[string]any{}
		}
	}
	delta := map[string]any{}
	if err := json.Unmarshal(update, &delta); err != nil {
		return nil, fmt.Errorf("decode monitoring update: %w", err)
	}
	Sanitize(delta)

	byCollection := map[string]map[string]map[string]any{}
	order := map[string][]string{}
	for _, name := range collections {
		byCollection[name] = map[string]map[string]any{}
		for _, item := range append(objects(next[name]), objects(delta[name])...) {
			key := identity(name, item)
			if _, seen := byCollection[name][key]; !seen {
				order[name] = append(order[name], key)
			}
			byCollection[name][key] = item
		}
	}
	for _, deleted := range objects(delta["deletedKeys"]) {
		collection, ok := deletedKind[str(deleted["kind"])]
		if !ok {
			continue
		}
		delete(byCollection[collection], deletedIdentity(collection, deleted))
	}

	out := map[string]any{}
	for _, name := range collections {
		items := make([]any, 0, len(byCollection[name]))
		for _, key := range order[name] {
			if item, ok := byCollection[name][key]; ok {
				items = append(items, item)
			}
		}
		if len(items) > 0 {
			out[name] = items
		}
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode current monitoring state: %w", err)
	}
	return encoded, nil
}

func identity(collection string, item map[string]any) string {
	switch collection {
	case "groups":
		return str(item["name"])
	case "policies":
		return str(item["labGroupName"]) + "/" + str(item["namespace"])
	default:
		return str(item["namespace"]) + "/" + str(item["name"])
	}
}

func deletedIdentity(collection string, deleted map[string]any) string {
	switch collection {
	case "groups":
		return str(deleted["name"])
	case "policies":
		return str(deleted["labGroupName"]) + "/" + str(deleted["namespace"])
	default:
		return str(deleted["namespace"]) + "/" + str(deleted["name"])
	}
}

func objects(value any) []map[string]any {
	list, _ := value.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if object, ok := item.(map[string]any); ok {
			out = append(out, object)
		}
	}
	return out
}

func str(value any) string {
	s, _ := value.(string)
	return s
}
