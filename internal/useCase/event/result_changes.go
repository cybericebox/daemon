package event

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventResultRepo"
	"github.com/cybericebox/daemon/internal/model"
)

const resultChangeRetention = 15 * time.Minute

type teamChallengeResultChange struct {
	TeamID           uuid.UUID  `json:"TeamID"`
	EventChallengeID uuid.UUID  `json:"EventChallengeID"`
	SolvedAt         *time.Time `json:"SolvedAt"`
}

func recordResultChange(ctx context.Context, repo eventResultRepo.Queries, eventID, teamID, challengeID uuid.UUID, before, after *time.Time) error {
	if (before == nil && after == nil) || (before != nil && after != nil && before.Equal(*after)) {
		return nil
	}
	kind := eventResultRepo.ChangeTeamChallengeUnsolved
	if after != nil {
		kind = eventResultRepo.ChangeTeamChallengeSolved
	}
	payload, err := json.Marshal(teamChallengeResultChange{TeamID: teamID, EventChallengeID: challengeID, SolvedAt: after})
	if err != nil {
		return err
	}
	_, err = eventResultRepo.New(repo).Advance(ctx, eventID, eventResultRepo.Change{Kind: kind, Payload: payload, CreatedAt: time.Now().UTC()})
	return err
}

// recordScoreboardRecalculation tells live clients to reload their snapshot.
// A popularity solve can move several historical scores, so a single team
// delta would be incomplete by construction.
func recordScoreboardRecalculation(ctx context.Context, repo eventResultRepo.Queries, eventID uuid.UUID) error {
	_, err := eventResultRepo.New(repo).Advance(ctx, eventID, eventResultRepo.Change{Kind: eventResultRepo.ChangeScoreboardRecalculated, Payload: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()})
	return err
}

// CleanupExpiredResultChanges keeps only the short active-stream window. A
// client that loses SSE always loads a fresh snapshot instead of needing an
// older replay chain, so revisions themselves remain while their old deltas go.
func (u *EventUseCase) CleanupExpiredResultChanges(ctx context.Context) error {
	if _, err := u.results.DeleteChangesBefore(ctx, time.Now().UTC().Add(-resultChangeRetention)); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to clean expired result changes").Err()
	}
	return nil
}
