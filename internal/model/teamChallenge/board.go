package teamChallengeModel

import (
	"encoding/json"

	"github.com/gofrs/uuid"
)

// Attachment is a file reference of a pinned task snapshot.
type Attachment struct {
	FileID uuid.UUID `json:"file_id"`
	Name   string    `json:"name"`
}

// PrerequisitesLock reports whether a challenge stays locked for a team: it
// is locked while any of its prerequisites is unsolved by that team.
func PrerequisitesLock(solved []bool) bool {
	for _, ok := range solved {
		if !ok {
			return true
		}
	}
	return false
}

// LockedSnapshot reduces a pinned snapshot to what a locked challenge may
// show: its name and difficulty. Description and attachments stay hidden.
func LockedSnapshot(snapshot json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(snapshot, &fields); err != nil {
		return nil, err
	}
	locked := make(map[string]json.RawMessage, 2)
	for _, key := range []string{"name", "difficulty"} {
		if value, ok := fields[key]; ok {
			locked[key] = value
		}
	}
	return json.Marshal(locked)
}

// SnapshotAttachments reads the attachment references of a pinned snapshot.
func SnapshotAttachments(snapshot json.RawMessage) ([]Attachment, error) {
	var value struct {
		Attachments []Attachment `json:"attachments"`
	}
	if err := json.Unmarshal(snapshot, &value); err != nil {
		return nil, err
	}
	if value.Attachments == nil {
		return []Attachment{}, nil
	}
	return value.Attachments, nil
}
