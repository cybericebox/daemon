package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"strings"
	"time"
)

type (
	IChallengeSolutionRepository interface {
		GetChallengeFlag(ctx context.Context, arg postgres.GetChallengeFlagParams) (string, error)
		CreateEventChallengeSolutionAttempt(ctx context.Context, arg postgres.CreateEventChallengeSolutionAttemptParams) error
	}
)

func (s *EventService) GetChallengeFlag(ctx context.Context, challengeID, teamID uuid.UUID, flags []string) (string, error) {
	flag, err := s.repository.GetChallengeFlag(ctx, postgres.GetChallengeFlagParams{
		ChallengeID: challengeID,
		TeamID:      teamID,
	})
	if err != nil {
		if !tools.IsObjectNotFoundError(err) {
			return "", model.ErrEventChallenge.WithError(err).WithMessage("Failed to get challenge flag from repository").Cause()
		}
	}

	if err == nil && flag != "" {
		return flag, nil
	}

	flag, err = tools.GetSolutionForTask(flags...)
	if err != nil {
		return "", model.ErrEventChallenge.WithError(err).WithMessage("Failed to generate flag for challenge").Cause()
	}

	return flag, nil
}

func (s *EventService) SolveChallenge(ctx context.Context, eventID, teamID, userID, challengeID uuid.UUID, solutionAttempt string) (bool, error) {
	// get challenge flag
	flag, err := s.repository.GetChallengeFlag(ctx, postgres.GetChallengeFlagParams{
		ChallengeID: challengeID,
		TeamID:      teamID,
	})
	if err != nil {
		return false, model.ErrEventChallenge.WithError(err).WithMessage("Failed to get challenge flag from repository").Cause()
	}

	// check if flag is correct
	// Check if the solution is correct
	isCorrect := strings.Compare(flag, solutionAttempt) == 0

	// save attempt
	if err = s.repository.CreateEventChallengeSolutionAttempt(ctx, postgres.CreateEventChallengeSolutionAttemptParams{
		ID:            uuid.Must(uuid.NewV7()),
		EventID:       eventID,
		ChallengeID:   challengeID,
		TeamID:        teamID,
		ParticipantID: userID,
		Answer:        solutionAttempt,
		Flag:          flag,
		IsCorrect:     isCorrect,
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return false, errCreator.Cause()
		}
		return false, model.ErrEventChallenge.WithError(err).WithMessage("Failed to create event challenge solution attempt").Cause()
	}

	return isCorrect, nil
}
