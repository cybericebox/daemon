package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog/log"
)

type (
	IChallengeRepository interface {
		CreateEventChallenge(ctx context.Context, arg []postgres.CreateEventChallengeParams) *postgres.CreateEventChallengeBatchResults

		CountChallengesInCategoryInEvent(ctx context.Context, arg postgres.CountChallengesInCategoryInEventParams) (int64, error)
		GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]postgres.EventChallenge, error)
		GetEventChallengeByID(ctx context.Context, arg postgres.GetEventChallengeByIDParams) (postgres.EventChallenge, error)

		DeleteEventChallenges(ctx context.Context, arg []postgres.DeleteEventChallengesParams) *postgres.DeleteEventChallengesBatchResults

		UpdateEventChallengeOrder(ctx context.Context, arg []postgres.UpdateEventChallengeOrderParams) *postgres.UpdateEventChallengeOrderBatchResults

		GetTeamsChallengeSolvedByInEvent(ctx context.Context, arg postgres.GetTeamsChallengeSolvedByInEventParams) ([]postgres.GetTeamsChallengeSolvedByInEventRow, error)

		GetChallengeFlag(ctx context.Context, arg postgres.GetChallengeFlagParams) (string, error)
		CreateEventChallengeSolutionAttempt(ctx context.Context, arg postgres.CreateEventChallengeSolutionAttemptParams) error
	}
)

func (s *EventService) GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]*model.Challenge, error) {
	challenges, err := s.repository.GetEventChallenges(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventChallenge.WithError(err).WithMessage("Failed to get challenges from repository").Cause()
	}

	result := make([]*model.Challenge, 0, len(challenges))
	for _, challenge := range challenges {
		result = append(result, &model.Challenge{
			ID:             challenge.ID,
			EventID:        challenge.EventID,
			CategoryID:     challenge.CategoryID,
			Data:           challenge.Data,
			ExerciseID:     challenge.ExerciseID,
			ExerciseTaskID: challenge.ExerciseTaskID,
			Order:          challenge.OrderIndex,
			CreatedAt:      challenge.CreatedAt,
		})
	}

	return result, nil
}

func (s *EventService) GetEventTeamsChallengeSolvedBy(ctx context.Context, eventID, challengeID uuid.UUID) (*model.TeamsChallengeSolvedBy, error) {
	teamSolutions, err := s.repository.GetTeamsChallengeSolvedByInEvent(ctx, postgres.GetTeamsChallengeSolvedByInEventParams{
		EventID:     eventID,
		ChallengeID: challengeID,
	})
	if err != nil {
		return nil, model.ErrEventChallenge.WithError(err).WithMessage("Failed to get teams solved challenge from repository").Cause()
	}

	teams := make([]*model.TeamChallengeSolvedBy, 0, len(teamSolutions))
	for _, team := range teamSolutions {
		teams = append(teams, &model.TeamChallengeSolvedBy{
			ID:       team.ID,
			Name:     team.Name,
			SolvedAt: team.Timestamp,
		})
	}

	return &model.TeamsChallengeSolvedBy{
		ChallengeID: challengeID,
		Teams:       teams,
	}, nil
}

func (s *EventService) GetEventChallengeByID(ctx context.Context, eventID, challengeID uuid.UUID) (*model.Challenge, error) {
	challenge, err := s.repository.GetEventChallengeByID(ctx, postgres.GetEventChallengeByIDParams{
		ID:      challengeID,
		EventID: eventID,
	})
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, model.ErrEventChallengeChallengeNotFound.WithMessage("Event challenge not found").Cause()
		}
		return nil, model.ErrEventChallenge.WithError(err).WithMessage("Failed to get challenge from repository").Cause()
	}

	return &model.Challenge{
		ID:             challenge.ID,
		EventID:        challenge.EventID,
		CategoryID:     challenge.CategoryID,
		Data:           challenge.Data,
		ExerciseID:     challenge.ExerciseID,
		ExerciseTaskID: challenge.ExerciseTaskID,
		Order:          challenge.OrderIndex,
		CreatedAt:      challenge.CreatedAt,
	}, nil
}

func (s *EventService) AddEventChallenges(ctx context.Context, eventID, categoryID uuid.UUID, exercises []*model.Exercise) error {
	count, err := s.repository.CountChallengesInCategoryInEvent(ctx, postgres.CountChallengesInCategoryInEventParams{
		EventID:    eventID,
		CategoryID: categoryID,
	})
	if err != nil {
		return model.ErrEventChallenge.WithError(err).WithMessage("Failed to count challenges in category in event").Cause()
	}

	createParams := make([]postgres.CreateEventChallengeParams, 0, len(exercises))

	for _, exercise := range exercises {
		for _, task := range exercise.Data.Tasks {
			// create challenge data
			data := model.ChallengeData{
				Name:          task.Name,
				Description:   task.Description,
				Points:        task.Points,
				AttachedFiles: make([]model.ExerciseFile, 0, len(task.AttachedFileIDs)),
			}

			// add attached files
			for _, fileID := range task.AttachedFileIDs {
				for _, file := range exercise.Data.Files {
					if file.ID == fileID {
						data.AttachedFiles = append(data.AttachedFiles, model.ExerciseFile{
							ID:   file.ID,
							Name: file.Name,
						})
						break
					}
				}
			}
			createParams = append(createParams, postgres.CreateEventChallengeParams{
				ID:             uuid.Must(uuid.NewV7()),
				EventID:        eventID,
				CategoryID:     categoryID,
				Data:           data,
				OrderIndex:     int32(count + 1),
				ExerciseID:     exercise.ID,
				ExerciseTaskID: task.ID,
			})
			count++
		}
	}

	batchResult := s.repository.CreateEventChallenge(ctx, createParams)
	defer func() {
		if err = batchResult.Close(); err != nil {
			log.Error().Err(err).Msg("Failed to close batch result")
		}
	}()

	var errs error
	batchResult.Exec(func(i int, err error) {
		if err != nil {
			if tools.IsUniqueViolationError(err) {
				errs = multierror.Append(errs, model.ErrEventChallengeChallengeExists.Cause())
				return
			}
			errCreator, has := tools.ForeignKeyViolationError(err)
			if has {
				errs = multierror.Append(errs, errCreator.Cause())
				return
			}
			errs = multierror.Append(errs, model.ErrEventChallenge.WithError(err).WithMessage("Failed to create event challenge").WithContext("ExerciseID", createParams[i].ExerciseID).WithContext("TaskID", createParams[i].ExerciseTaskID).Cause())
		}
	})

	if errs != nil {
		return model.ErrEventChallenge.WithError(errs).WithMessage("Failed to create event challenges").Cause()
	}

	return nil
}

func (s *EventService) DeleteEventChallenges(ctx context.Context, eventID uuid.UUID, exerciseIDs []uuid.UUID) error {

	deleteParams := make([]postgres.DeleteEventChallengesParams, 0, len(exerciseIDs))
	for _, exerciseID := range exerciseIDs {
		deleteParams = append(deleteParams, postgres.DeleteEventChallengesParams{
			EventID:    eventID,
			ExerciseID: exerciseID,
		})
	}

	batchResult := s.repository.DeleteEventChallenges(ctx, deleteParams)
	defer func() {
		if err := batchResult.Close(); err != nil {
			log.Error().Err(err).Msg("Failed to close batch result")
		}
	}()

	var errs error
	batchResult.Exec(func(i int, affected int64, err error) {
		if err != nil {
			errs = multierror.Append(errs, model.ErrEventChallenge.WithError(err).WithMessage("Failed to delete event challenge").WithContext("ExerciseID", deleteParams[i].ExerciseID).Cause())
		}
		if affected == 0 {
			errs = multierror.Append(errs, model.ErrEventChallengeChallengeNotFound.WithMessage("Event challenges not found").WithContext("ExerciseID", deleteParams[i].ExerciseID).Cause())
		}
	})

	// remain rest challenges order
	challenges, err := s.repository.GetEventChallenges(ctx, eventID)
	if err != nil {
		return model.ErrEventChallenge.WithError(err).WithMessage("Failed to get event challenges from repository").Cause()
	}

	orderParams := make([]postgres.UpdateEventChallengeOrderParams, 0, len(challenges))
	for _, challenge := range challenges {
		orderParams = append(orderParams, postgres.UpdateEventChallengeOrderParams{
			EventID:    eventID,
			ID:         challenge.ID,
			OrderIndex: challenge.OrderIndex,
			CategoryID: challenge.CategoryID,
		})
	}

	if err = s.updateEventChallengesOrder(ctx, orderParams); err != nil {
		return model.ErrEventChallenge.WithError(err).WithMessage("Failed to update event categories order after delete").Cause()
	}

	if errs != nil {
		return model.ErrEventChallenge.WithError(errs).WithMessage("Failed to delete event challenges").Cause()
	}

	return nil
}

func (s *EventService) UpdateEventChallengesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
	}

	params := make([]postgres.UpdateEventChallengeOrderParams, 0, len(orders))

	for _, order := range orders {
		params = append(params, postgres.UpdateEventChallengeOrderParams{
			EventID:    eventID,
			ID:         order.ID,
			OrderIndex: order.Index,
			CategoryID: order.CategoryID,
			UpdatedBy: uuid.NullUUID{
				UUID:  currentUserID,
				Valid: true,
			},
		})
	}

	return s.updateEventChallengesOrder(ctx, params)
}

func (s *EventService) updateEventChallengesOrder(ctx context.Context, orderParams []postgres.UpdateEventChallengeOrderParams) error {
	batchResult := s.repository.UpdateEventChallengeOrder(ctx, orderParams)
	defer func() {
		if err := batchResult.Close(); err != nil {
			log.Error().Err(err).Msg("Failed to close batch result")
		}
	}()

	var errs error
	batchResult.Exec(func(i int, affected int64, err error) {
		if err != nil {
			errCreator, has := tools.ForeignKeyViolationError(err)
			if has {
				errs = multierror.Append(errs, errCreator.Cause())
				return
			}
			errs = multierror.Append(errs, model.ErrEventChallenge.WithError(err).WithMessage("Failed to update event challenge order").WithContext("ChallengeID", orderParams[i].ID).Cause())
		}

		if affected == 0 {
			errs = multierror.Append(errs, model.ErrEventChallengeChallengeNotFound.WithMessage("Event challenge not found").WithContext("ChallengeID", orderParams[i].ID).Cause())
		}
	})

	if errs != nil {
		return model.ErrEventChallenge.WithError(errs).WithMessage("Failed to update event challenge order").Cause()
	}

	return nil
}
