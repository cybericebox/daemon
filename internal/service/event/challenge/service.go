package challengeService

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog/log"
)

type (
	ChallengeService struct {
		repository IRepository
	}

	IRepository interface {
		CreateEventChallenge(ctx context.Context, arg []postgres.CreateEventChallengeParams) *postgres.CreateEventChallengeBatchResults

		CountChallengesInCategoryInEvent(ctx context.Context, categoryID uuid.UUID) (int64, error)
		GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]postgres.EventChallenge, error)
		GetEventChallengeByID(ctx context.Context, id uuid.UUID) (postgres.EventChallenge, error)

		DeleteEventChallenges(ctx context.Context, arg []postgres.DeleteEventChallengesParams) *postgres.DeleteEventChallengesBatchResults

		UpdateEventChallengeOrder(ctx context.Context, arg []postgres.UpdateEventChallengeOrderParams) *postgres.UpdateEventChallengeOrderBatchResults

		GetTeamsChallengeSolvedByInEvent(ctx context.Context, arg postgres.GetTeamsChallengeSolvedByInEventParams) ([]postgres.GetTeamsChallengeSolvedByInEventRow, error)
		GetTeamsChallengeSolvedByInEventPaged(ctx context.Context, arg postgres.GetTeamsChallengeSolvedByInEventPagedParams) ([]postgres.GetTeamsChallengeSolvedByInEventPagedRow, error)

		GetChallengeFlag(ctx context.Context, arg postgres.GetChallengeFlagParams) (string, error)
		CreateEventChallengeSolutionAttempt(ctx context.Context, arg postgres.CreateEventChallengeSolutionAttemptParams) error
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *ChallengeService {
	return &ChallengeService{
		repository: deps.Repository,
	}
}

func (s *ChallengeService) GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]*eventModel.Challenge, error) {
	challenges, err := s.repository.GetEventChallenges(ctx, eventID)
	if err != nil {
		return nil, eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to get challenges from repository").Err()
	}

	result := make([]*eventModel.Challenge, 0, len(challenges))
	for _, challenge := range challenges {
		result = append(result, &eventModel.Challenge{
			ID:             challenge.ID,
			EventID:        challenge.EventID,
			CategoryID:     challenge.CategoryID,
			Data:           challenge.Data,
			ExerciseID:     challenge.ExerciseID,
			ExerciseTaskID: challenge.ExerciseTaskID,
			Order:          challenge.OrderIndex,
			UpdatedAt:      challenge.UpdatedAt.Time,
			UpdatedBy:      challenge.UpdatedBy,
			CreatedAt:      challenge.CreatedAt,
		})
	}

	return result, nil
}

func (s *ChallengeService) GetEventTeamsChallengeSolvedBy(ctx context.Context, eventID, challengeID uuid.UUID, page int) (*eventModel.TeamsChallengeSolvedBy, error) {
	if page == config.AllPages {
		teamSolutions, err := s.repository.GetTeamsChallengeSolvedByInEvent(ctx, postgres.GetTeamsChallengeSolvedByInEventParams{
			EventID:     eventID,
			ChallengeID: challengeID,
		})
		if err != nil {
			return nil, eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to get teams solved challenge from repository").Err()
		}

		teams := make([]*eventModel.TeamChallengeSolvedBy, 0, len(teamSolutions))
		for _, team := range teamSolutions {
			teams = append(teams, &eventModel.TeamChallengeSolvedBy{
				ID:       team.ID,
				Name:     team.Name,
				SolvedAt: team.Timestamp,
				Hidden:   team.Hidden,
			})
		}

		return &eventModel.TeamsChallengeSolvedBy{
			ChallengeID: challengeID,
			Teams:       teams,
		}, nil
	}

	teamSolutions, err := s.repository.GetTeamsChallengeSolvedByInEventPaged(ctx, postgres.GetTeamsChallengeSolvedByInEventPagedParams{
		EventID:     eventID,
		ChallengeID: challengeID,
		Limit:       config.DefaultOnePageLimit,
		Offset:      int32(page * config.DefaultOnePageLimit),
	})
	if err != nil {
		return nil, eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to get teams solved challenge from repository").Err()
	}

	teams := make([]*eventModel.TeamChallengeSolvedBy, 0, len(teamSolutions))
	for _, team := range teamSolutions {
		teams = append(teams, &eventModel.TeamChallengeSolvedBy{
			ID:       team.ID,
			Name:     team.Name,
			SolvedAt: team.Timestamp,
			Hidden:   team.Hidden,
		})
	}

	return &eventModel.TeamsChallengeSolvedBy{
		ChallengeID: challengeID,
		Teams:       teams,
	}, nil
}

func (s *ChallengeService) GetEventChallengeByID(ctx context.Context, challengeID uuid.UUID) (*eventModel.Challenge, error) {
	challenge, err := s.repository.GetEventChallengeByID(ctx, challengeID)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, eventModel.ErrEventChallengeChallengeNotFound.WithMessage("Event challenge not found").Err()
		}
		return nil, eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to get challenge from repository").Err()
	}

	return &eventModel.Challenge{
		ID:             challenge.ID,
		EventID:        challenge.EventID,
		CategoryID:     challenge.CategoryID,
		Data:           challenge.Data,
		ExerciseID:     challenge.ExerciseID,
		ExerciseTaskID: challenge.ExerciseTaskID,
		Order:          challenge.OrderIndex,
		UpdatedAt:      challenge.UpdatedAt.Time,
		UpdatedBy:      challenge.UpdatedBy,
		CreatedAt:      challenge.CreatedAt,
	}, nil
}

func (s *ChallengeService) AddEventChallenges(ctx context.Context, eventID, categoryID uuid.UUID, exercises []*exerciseModel.Exercise) error {
	count, err := s.repository.CountChallengesInCategoryInEvent(ctx, categoryID)
	if err != nil {
		return eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to count challenges in category in event").Err()
	}

	createParams := make([]postgres.CreateEventChallengeParams, 0, len(exercises))

	for _, exercise := range exercises {
		for _, task := range exercise.Data.Tasks {
			// create challenge data
			data := eventModel.ChallengeData{
				Name:          task.Name,
				Description:   task.Description,
				Points:        task.Points,
				AttachedFiles: make([]exerciseModel.ExerciseFile, 0, len(task.AttachedFileIDs)),
			}

			// add attached files
			for _, fileID := range task.AttachedFileIDs {
				for _, file := range exercise.Data.Files {
					if file.ID == fileID {
						data.AttachedFiles = append(data.AttachedFiles, exerciseModel.ExerciseFile{
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
			errCreator, has := tools.UniqueViolationError(err, eventModel.ErrEventChallengeChallengeExists)
			if has {
				errs = multierror.Append(errs, errCreator.Err())
				return
			}
			errCreator, has = tools.ForeignKeyViolationError(err)
			if has {
				errs = multierror.Append(errs, errCreator.Err())
				return
			}
			errs = multierror.Append(errs, eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to create event challenge").WithContext("ExerciseID", createParams[i].ExerciseID).WithContext("TaskID", createParams[i].ExerciseTaskID).Err())
		}
	})

	if errs != nil {
		return eventModel.ErrEventChallenge.WithError(errs).WithMessage("Failed to create event challenges").Err()
	}

	return nil
}

func (s *ChallengeService) DeleteEventChallenges(ctx context.Context, eventID uuid.UUID, exerciseIDs []uuid.UUID) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
	}

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
			errs = multierror.Append(errs, eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to delete event challenge").WithContext("ExerciseID", deleteParams[i].ExerciseID).Err())
		}
		if affected == 0 {
			errs = multierror.Append(errs, eventModel.ErrEventChallengeChallengeNotFound.WithMessage("Event challenges not found").WithContext("ExerciseID", deleteParams[i].ExerciseID).Err())
		}
	})

	// remain rest challenges order
	challenges, err := s.repository.GetEventChallenges(ctx, eventID)
	if err != nil {
		return eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to get event challenges from repository").Err()
	}

	orderParams := make([]postgres.UpdateEventChallengeOrderParams, 0, len(challenges))
	for _, challenge := range challenges {
		orderParams = append(orderParams, postgres.UpdateEventChallengeOrderParams{
			ID:         challenge.ID,
			OrderIndex: challenge.OrderIndex,
			CategoryID: challenge.CategoryID,
			UpdatedBy: uuid.NullUUID{
				UUID:  currentUserID,
				Valid: true,
			},
		})
	}

	if err = s.updateEventChallengesOrder(ctx, orderParams); err != nil {
		return eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to update event categories order after delete").Err()
	}

	if errs != nil {
		return eventModel.ErrEventChallenge.WithError(errs).WithMessage("Failed to delete event challenges").Err()
	}

	return nil
}

func (s *ChallengeService) UpdateEventChallengesOrder(ctx context.Context, orders []eventModel.Order) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
	}

	params := make([]postgres.UpdateEventChallengeOrderParams, 0, len(orders))

	for _, order := range orders {
		params = append(params, postgres.UpdateEventChallengeOrderParams{
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

func (s *ChallengeService) updateEventChallengesOrder(ctx context.Context, orderParams []postgres.UpdateEventChallengeOrderParams) error {
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
				errs = multierror.Append(errs, errCreator.Err())
				return
			}
			errs = multierror.Append(errs, eventModel.ErrEventChallenge.WithError(err).WithMessage("Failed to update event challenge order").WithContext("ChallengeID", orderParams[i].ID).Err())
		}

		if affected == 0 {
			errs = multierror.Append(errs, eventModel.ErrEventChallengeChallengeNotFound.WithMessage("Event challenge not found").WithContext("ChallengeID", orderParams[i].ID).Err())
		}
	})

	if errs != nil {
		return eventModel.ErrEventChallenge.WithError(errs).WithMessage("Failed to update event challenge order").Err()
	}

	return nil
}
