package event

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"

	"github.com/gofrs/uuid"
)

// frozenResultsVersion is an opaque version of exactly the visible frozen
// projection. Global revisions and timestamps would disclose hidden activity.
// A changed version always requires a snapshot, never an incremental replay.
func frozenResultsVersion(view ResultsSnapshotView) int64 {
	type row struct {
		Rank       int32
		TeamID     uuid.UUID
		TeamName   string
		NameHidden bool
		Points     int64
		Solved     int64
	}
	rows := make([]row, 0, len(view.Scoreboard))
	for _, entry := range view.Scoreboard {
		rows = append(rows, row{entry.Rank, entry.TeamID, entry.TeamName, entry.NameHidden, entry.Points, entry.Solved})
	}
	// Explicit allowlist: internal last-solve times/name flags and the global
	// GeneratedAt value never participate in this public version.
	data, _ := json.Marshal(struct {
		Scoreboard []row
		Timeline   []EventScoreTimelineEntryView
		TotalTeams int
		Tasks      *int64
		Freeze     ResultsFreezeView
		Display    ResultsDisplayView
	}{rows, view.Timeline, view.TotalTeams, view.Tasks, view.Freeze, view.Display})
	digest := sha256.Sum256(data)
	// JavaScript numbers exactly represent integers of at most 53 bits.
	version := int64(binary.BigEndian.Uint64(digest[:8]) >> 11)
	if version == 0 {
		return 1
	}
	return version
}
