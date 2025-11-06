package event

import (
	"context"
	"slices"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	storageModel "github.com/cybericebox/daemon/internal/model/storage"
	"github.com/cybericebox/daemon/internal/tools"
)

type (
	IChallengeService interface {
		GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]*eventModel.Challenge, error)
		GetEventChallengeByID(ctx context.Context, challengeID uuid.UUID) (*eventModel.Challenge, error)
		GetEventTeamsChallengeSolvedBy(
			ctx context.Context,
			eventID, challengeID uuid.UUID,
			page, pageSize int,
		) (*eventModel.TeamsChallengeSolvedBy, error)

		AddEventChallenges(
			ctx context.Context,
			eventID, categoryID uuid.UUID,
			exercises []*exerciseModel.Exercise,
		) error
		DeleteEventChallenges(ctx context.Context, eventID uuid.UUID, exerciseIDs []uuid.UUID) error
		UpdateEventChallengesOrder(ctx context.Context, orders []eventModel.Order) error
		//
		GetExercisesWithIDs(ctx context.Context, exerciseIDs []uuid.UUID) ([]*exerciseModel.Exercise, error)

		// DeleteEventTeamsChallenges(ctx context.Context, eventID, exerciseID uuid.UUID) error
	}
)

// for administrators

func (u *EventUseCase) GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]*eventModel.Challenge, error) {
	challenges, err := u.service.GetEventChallenges(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event challenges").Err()
	}
	return challenges, nil
}

func (u *EventUseCase) AddEventChallenges(
	ctx context.Context,
	eventID, categoryID uuid.UUID,
	exerciseIDs []uuid.UUID,
) error {
	// get exercises by ids
	exercises, err := u.service.GetExercisesWithIDs(ctx, exerciseIDs)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get exercises by ids").Err()
	}

	if err = u.service.AddEventChallenges(ctx, eventID, categoryID, exercises); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to add exercises to event").Err()
	}

	// create event teams challenges
	if err = u.CreateEventTeamsChallenges(ctx, eventID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create event teams challenges").Err()
	}

	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}

	u.AddCreateTeamsChallengesTask(ctx, *event)

	return nil
}

func (u *EventUseCase) DeleteEventChallenge(ctx context.Context, eventID uuid.UUID, challengeID uuid.UUID) error {
	challenge, err := u.service.GetEventChallengeByID(ctx, challengeID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event challenge by id").Err()
	}

	if err = u.service.DeleteEventChallenges(ctx, eventID, []uuid.UUID{challenge.ExerciseID}); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event challenges").Err()
	}

	if err = u.DeleteEventTeamsChallengeInfrastructureByExerciseID(ctx, eventID, challenge.ExerciseID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event teams challenges").Err()
	}

	return nil
}

func (u *EventUseCase) UpdateEventChallengesOrder(
	ctx context.Context,
	orders []eventModel.Order,
) error {
	if err := u.service.UpdateEventChallengesOrder(ctx, orders); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update event challenges order").Err()
	}
	return nil
}

// for participants

func (u *EventUseCase) GetEventChallengesInfo(
	ctx context.Context,
	eventID uuid.UUID,
) ([]*eventModel.ChallengeCategoryInfo, error) {
	// check if user has team in event
	team, err := u.GetSelfTeam(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get self team").Err()
	}

	challenges, err := u.GetEventChallenges(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event challenges").Err()
	}

	categories, err := u.GetEventCategories(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event categories").Err()
	}

	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}

	result := make([]*eventModel.ChallengeCategoryInfo, 0, len(categories))
	for _, category := range categories {
		challengesInCategory := make([]*eventModel.ChallengeInfo, 0, len(challenges))
		for _, challenge := range challenges {
			if challenge.CategoryID == category.ID {
				// count challenge points
				points := challenge.Data.Points

				solvedBy, err := u.service.GetEventTeamsChallengeSolvedBy(
					ctx,
					eventID,
					challenge.ID,
					config.AllPages,
					0,
				)
				if err != nil {
					return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event challenge solved by").Err()
				}

				if event.DynamicScoring {
					count := len(solvedBy.Teams)

					// calculate points
					points = tools.CalculateScore(
						event.DynamicMinScore,
						event.DynamicMaxScore,
						event.DynamicSolveThreshold,
						float64(count),
					)
				}

				// check if challenge is solved by team
				solved := slices.IndexFunc(
					solvedBy.Teams, func(t *eventModel.TeamChallengeSolvedBy) bool {
						return t.ID == team.ID
					},
				) != -1 // -1 if not solved

				challengesInCategory = append(
					challengesInCategory, &eventModel.ChallengeInfo{
						ID:            challenge.ID,
						Name:          challenge.Data.Name,
						Description:   challenge.Data.Description,
						Points:        points,
						AttachedFiles: challenge.Data.AttachedFiles,
						Solved:        solved,
					},
				)

			}
		}
		result = append(
			result, &eventModel.ChallengeCategoryInfo{
				ID:         category.ID,
				Name:       category.Name,
				Challenges: challengesInCategory,
			},
		)
	}

	return result, nil
}

func (u *EventUseCase) GetTeamsChallengeSolvedBy(
	ctx context.Context,
	eventID, challengeID uuid.UUID,
	page, pageSize int,
) ([]*eventModel.TeamChallengeSolvedBy, error) {
	solvedBy, err := u.service.GetEventTeamsChallengeSolvedBy(ctx, eventID, challengeID, page, pageSize)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event challenge solved by").Err()
	}

	return solvedBy.Teams, nil
}

func (u *EventUseCase) GetDownloadAttachedFileLink(ctx context.Context, challengeID, fileID uuid.UUID) (
	string,
	error,
) {
	challenge, err := u.service.GetEventChallengeByID(ctx, challengeID)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}

	// find file
	fileName := fileID.String()
	for _, file := range challenge.Data.AttachedFiles {
		if file.ID == fileID {
			fileName = file.Name
			break
		}
	}

	downloadFileLink, err := u.service.GetDownloadFileURL(
		ctx, storageModel.DownloadFileParams{
			StorageType: storageModel.TaskStorageType,
			FileID:      fileID,
			FileName:    fileName,
		},
	)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get download file link").Err()
	}
	return string(downloadFileLink), nil
}

// other
