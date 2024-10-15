package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"
)

type (
	ITeamChallengeService interface {
		GetExercise(ctx context.Context, exerciseID uuid.UUID) (*model.Exercise, error)
		GetEventTeams(ctx context.Context, eventID uuid.UUID) ([]*model.Team, error)
		GetChallengeFlag(ctx context.Context, challengeID, teamID uuid.UUID, flags []string) (string, error)

		CreateTeamChallenges(ctx context.Context, teamChallenges []model.TeamChallenge) error

		AddLaboratoryChallenges(ctx context.Context, labID uuid.UUID, configs []model.LaboratoryChallenge) error
		DeleteLaboratories(ctx context.Context, labIDs ...uuid.UUID) error
		DeleteLaboratoriesChallenges(ctx context.Context, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error
	}
)

func (u *EventUseCase) CreateEventTeamsChallenges(ctx context.Context, eventID uuid.UUID) error {
	teams, err := u.service.GetEventTeams(ctx, eventID)
	if err != nil {
		return model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to get event teams").WithContext("eventID", eventID.String()).Cause()
	}

	challenges, err := u.service.GetEventChallenges(ctx, eventID)
	if err != nil {
		return model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to get event challenges").WithContext("eventID", eventID.String()).Cause()
	}

	teamChallenges := make([]model.TeamChallenge, 0)
	var errs error
	hasInstances := false

	for _, team := range teams {
		// map[taskID]flag
		taskFlags := make(map[uuid.UUID]string)
		// map[exerciseID][]instance
		exercisesInstances := make(map[uuid.UUID][]model.Instance)
	chLoop:
		for _, challenge := range challenges {
			// get challenge exercise
			exercise, err := u.service.GetExercise(ctx, challenge.ExerciseID)
			if err != nil {
				errs = multierror.Append(errs, model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to get exercise by id").WithContext("exerciseID", challenge.ExerciseID.String()).Cause())
				continue chLoop
			}

			// if exercise has instances save them
			if _, ok := exercisesInstances[challenge.ExerciseID]; !ok {
				exercisesInstances[challenge.ExerciseID] = make([]model.Instance, 0)
			}

			if len(exercise.Data.Instances) > 0 {
				for _, instance := range exercise.Data.Instances {
					exercisesInstances[challenge.ExerciseID] = append(exercisesInstances[challenge.ExerciseID], model.Instance{
						ID:              instance.ID,
						Name:            instance.Name,
						Image:           instance.Image,
						LinkedTaskID:    instance.LinkedTaskID,
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
					flag, err := u.service.GetChallengeFlag(ctx, challenge.ID, team.ID, task.Flags)
					if err != nil {
						errs = multierror.Append(errs, model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to get challenge flag").WithContext("challengeID", challenge.ID.String()).WithContext("teamID", team.ID.String()).Cause())
						continue chLoop
					}

					// save flag
					taskFlags[task.ID] = flag

					teamChallenges = append(teamChallenges, model.TeamChallenge{
						EventID:     eventID,
						TeamID:      team.ID,
						ChallengeID: challenge.ID,
						Flag:        flag,
					})

					break
				}
			}

		}
		labChallenges := make([]model.LaboratoryChallenge, 0)

		for exerciseID, instances := range exercisesInstances {
			if len(instances) > 0 {
				hasInstances = true
			}
			for index, inst := range instances {
				// if instance has flag var add it to envs
				if inst.LinkedTaskID.Valid {
					// get instance envs
					envs := inst.EnvVars
					// add flag to envs
					envs = append(envs, model.EnvVar{
						Name:  inst.InstanceFlagVar,
						Value: taskFlags[inst.LinkedTaskID.UUID],
					})
					// set updated envs to instance
					exercisesInstances[exerciseID][index].EnvVars = envs
				}
			}

			labChallenges = append(labChallenges, model.LaboratoryChallenge{
				ID:        exerciseID,
				Instances: instances,
			})
		}
		if hasInstances {
			if err = u.service.AddLaboratoryChallenges(ctx, team.LaboratoryID.UUID, labChallenges); err != nil {
				errs = multierror.Append(errs, model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to add lab challenges").WithContext("labID", team.LaboratoryID.UUID.String()).Cause())
			}
		}
	}

	if err = u.service.CreateTeamChallenges(ctx, teamChallenges); err != nil {
		errs = multierror.Append(errs, model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to create team challenges").Cause())
	}

	if errs != nil {
		return model.ErrEventTeamChallenge.WithError(errs).WithMessage("Failed to create team challenges").Cause()
	}

	return nil
}

func (u *EventUseCase) DeleteEventTeamsChallengesInfrastructure(ctx context.Context, eventID uuid.UUID) error {
	teams, err := u.service.GetEventTeams(ctx, eventID)
	if err != nil {
		return model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to get event teams").WithContext("eventID", eventID.String()).Cause()
	}

	labIDs := make([]uuid.UUID, 0)
	for _, team := range teams {
		labIDs = append(labIDs, team.LaboratoryID.UUID)
	}

	if err = u.service.DeleteLaboratories(ctx, labIDs...); err != nil {
		return model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to delete laboratories").Cause()
	}

	return nil
}

func (u *EventUseCase) DeleteEventTeamsChallengeInfrastructureByExerciseID(ctx context.Context, eventID, exerciseID uuid.UUID) error {
	exercise, err := u.service.GetExercise(ctx, exerciseID)
	if err != nil {
		return model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to get exercise by id").WithContext("exerciseID", exerciseID.String()).Cause()
	}

	if len(exercise.Data.Instances) == 0 {
		return nil
	}

	teams, err := u.service.GetEventTeams(ctx, eventID)
	if err != nil {
		return model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to get event teams").WithContext("eventID", eventID.String()).Cause()
	}

	labIDs := make([]uuid.UUID, 0)
	for _, team := range teams {
		labIDs = append(labIDs, team.LaboratoryID.UUID)
	}

	if err = u.service.DeleteLaboratoriesChallenges(ctx, labIDs, []uuid.UUID{exerciseID}); err != nil {
		return model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to delete laboratories challenges").Cause()
	}

	return nil
}
