package event

import (
	"context"
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

		DeleteEventChallenge(ctx context.Context, arg postgres.DeleteEventChallengeParams) error
		UpdateEventChallengeOrder(ctx context.Context, arg postgres.UpdateEventChallengeOrderParams) error

		WithTransaction(ctx context.Context) (withTx interface{}, commit func(), rollback func(), err error)

		GetTeamsSolvedChallengeInEvent(ctx context.Context, arg postgres.GetTeamsSolvedChallengeInEventParams) ([]postgres.GetTeamsSolvedChallengeInEventRow, error)

		GetChallengeFlag(ctx context.Context, arg postgres.GetChallengeFlagParams) (string, error)
		CreateEventChallengeSolutionAttempt(ctx context.Context, arg postgres.CreateEventChallengeSolutionAttemptParams) error

		CreateEventTeamChallenge(ctx context.Context, arg postgres.CreateEventTeamChallengeParams) error

		AddLabsChallenges(ctx context.Context, labIDs []uuid.UUID, configs []model.LabChallenge) error
	}

	IExerciseService interface {
		GetExercise(ctx context.Context, exerciseID uuid.UUID) (*model.Exercise, error)
	}
)

func (s *EventService) GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]*model.Challenge, error) {
	challenges, err := s.repository.GetEventChallenges(ctx, eventID)
	if err != nil {
		return nil, err
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

func (s *EventService) GetEventChallengeSolvedBy(ctx context.Context, eventID, challengeID uuid.UUID) (*model.ChallengeSoledBy, error) {
	teamSolutions, err := s.repository.GetTeamsSolvedChallengeInEvent(ctx, postgres.GetTeamsSolvedChallengeInEventParams{
		EventID:     eventID,
		ChallengeID: challengeID,
	})
	if err != nil {
		return nil, err
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
	count := len(ch)

	if err != nil {
		return err
	}

	for _, id := range exerciseIDs {
		exercise, err := s.exerciseService.GetExercise(ctx, id)
		if err != nil {
			return err
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

				return err
			}
			count++
		}
	}

	return nil
}

func (s *EventService) CreateEventTeamsChallenges(ctx context.Context, eventID uuid.UUID) error {
	var errs error

	// get all teams in event
	teams, err := s.repository.GetEventTeams(ctx, eventID)
	if err != nil {
		return err
	}

	// get all challenges in event
	challenges, err := s.repository.GetEventChallenges(ctx, eventID)
	if err != nil {
		return err
	}

	// create team challenges
	for _, team := range teams {
		for _, challenge := range challenges {

			// get exercise task
			exercise, err := s.exerciseService.GetExercise(ctx, challenge.ExerciseID)
			if err != nil {
				errs = multierror.Append(errs, err)
				continue
			}

			var flags map[uuid.UUID]string

			for _, task := range exercise.Data.Tasks {
				if task.ID == challenge.ExerciseTaskID {
					flag, err := tools.GetSolutionForTask(task.Flags...)
					if err != nil {
						errs = multierror.Append(errs, err)
						continue
					}

					if err = s.repository.CreateEventTeamChallenge(ctx, postgres.CreateEventTeamChallengeParams{
						ID:          uuid.Must(uuid.NewV7()),
						EventID:     eventID,
						TeamID:      team.ID,
						ChallengeID: challenge.ID,
						Flag:        flag,
					}); err != nil {
						errs = multierror.Append(errs, err)
						continue
					}

					// save flag
					flags[task.ID] = flag
				}
			}
			// create dynamic instances if needed
			if len(exercise.Data.Instances) > 0 {

				insts := make([]model.Instance, 0, len(exercise.Data.Instances))

				for _, instance := range exercise.Data.Instances {
					envs := instance.EnvVars

					// add flag to env vars
					envs = append(envs, model.EnvVar{
						Name:  instance.InstanceFlagVar,
						Value: flags[instance.LinkedTaskID.UUID],
					})

					insts = append(insts, model.Instance{
						ID:    instance.ID,
						Name:  instance.Name,
						Image: instance.Image,
						LinkedTaskID: uuid.NullUUID{
							UUID:  instance.LinkedTaskID.UUID,
							Valid: instance.LinkedTaskID.Valid,
						},
						InstanceFlagVar: instance.InstanceFlagVar,
						EnvVars:         envs,
						DNSRecords:      instance.DNSRecords,
					})
				}

				if err = s.repository.AddLabsChallenges(ctx, []uuid.UUID{team.LaboratoryID.UUID}, []model.LabChallenge{
					{
						ID:        challenge.ID,
						Instances: insts,
					},
				}); err != nil {
					errs = multierror.Append(errs, err)
					continue
				}
			}
		}
	}

	if errs != nil {
		return errs
	}

	return nil
}

func (s *EventService) DeleteEventChallenge(ctx context.Context, eventID uuid.UUID, challengeID uuid.UUID) error {
	if err := s.repository.DeleteEventChallenge(ctx, postgres.DeleteEventChallengeParams{
		EventID: eventID,
		ID:      challengeID,
	}); err != nil {
		return err
	}

	return nil
}

func (s *EventService) UpdateEventChallengesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error {

	for _, order := range orders {
		if err := s.repository.UpdateEventChallengeOrder(ctx, postgres.UpdateEventChallengeOrderParams{
			ID:         order.ID,
			EventID:    eventID,
			OrderIndex: order.Index,
			CategoryID: order.CategoryID,
		}); err != nil {

			return err
		}
	}

	return nil
}

func (s *EventService) SolveChallenge(ctx context.Context, eventID, teamID, challengeID uuid.UUID, solutionAttempt string) (bool, error) {
	// get user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return false, err
	}

	// get challenge flag
	flag, err := s.repository.GetChallengeFlag(ctx, postgres.GetChallengeFlagParams{
		ChallengeID: challengeID,
		TeamID:      teamID,
	})
	if err != nil {
		return false, err
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
		return false, err
	}

	return isCorrect, nil
}