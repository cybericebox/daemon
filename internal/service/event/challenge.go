package event

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"
	"strings"
	"time"
)

type (
	IChallengeRepository interface {
		CreateEventChallenge(ctx context.Context, arg postgres.CreateEventChallengeParams) error

		GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]postgres.EventChallenge, error)
		GetEventChallengeByID(ctx context.Context, params postgres.GetEventChallengeByIDParams) (postgres.EventChallenge, error)
		DeleteEventChallenges(ctx context.Context, arg postgres.DeleteEventChallengesParams) error
		UpdateEventChallengeOrder(ctx context.Context, arg postgres.UpdateEventChallengeOrderParams) error

		WithTransaction(ctx context.Context) (withTx interface{}, commit func(), rollback func(), err error)

		GetTeamsSolvedChallengeInEvent(ctx context.Context, arg postgres.GetTeamsSolvedChallengeInEventParams) ([]postgres.GetTeamsSolvedChallengeInEventRow, error)

		GetChallengeFlag(ctx context.Context, arg postgres.GetChallengeFlagParams) (string, error)
		CreateEventChallengeSolutionAttempt(ctx context.Context, arg postgres.CreateEventChallengeSolutionAttemptParams) error

		CreateEventTeamChallenge(ctx context.Context, arg postgres.CreateEventTeamChallengeParams) error

		AddLabChallenges(ctx context.Context, labID uuid.UUID, configs []model.LabChallenge) error
		DeleteLabsChallenges(ctx context.Context, labIDs []uuid.UUID, exerciseIDs []uuid.UUID) error
	}

	IExerciseService interface {
		GetExercise(ctx context.Context, exerciseID uuid.UUID) (*model.Exercise, error)
	}
)

func (s *EventService) GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]*model.Challenge, error) {
	challenges, err := s.repository.GetEventChallenges(ctx, eventID)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get challenges from repository")
	}

	result := make([]*model.Challenge, 0, len(challenges))
	for _, challenge := range challenges {
		result = append(result, &model.Challenge{
			ID:             challenge.ID,
			EventID:        challenge.EventID,
			CategoryID:     challenge.CategoryID,
			ExerciseID:     challenge.ExerciseID,
			ExerciseTaskID: challenge.ExerciseTaskID,
			Name:           challenge.Name,
			Description:    challenge.Description,
			Points:         challenge.Points,
			Order:          challenge.OrderIndex,
			CreatedAt:      challenge.CreatedAt,
		})
	}

	return result, nil
}

func (s *EventService) GetEventChallengeByID(ctx context.Context, eventID uuid.UUID, challengeID uuid.UUID) (*model.Challenge, error) {
	challenge, err := s.repository.GetEventChallengeByID(ctx, postgres.GetEventChallengeByIDParams{
		EventID: eventID,
		ID:      challengeID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.ErrNotFound
		}
		return nil, appError.NewError().WithError(err).WithMessage("failed to get challenge by id from repository")
	}

	return &model.Challenge{
		ID:             challenge.ID,
		EventID:        challenge.EventID,
		CategoryID:     challenge.CategoryID,
		ExerciseID:     challenge.ExerciseID,
		ExerciseTaskID: challenge.ExerciseTaskID,
		Name:           challenge.Name,
		Description:    challenge.Description,
		Points:         challenge.Points,
		Order:          challenge.OrderIndex,
		CreatedAt:      challenge.CreatedAt,
	}, nil
}

func (s *EventService) GetEventChallengeSolvedBy(ctx context.Context, eventID, challengeID uuid.UUID) (*model.ChallengeSoledBy, error) {
	teamSolutions, err := s.repository.GetTeamsSolvedChallengeInEvent(ctx, postgres.GetTeamsSolvedChallengeInEventParams{
		EventID:     eventID,
		ChallengeID: challengeID,
	})
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get teams solved challenge from repository")
	}

	teams := make([]*model.TeamSolvedChallenge, 0, len(teamSolutions))
	for _, team := range teamSolutions {
		teams = append(teams, &model.TeamSolvedChallenge{
			ID:       team.ID,
			Name:     team.Name,
			SolvedAt: team.Timestamp,
		})
	}

	return &model.ChallengeSoledBy{
		ChallengeID: challengeID,
		Teams:       teams,
	}, nil
}

func (s *EventService) AddExercisesToEvent(ctx context.Context, eventID, categoryID uuid.UUID, exerciseIDs []uuid.UUID) error {
	ch, err := s.repository.GetEventChallenges(ctx, eventID)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to get challenges from repository")
	}

	count := len(ch)

	for _, id := range exerciseIDs {
		exercise, err := s.exerciseService.GetExercise(ctx, id)
		if err != nil {
			return appError.NewError().WithError(err).WithMessage(fmt.Sprintf("failed to get exercise by id %s", id.String()))
		}

		for _, task := range exercise.Data.Tasks {
			if err = s.repository.CreateEventChallenge(ctx, postgres.CreateEventChallengeParams{
				ID:             uuid.Must(uuid.NewV7()),
				EventID:        eventID,
				CategoryID:     categoryID,
				Name:           task.Name,
				Description:    task.Description,
				Points:         task.Points,
				OrderIndex:     int32(count + 1),
				ExerciseID:     exercise.ID,
				ExerciseTaskID: task.ID,
			}); err != nil {
				return appError.NewError().WithError(err).WithMessage("failed to create event challenge")
			}
			count++
		}
	}

	return nil
}

func (s *EventService) DeleteEventChallenges(ctx context.Context, eventID uuid.UUID, exerciseID uuid.UUID) error {
	if err := s.repository.DeleteEventChallenges(ctx, postgres.DeleteEventChallengesParams{
		EventID:    eventID,
		ExerciseID: exerciseID,
	}); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to delete event challenges")
	}

	return nil
}

func (s *EventService) UpdateEventChallengesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error {
	// start transaction
	for _, order := range orders {
		if err := s.repository.UpdateEventChallengeOrder(ctx, postgres.UpdateEventChallengeOrderParams{
			ID:         order.ID,
			EventID:    eventID,
			OrderIndex: order.OrderIndex,
			CategoryID: order.CategoryID,
		}); err != nil {
			// rollback transaction
			return appError.NewError().WithError(err).WithMessage("failed to update event challenge order")
		}
	}

	// commit transaction

	return nil
}

func (s *EventService) CreateEventTeamsChallenges(ctx context.Context, eventID uuid.UUID) error {
	var errs error

	// get all teams in event
	teams, err := s.repository.GetEventTeams(ctx, eventID)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to get teams from repository")
	}

	// get all challenges in event
	challenges, err := s.repository.GetEventChallenges(ctx, eventID)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to get challenges from repository")
	}

	// create team challenges
	for _, team := range teams {

		flags := make(map[uuid.UUID]string)
		// map[exerciseID][]instance
		exeInstances := make(map[uuid.UUID][]model.Instance)
	chF:
		for _, challenge := range challenges {

			// get exercise task
			exercise, err := s.exerciseService.GetExercise(ctx, challenge.ExerciseID)
			if err != nil {
				errs = multierror.Append(errs, appError.NewError().WithError(err).WithMessage(fmt.Sprintf("failed to get exercise by id %s", challenge.ExerciseID.String())))
				continue chF
			}

			//if exercise has instances save them
			if _, ok := exeInstances[challenge.ExerciseID]; !ok {
				exeInstances[challenge.ExerciseID] = make([]model.Instance, 0)
			}

			if len(exercise.Data.Instances) > 0 {
				for _, instance := range exercise.Data.Instances {
					exeInstances[challenge.ExerciseID] = append(exeInstances[challenge.ExerciseID], model.Instance{
						ID:    instance.ID,
						Name:  instance.Name,
						Image: instance.Image,
						LinkedTaskID: uuid.NullUUID{
							UUID:  instance.LinkedTaskID.UUID,
							Valid: instance.LinkedTaskID.Valid,
						},
						InstanceFlagVar: instance.InstanceFlagVar,
						EnvVars:         instance.EnvVars,
						DNSRecords:      instance.DNSRecords,
					})
				}
			}

			// find task for challenge
			for _, task := range exercise.Data.Tasks {
				if task.ID == challenge.ExerciseTaskID {
					// try to get team challenge
					flag, err := s.repository.GetChallengeFlag(ctx, postgres.GetChallengeFlagParams{
						ChallengeID: challenge.ID,
						TeamID:      team.ID,
					})
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						errs = multierror.Append(errs, appError.NewError().WithError(err).WithMessage("failed to get challenge flag from repository"))
						continue chF
					}

					// if flag is already set skip
					if err == nil && flag != "" {
						flags[task.ID] = flag
						break
					}

					// if challenge is not created yet
					// get solution for challenge
					flag, err = tools.GetSolutionForTask(task.Flags...)
					if err != nil {
						errs = multierror.Append(errs, appError.NewError().WithError(err).WithMessage("failed to generate flag for challenge"))
						continue chF
					}

					// create team challenge
					if err = s.repository.CreateEventTeamChallenge(ctx, postgres.CreateEventTeamChallengeParams{
						ID:          uuid.Must(uuid.NewV7()),
						EventID:     eventID,
						TeamID:      team.ID,
						ChallengeID: challenge.ID,
						Flag:        flag,
					}); err != nil {
						errs = multierror.Append(errs, appError.NewError().WithError(err).WithMessage("failed to create team challenge"))
						continue chF
					}
					// save flag
					flags[task.ID] = flag
					break
				}
			}
		}

		labChallenges := make([]model.LabChallenge, 0)

		for exID, insts := range exeInstances {
			for index, inst := range insts {
				// if instance has flag var add it to envs
				if inst.LinkedTaskID.Valid {
					// get instance envs
					envs := inst.EnvVars
					// add flag to envs
					envs = append(envs, model.EnvVar{
						Name:  inst.InstanceFlagVar,
						Value: flags[inst.LinkedTaskID.UUID],
					})
					// set updated envs to instance
					exeInstances[exID][index].EnvVars = envs
				}
			}

			labChallenges = append(labChallenges, model.LabChallenge{
				ID:        exID,
				Instances: insts,
			})
		}

		// create instances for team
		if err = s.repository.AddLabChallenges(ctx, team.LaboratoryID.UUID, labChallenges); err != nil {
			errs = multierror.Append(errs, appError.NewError().WithError(err).WithMessage("failed to add lab challenges"))
		}
	}

	if errs != nil {
		return errs
	}

	return nil
}

func (s *EventService) DeleteEventTeamsChallenges(ctx context.Context, eventID, exerciseID uuid.UUID) error {
	// get exercise
	exercise, err := s.exerciseService.GetExercise(ctx, exerciseID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.ErrNotFound
		}
		return appError.NewError().WithError(err).WithMessage(fmt.Sprintf("failed to get exercise by id %s", exerciseID.String()))
	}

	// if exercise has no instances return
	if len(exercise.Data.Instances) == 0 {
		return nil
	}

	// get all teams in event
	teams, err := s.repository.GetEventTeams(ctx, eventID)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to get teams from repository")
	}

	// get all labs in event
	labIDs := make([]uuid.UUID, 0)

	for _, team := range teams {
		labIDs = append(labIDs, team.LaboratoryID.UUID)
	}

	if err = s.repository.DeleteLabsChallenges(ctx, labIDs, []uuid.UUID{exerciseID}); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to delete lab challenges")
	}

	return nil
}

func (s *EventService) SolveChallenge(ctx context.Context, eventID, teamID, challengeID uuid.UUID, solutionAttempt string) (bool, error) {
	// get user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return false, appError.NewError().WithError(err).WithMessage("failed to get user id from context")
	}

	// get challenge flag
	flag, err := s.repository.GetChallengeFlag(ctx, postgres.GetChallengeFlagParams{
		ChallengeID: challengeID,
		TeamID:      teamID,
	})
	if err != nil {
		return false, appError.NewError().WithError(err).WithMessage("failed to get challenge flag from repository")
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
		return false, appError.NewError().WithError(err).WithMessage("failed to create event challenge solution attempt")
	}

	return isCorrect, nil
}
