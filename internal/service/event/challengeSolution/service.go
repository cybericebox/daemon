package challengeSolutionService

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"strings"
	"time"
)

type (
	ChallengeSolutionService struct {
		repository IRepository
	}

	IRepository interface {
		GetChallengeFlag(ctx context.Context, arg postgres.GetChallengeFlagParams) (string, error)
		CreateEventChallengeSolutionAttempt(ctx context.Context, arg postgres.CreateEventChallengeSolutionAttemptParams) error

		GetEventChallengeSolutionAttemptsPaged(ctx context.Context, arg postgres.GetEventChallengeSolutionAttemptsPagedParams) ([]postgres.GetEventChallengeSolutionAttemptsPagedRow, error)
		UpdateEventChallengeSolutionAttempt(ctx context.Context, arg postgres.UpdateEventChallengeSolutionAttemptParams) (int64, error)
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *ChallengeSolutionService {
	return &ChallengeSolutionService{
		repository: deps.Repository,
	}
}

func (s *ChallengeSolutionService) GetEventChallengeFlag(ctx context.Context, challengeID, teamID uuid.UUID, flags []string) (string, error) {
	flag, err := s.repository.GetChallengeFlag(ctx, postgres.GetChallengeFlagParams{
		ChallengeID: challengeID,
		TeamID:      teamID,
	})
	if err != nil {
		if !tools.IsObjectNotFoundError(err) {
			return "", eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to get challenge flag from repository").Err()
		}
	}

	if err == nil && flag != "" {
		return flag, nil
	}

	flag, err = tools.GetSolutionForTask(flags...)
	if err != nil {
		return "", eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to generate flag for challenge").Err()
	}

	return flag, nil
}

func (s *ChallengeSolutionService) SolveEventChallenge(ctx context.Context, teamID, userID, challengeID uuid.UUID, solutionAttempt string, timestamp time.Time) (bool, error) {
	// get challenge flag
	flag, err := s.repository.GetChallengeFlag(ctx, postgres.GetChallengeFlagParams{
		ChallengeID: challengeID,
		TeamID:      teamID,
	})
	if err != nil {
		return false, eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to get challenge flag from repository").Err()
	}

	// check if flag is correct
	// Check if the solution is correct
	isCorrect := strings.Compare(flag, solutionAttempt) == 0

	// save attempt
	if err = s.repository.CreateEventChallengeSolutionAttempt(ctx, postgres.CreateEventChallengeSolutionAttemptParams{
		ID:            uuid.Must(uuid.NewV7()),
		ChallengeID:   challengeID,
		TeamID:        teamID,
		ParticipantID: userID,
		Answer:        solutionAttempt,
		Flag:          flag,
		IsCorrect:     isCorrect,
		Timestamp:     timestamp,
	}); err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return false, errCreator.Err()
		}
		errCreator, has = tools.UniqueViolationError(err, eventModel.ErrEventTeamChallengeAlreadySolved)
		if has {
			return false, errCreator.Err()
		}
		return false, eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to create event challenge solution attempt").Err()
	}

	return isCorrect, nil
}

func (s *ChallengeSolutionService) GetEventChallengeSolutionAttempts(ctx context.Context, eventID uuid.UUID, page int) ([]*eventModel.TeamChallengeSolutionAttempt, error) {
	solutions, err := s.repository.GetEventChallengeSolutionAttemptsPaged(ctx, postgres.GetEventChallengeSolutionAttemptsPagedParams{
		EventID: eventID,
		Limit:   config.DefaultOnePageLimit,
		Offset:  int32(page * config.DefaultOnePageLimit),
	})
	if err != nil {
		return nil, eventModel.ErrEventScore.WithError(err).WithMessage("Failed to get all challenges solutions in event").Err()
	}

	result := make([]*eventModel.TeamChallengeSolutionAttempt, 0, len(solutions))
	for _, solution := range solutions {
		sol := eventModel.TeamChallengeSolutionAttempt(solution)
		result = append(result, &sol)
	}

	return result, nil
}

func (s *ChallengeSolutionService) UpdateEventChallengeSolutionAttempt(ctx context.Context, solutionAttempt eventModel.TeamChallengeSolutionAttempt) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
	}

	affected, err := s.repository.UpdateEventChallengeSolutionAttempt(ctx, postgres.UpdateEventChallengeSolutionAttemptParams{
		ID:        solutionAttempt.ID,
		IsCorrect: solutionAttempt.IsCorrect,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		return eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to update event challenge solution attempt").Err()
	}
	if affected == 0 {
		return eventModel.ErrEventChallenge.WithMessage("No rows affected").Err()
	}
	return nil
}
