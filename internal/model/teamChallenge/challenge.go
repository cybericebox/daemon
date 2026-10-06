// Package teamChallengeModel owns a challenge materialized for one team.
package teamChallengeModel

import (
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
)

type Readiness int16

const (
	ReadinessPreparing Readiness = iota
	ReadinessReady
	ReadinessPublished
	ReadinessFailed
)

type TeamChallenge struct {
	ID               uuid.UUID
	EventID          uuid.UUID
	EventTeamID      uuid.UUID
	EventChallengeID uuid.UUID
	VariantIndex     int32
	Snapshot         json.RawMessage
	ExpectedFlag     string
	Readiness        Readiness
	SolvedAt         *time.Time
	CreatedAt        time.Time
	// Hints are the team variant's hint texts. They never enter Snapshot:
	// a text is shown only after the team unlocked that hint.
	Hints []Hint
}

// Hint is the team's text of one unlockable hint.
type Hint struct {
	ID   uuid.UUID `json:"id"`
	Text string    `json:"text"`
}

// HintText returns the team's text of one hint.
func HintText(hints []Hint, id uuid.UUID) (string, bool) {
	for _, hint := range hints {
		if hint.ID == id {
			return hint.Text, true
		}
	}
	return "", false
}

func New(eventID, teamID, challengeID uuid.UUID, variantIndex int32, snapshot json.RawMessage, expectedFlag string, now time.Time) (TeamChallenge, error) {
	if eventID == uuid.Nil || teamID == uuid.Nil || challengeID == uuid.Nil || variantIndex < 0 || len(snapshot) == 0 || expectedFlag == "" {
		return TeamChallenge{}, ErrTeamChallengeInvalid.Err()
	}
	return TeamChallenge{ID: uuid.Must(uuid.NewV7()), EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID, VariantIndex: variantIndex, Snapshot: snapshot, ExpectedFlag: expectedFlag, Readiness: ReadinessPreparing, CreatedAt: now}, nil
}

func (c *TeamChallenge) MarkReady() error {
	if c.Readiness != ReadinessPreparing {
		return ErrTeamChallengeTransition.Err()
	}
	c.Readiness = ReadinessReady
	return nil
}

func (c *TeamChallenge) Publish() error {
	if c.Readiness != ReadinessReady {
		return ErrTeamChallengeTransition.Err()
	}
	c.Readiness = ReadinessPublished
	return nil
}
