package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"slices"
	"time"
)

type (
	IChallengeService interface {
		GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]*model.Challenge, error)
		GetEventChallengeByID(ctx context.Context, eventID uuid.UUID, challengeID uuid.UUID) (*model.Challenge, error)
		GetEventChallengeSolvedBy(ctx context.Context, eventID, challengeID uuid.UUID) (*model.ChallengeSoledBy, error)

		AddExercisesToEvent(ctx context.Context, eventID, categoryID uuid.UUID, exerciseIDs []uuid.UUID) error
		DeleteEventChallenges(ctx context.Context, eventID uuid.UUID, exerciseID uuid.UUID) error
		UpdateEventChallengesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error

		DeleteEventTeamsChallenges(ctx context.Context, eventID, exerciseID uuid.UUID) error

		SolveChallenge(ctx context.Context, eventID, teamID, challengeID uuid.UUID, solutionAttempt string) (bool, error)
	}
)

func (u *EventUseCase) GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]*model.Challenge, error) {
	challenges, err := u.service.GetEventChallenges(ctx, eventID)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get event challenges")
	}
	return challenges, nil
}

func (u *EventUseCase) GetEventChallengesInfo(ctx context.Context, eventID uuid.UUID) ([]*model.CategoryInfo, error) {
	// check if user has team in event
	team, err := u.GetSelfTeam(ctx, eventID)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get self team")
	}

	challenges, err := u.GetEventChallenges(ctx, eventID)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get event challenges")
	}

	categories, err := u.GetEventCategories(ctx, eventID)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get event categories")
	}

	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get event")
	}

	result := make([]*model.CategoryInfo, 0, len(categories))
	for _, category := range categories {
		challengesInCategory := make([]*model.ChallengeInfo, 0, len(challenges))
		for _, challenge := range challenges {
			if challenge.CategoryID == category.ID {
				// count challenge points
				points := challenge.Points

				solvedBy, err := u.service.GetEventChallengeSolvedBy(ctx, eventID, challenge.ID)
				if err != nil {
					return nil, appError.NewError().WithError(err).WithMessage("failed to get event challenge solved by")
				}

				if event.DynamicScoring {
					count := len(solvedBy.Teams)

					// calculate points
					points = tools.CalculateScore(event.DynamicMinScore, event.DynamicMaxScore, event.DynamicSolveThreshold, float64(count))
				}

				// check if challenge is solved by team
				solved := slices.IndexFunc(solvedBy.Teams, func(t *model.TeamSolvedChallenge) bool {
					return t.ID == team.ID
				}) != -1 // -1 if not solved

				challengesInCategory = append(challengesInCategory, &model.ChallengeInfo{
					ID:          challenge.ID,
					Name:        challenge.Name,
					Description: challenge.Description,
					Points:      points,
					Solved:      solved,
				})

			}
		}
		result = append(result, &model.CategoryInfo{
			ID:         category.ID,
			Name:       category.Name,
			Challenges: challengesInCategory,
		})
	}

	return result, nil
}

func (u *EventUseCase) AddExercisesToEvent(ctx context.Context, eventID, categoryID uuid.UUID, exerciseIDs []uuid.UUID) error {
	if err := u.service.AddExercisesToEvent(ctx, eventID, categoryID, exerciseIDs); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to add exercises to event")
	}

	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to get event")
	}

	u.CreateEventTeamsChallengesTask(ctx, *event)

	return nil
}

func (u *EventUseCase) DeleteEventChallenge(ctx context.Context, eventID uuid.UUID, challengeID uuid.UUID) error {
	challenge, err := u.service.GetEventChallengeByID(ctx, eventID, challengeID)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to get event challenge by id")
	}

	if err = u.service.DeleteEventChallenges(ctx, eventID, challenge.ExerciseID); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to delete event challenges")
	}

	if err = u.service.DeleteEventTeamsChallenges(ctx, eventID, challenge.ExerciseID); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to delete event teams challenges")
	}

	return nil
}

func (u *EventUseCase) UpdateEventChallengesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error {
	if err := u.service.UpdateEventChallengesOrder(ctx, eventID, orders); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to update event challenges order")
	}
	return nil
}

func (u *EventUseCase) GetTeamsSolvedChallenge(ctx context.Context, eventID, challengeID uuid.UUID) ([]*model.TeamSolvedChallenge, error) {
	solvedBy, err := u.service.GetEventChallengeSolvedBy(ctx, eventID, challengeID)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get event challenge solved by")
	}

	return solvedBy.Teams, nil
}

func (u *EventUseCase) SolveChallenge(ctx context.Context, eventID, challengeID uuid.UUID, solution string) (bool, error) {
	// check if user has team in event
	team, err := u.GetSelfTeam(ctx, eventID)
	if err != nil {
		return false, appError.NewError().WithError(err).WithMessage("failed to get self team")
	}

	// check if allowed to solve challenge
	// if event is not started, or ended, or paused
	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return false, appError.NewError().WithError(err).WithMessage("failed to get event")
	}

	if event.StartTime.After(time.Now().UTC()) || event.FinishTime.Before(time.Now().UTC()) {
		return false, model.ErrSolutionAttemptNotAllowed
	}

	solved, err := u.service.SolveChallenge(ctx, eventID, team.ID, challengeID, solution)
	if err != nil {
		return false, appError.NewError().WithError(err).WithMessage("failed to solve challenge")
	}

	return solved, nil
}
