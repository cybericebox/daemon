package tools

import (
	"github.com/gofrs/uuid"
)

func NewUUIDv7() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

// FromStringOrNilSlice parses ids, mapping unparseable ones to uuid.Nil.
func FromStringOrNilSlice(strIDs []string) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(strIDs))
	for _, id := range strIDs {
		ids = append(ids, uuid.FromStringOrNil(id))
	}
	return ids
}

// ToStringSlice renders ids as canonical strings.
func ToStringSlice(uuids []uuid.UUID) []string {
	out := make([]string, 0, len(uuids))
	for _, id := range uuids {
		out = append(out, id.String())
	}
	return out
}
