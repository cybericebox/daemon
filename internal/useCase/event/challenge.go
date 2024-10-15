package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"slices"
)

type (
	IChallengeService interface {
		GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]*model.Challenge, error)
		GetEventChallengeByID(ctx context.Context, eventID uuid.UUID, challengeID uuid.UUID) (*model.Challenge, error)
		GetEventTeamsChallengeSolvedBy(ctx context.Context, eventID, challengeID uuid.UUID) (*model.TeamsChallengeSolvedBy, error)

		AddEventChallenges(ctx context.Context, eventID, categoryID uuid.UUID, exercises []*model.Exercise) error
		DeleteEventChallenges(ctx context.Context, eventID uuid.UUID, exerciseIDs []uuid.UUID) error
		UpdateEventChallengesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error
		//
		GetExercisesByIDs(ctx context.Context, exerciseIDs []uuid.UUID) ([]*model.Exercise, error)

		//DeleteEventTeamsChallenges(ctx context.Context, eventID, exerciseID uuid.UUID) error
	}
)

// for administrators

func (u *EventUseCase) GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]*model.Challenge, error) {
	challenges, err := u.service.GetEventChallenges(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventChallenge.WithError(err).WithMessage("Failed to get event challenges").Cause()
	}
	return challenges, nil
}

func (u *EventUseCase) AddEventChallenges(ctx context.Context, eventID, categoryID uuid.UUID, exerciseIDs []uuid.UUID) error {
	// get exercises by ids
	exercises, err := u.service.GetExercisesByIDs(ctx, exerciseIDs)
	if err != nil {
		return model.ErrEventChallenge.WithError(err).WithMessage("Failed to get exercises by ids").Cause()
	}

	if err = u.service.AddEventChallenges(ctx, eventID, categoryID, exercises); err != nil {
		return model.ErrEventChallenge.WithError(err).WithMessage("Failed to add exercises to event").Cause()
	}

	// create event teams challenges
	if err = u.CreateEventTeamsChallenges(ctx, eventID); err != nil {
		return model.ErrEventChallenge.WithError(err).WithMessage("Failed to create event teams challenges").Cause()
	}

	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return model.ErrEventChallenge.WithError(err).WithMessage("Failed to get event").Cause()
	}

	u.AddCreateTeamsChallengesTask(ctx, *event)

	return nil
}

func (u *EventUseCase) DeleteEventChallenge(ctx context.Context, eventID uuid.UUID, challengeID uuid.UUID) error {
	challenge, err := u.service.GetEventChallengeByID(ctx, eventID, challengeID)
	if err != nil {
		return model.ErrEventChallenge.WithError(err).WithMessage("Failed to get event challenge by id").Cause()
	}

	if err = u.service.DeleteEventChallenges(ctx, eventID, []uuid.UUID{challenge.ExerciseID}); err != nil {
		return model.ErrEventChallenge.WithError(err).WithMessage("Failed to delete event challenges").Cause()
	}

	if err = u.DeleteEventTeamsChallengeInfrastructureByExerciseID(ctx, eventID, challenge.ExerciseID); err != nil {
		return model.ErrEventChallenge.WithError(err).WithMessage("Failed to delete event teams challenges").Cause()
	}

	return nil
}

func (u *EventUseCase) UpdateEventChallengesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error {
	if err := u.service.UpdateEventChallengesOrder(ctx, eventID, orders); err != nil {
		return model.ErrEventChallenge.WithError(err).WithMessage("Failed to update event challenges order").Cause()
	}
	return nil
}

// for participants

func (u *EventUseCase) GetEventChallengesInfo(ctx context.Context, eventID uuid.UUID) ([]*model.CategoryInfo, error) {
	// check if user has team in event
	team, err := u.GetSelfTeam(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventChallenge.WithError(err).WithMessage("Failed to get self team").Cause()
	}

	challenges, err := u.GetEventChallenges(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventChallenge.WithError(err).WithMessage("Failed to get event challenges").Cause()
	}

	categories, err := u.GetEventCategories(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventChallenge.WithError(err).WithMessage("Failed to get event categories").Cause()
	}

	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventChallenge.WithError(err).WithMessage("Failed to get event").Cause()
	}

	result := make([]*model.CategoryInfo, 0, len(categories))
	for _, category := range categories {
		challengesInCategory := make([]*model.ChallengeInfo, 0, len(challenges))
		for _, challenge := range challenges {
			if challenge.CategoryID == category.ID {
				// count challenge points
				points := challenge.Data.Points

				solvedBy, err := u.service.GetEventTeamsChallengeSolvedBy(ctx, eventID, challenge.ID)
				if err != nil {
					return nil, model.ErrEventChallenge.WithError(err).WithMessage("Failed to get event challenge solved by").Cause()
				}

				if event.DynamicScoring {
					count := len(solvedBy.Teams)

					// calculate points
					points = tools.CalculateScore(event.DynamicMinScore, event.DynamicMaxScore, event.DynamicSolveThreshold, float64(count))
				}

				// check if challenge is solved by team
				solved := slices.IndexFunc(solvedBy.Teams, func(t *model.TeamChallengeSolvedBy) bool {
					return t.ID == team.ID
				}) != -1 // -1 if not solved

				challengesInCategory = append(challengesInCategory, &model.ChallengeInfo{
					ID:            challenge.ID,
					Name:          challenge.Data.Name,
					Description:   challenge.Data.Description,
					Points:        points,
					AttachedFiles: challenge.Data.AttachedFiles,
					Solved:        solved,
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

func (u *EventUseCase) GetTeamsChallengeSolvedBy(ctx context.Context, eventID, challengeID uuid.UUID) ([]*model.TeamChallengeSolvedBy, error) {
	solvedBy, err := u.service.GetEventTeamsChallengeSolvedBy(ctx, eventID, challengeID)
	if err != nil {
		return nil, model.ErrEventChallenge.WithError(err).WithMessage("Failed to get event challenge solved by").Cause()
	}

	return solvedBy.Teams, nil
}

func (u *EventUseCase) GetDownloadAttachedFileLink(ctx context.Context, eventID, challengeID, fileID uuid.UUID) (string, error) {
	challenge, err := u.service.GetEventChallengeByID(ctx, eventID, challengeID)
	if err != nil {
		return "", model.ErrEventChallenge.WithError(err).WithMessage("Failed to get exercise").Cause()
	}

	// find file
	fileName := fileID.String()
	for _, file := range challenge.Data.AttachedFiles {
		if file.ID == fileID {
			fileName = file.Name
			break
		}
	}

	downloadFileLink, err := u.service.GetDownloadFileLink(ctx, model.DownloadFileParams{
		StorageType: model.TaskStorageType,
		FileID:      fileID,
		FileName:    fileName,
	})
	if err != nil {
		return "", model.ErrEventChallenge.WithError(err).WithMessage("Failed to get download file link").Cause()
	}
	return downloadFileLink, nil
}

// other
