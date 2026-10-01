package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/challengeAttemptRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

// fixedAward calculates the immutable award for static and time profiles.
// Popularity and ladder return nil: their score is a read-time projection over
// the currently visible teams, not a historical award.
func fixedAward(ctx context.Context, attempts *challengeAttemptRepo.Repository, teamChallengeID uuid.UUID, solvedAt time.Time) (*int32, error) {
	context, err := attempts.GetScoringContext(ctx, teamChallengeID)
	if err != nil {
		return nil, err
	}
	profile := eventModel.ResolveScoringProfile(context.EventProfile, context.LocalProfile, context.ForceEventScoring)
	// Popularity and ladder points depend on which teams are visible, so they
	// are computed when results are read and never stored.
	if profile.Mode == eventModel.ScoringPopularityCurve || profile.Mode == eventModel.ScoringFirstSolvesLadder {
		return nil, nil
	}
	duration := time.Duration(0)
	if context.FinishAt != nil {
		duration = context.FinishAt.Sub(context.StartAt)
	}
	points := eventModel.AwardPoints(profile, context.StaticPoints, context.Population, 1, 0, solvedAt.Sub(context.StartAt), duration)
	return &points, nil
}
