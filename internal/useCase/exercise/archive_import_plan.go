package exercise

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/gofrs/uuid"
)

// Stable order, retaining nil vs explicitly empty choices. Passwords never
// enter a persisted plan or its digest.
func canonicalImportPlan(in ImportOptions, owner *uuid.UUID) ([]byte, string, error) {
	in.Targets = slices.Clone(in.Targets)
	in.ConfirmedUpdates = slices.Clone(in.ConfirmedUpdates)
	slices.SortFunc(in.Targets, func(a, b ImportTarget) int { return strings.Compare(a.ItemKey, b.ItemKey) })
	slices.SortFunc(in.ConfirmedUpdates, func(a, b ConfirmedImportUpdate) int { return strings.Compare(a.ItemKey, b.ItemKey) })
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, "", err
	}
	hashInput, err := json.Marshal(struct {
		Options      ImportOptions
		OwnerEventID *uuid.UUID
	}{in, owner})
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(hashInput)
	return encoded, hex.EncodeToString(hash[:]), nil
}

func attemptGeneration(g int64) int64 {
	if g == 0 {
		return 1
	}
	return g
}
