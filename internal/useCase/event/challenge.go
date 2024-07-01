package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/cybericebox/daemon/pkg/worker"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
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
	return u.service.GetEventChallenges(ctx, eventID)
}

func (u *EventUseCase) GetEventChallengesInfo(ctx context.Context, eventID uuid.UUID) ([]*model.CategoryInfo, error) {
	// check if user has team in event
	team, err := u.GetSelfTeam(ctx, eventID)
	if err != nil {
		return nil, err
	}

	challenges, err := u.GetEventChallenges(ctx, eventID)
	if err != nil {
		return nil, err
	}

	categories, err := u.GetEventCategories(ctx, eventID)
	if err != nil {
		return nil, err
	}

	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return nil, err
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
					return nil, err
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
		return err
	}

	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return err
	}

	u.worker.AddTask(worker.Task{
		Do: func() {
			if err = u.service.CreateEventTeamsChallenges(ctx, event.ID); err != nil {
				log.Error().Err(err).Msg("failed to create event teams challenges")
			}
		},
		CheckIfNeedToDo: func() (bool, *time.Time) {
			e, err := u.service.GetEventByID(ctx, event.ID)
			if err != nil {
				log.Error().Err(err).Msg("failed to get event")
				return false, nil
			}

			next := e.StartTime.Add(-time.Minute)

			return e.StartTime.Add(-time.Minute).Before(time.Now().UTC()), &next
		},
		TimeToDo: event.StartTime.Add(-time.Minute),
	})

	return nil
}

func (u *EventUseCase) DeleteEventChallenge(ctx context.Context, eventID uuid.UUID, challengeID uuid.UUID) error {
	challenge, err := u.service.GetEventChallengeByID(ctx, eventID, challengeID)
	if err != nil {
		return err
	}

	if err = u.service.DeleteEventTeamsChallenges(ctx, eventID, challenge.ExerciseID); err != nil {
		return err
	}

	if err = u.service.DeleteEventChallenges(ctx, eventID, challengeID); err != nil {
		return err
	}

	return nil
}

func (u *EventUseCase) UpdateEventChallengesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error {
	return u.service.UpdateEventChallengesOrder(ctx, eventID, orders)
}

func (u *EventUseCase) GetTeamsSolvedChallenge(ctx context.Context, eventID, challengeID uuid.UUID) ([]*model.TeamSolvedChallenge, error) {
	solvedBy, err := u.service.GetEventChallengeSolvedBy(ctx, eventID, challengeID)
	if err != nil {
		return nil, err
	}

	return solvedBy.Teams, nil
}

func (u *EventUseCase) SolveChallenge(ctx context.Context, eventID, challengeID uuid.UUID, solution string) (bool, error) {
	// check if user has team in event
	team, err := u.GetSelfTeam(ctx, eventID)
	if err != nil {
		return false, err
	}

	// check if allowed to solve challenge
	// if event is not started, or ended, or paused
	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return false, err
	}

	if event.StartTime.After(time.Now().UTC()) || event.FinishTime.Before(time.Now().UTC()) {
		return false, model.ErrSolutionAttemptNotAllowed
	}

	solved, err := u.service.SolveChallenge(ctx, eventID, team.ID, challengeID, solution)
	if err != nil {
		return false, err
	}

	return solved, nil
}
