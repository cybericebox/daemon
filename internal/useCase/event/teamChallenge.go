package event

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	laboratoryModel "github.com/cybericebox/daemon/internal/model/laboratory"
)

type (
	ITeamChallengeService interface {
		GetExercise(ctx context.Context, exerciseID uuid.UUID) (*exerciseModel.Exercise, error)
		GetEventTeams(ctx context.Context, eventID uuid.UUID, page, pageSize int) ([]*eventModel.Team, error)
		GetEventChallengeFlag(ctx context.Context, challengeID, teamID uuid.UUID, flags []string) (string, error)

		CreateEventTeamChallenges(ctx context.Context, teamChallenges []eventModel.TeamChallenge) error

		AddLaboratoriesChallenges(
			ctx context.Context,
			labsGroupID uuid.UUID,
			labIDs []uuid.UUID,
			configs []laboratoryModel.LaboratoryChallenge,
			flagEnvVars []laboratoryModel.FlagVariable,
		) error
		DeleteLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) error
		DeleteLaboratoriesChallenges(
			ctx context.Context,
			labsGroupID uuid.UUID,
			labIDs []uuid.UUID,
			challengeIDs []uuid.UUID,
		) error
	}
)

func (u *EventUseCase) CreateEventTeamsChallenges(ctx context.Context, eventID uuid.UUID) error {
	teams, err := u.service.GetEventTeams(ctx, eventID, config.AllPages, 0)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event teams").WithContext(
			"eventID",
			eventID.String(),
		).Err()
	}

	challenges, err := u.service.GetEventChallenges(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event challenges").WithContext(
			"eventID",
			eventID.String(),
		).Err()
	}

	teamChallenges := make([]eventModel.TeamChallenge, 0)
	var errs error
	hasInstances := false

	for _, team := range teams {
		// map[taskID]flag
		taskFlags := make(map[uuid.UUID]string)
		// map[exerciseID][]instance
		exercisesInstances := make(map[uuid.UUID][]exerciseModel.Instance)
	chLoop:
		for _, challenge := range challenges {
			// get challenge exercise
			exercise, err := u.service.GetExercise(ctx, challenge.ExerciseID)
			if err != nil {
				errs = multierror.Append(
					errs,
					model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise by id").WithContext(
						"exerciseID",
						challenge.ExerciseID.String(),
					).Err(),
				)
				continue chLoop
			}

			// if exercise has instances save them
			if _, ok := exercisesInstances[challenge.ExerciseID]; !ok {
				exercisesInstances[challenge.ExerciseID] = make([]exerciseModel.Instance, 0)
			}

			if len(exercise.Data.Instances) > 0 {
				for _, instance := range exercise.Data.Instances {
					exercisesInstances[challenge.ExerciseID] = append(
						exercisesInstances[challenge.ExerciseID], exerciseModel.Instance{
							ID:              instance.ID,
							Name:            instance.Name,
							Image:           instance.Image,
							LinkedTaskID:    instance.LinkedTaskID,
							InstanceFlagVar: instance.InstanceFlagVar,
							EnvVars:         instance.EnvVars,
							DNSRecords:      instance.DNSRecords,
						},
					)
				}
			}

			// find task for challenge
			for _, task := range exercise.Data.Tasks {
				if task.ID == challenge.ExerciseTaskID {
					// try to get team challenge
					flag, err := u.service.GetEventChallengeFlag(ctx, challenge.ID, team.ID, task.Flags)
					if err != nil {
						errs = multierror.Append(
							errs,
							model.ErrPlatform.WithError(err).WithMessage("Failed to get challenge flag").WithContext(
								"challengeID",
								challenge.ID.String(),
							).WithContext("teamID", team.ID.String()).Err(),
						)
						continue chLoop
					}

					// save flag
					taskFlags[task.ID] = flag

					teamChallenges = append(
						teamChallenges, eventModel.TeamChallenge{
							TeamID:      team.ID,
							ChallengeID: challenge.ID,
							Flag:        flag,
						},
					)

					break
				}
			}

		}
		labChallenges := make([]laboratoryModel.LaboratoryChallenge, 0)
		flagEnvVars := make([]laboratoryModel.FlagVariable, 0)

		for exerciseID, instances := range exercisesInstances {
			if len(instances) > 0 {
				hasInstances = true
			}
			for index, inst := range instances {
				// if instance has flag var add it to envs
				if inst.LinkedTaskID.Valid {
					// get instance envs
					envs := inst.EnvVars
					// set updated envs to instance
					exercisesInstances[exerciseID][index].EnvVars = envs

					flagEnvVars = append(
						flagEnvVars, laboratoryModel.FlagVariable{
							LabID:       team.LaboratoryID.UUID,
							ChallengeID: exerciseID,
							InstanceID:  inst.ID,
							Flag:        taskFlags[inst.LinkedTaskID.UUID],
							Variable:    inst.InstanceFlagVar,
						},
					)
				}
			}

			labChallenges = append(
				labChallenges, laboratoryModel.LaboratoryChallenge{
					ID:        exerciseID,
					Instances: instances,
				},
			)
		}
		if hasInstances {
			if err = u.service.AddLaboratoriesChallenges(
				ctx,
				uuid.Nil,
				[]uuid.UUID{team.LaboratoryID.UUID},
				labChallenges,
				flagEnvVars,
			); err != nil {
				errs = multierror.Append(
					errs,
					model.ErrPlatform.WithError(err).WithMessage("Failed to add lab challenges").WithContext(
						"labID",
						team.LaboratoryID.UUID.String(),
					).Err(),
				)
			}
		}
	}

	if err = u.service.CreateEventTeamChallenges(ctx, teamChallenges); err != nil {
		errs = multierror.Append(
			errs,
			model.ErrPlatform.WithError(err).WithMessage("Failed to create team challenges").Err(),
		)
	}

	if errs != nil {
		return model.ErrPlatform.WithError(errs).WithMessage("Failed to create team challenges").Err()
	}

	return nil
}

func (u *EventUseCase) DeleteEventTeamsChallengesInfrastructure(ctx context.Context, eventID uuid.UUID) error {
	teams, err := u.service.GetEventTeams(ctx, eventID, config.AllPages, 0)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event teams").WithContext(
			"eventID",
			eventID.String(),
		).Err()
	}

	labIDs := make([]uuid.UUID, 0)
	for _, team := range teams {
		labIDs = append(labIDs, team.LaboratoryID.UUID)
	}

	if len(labIDs) == 0 {
		return nil
	}

	if err = u.service.DeleteLaboratories(ctx, uuid.Nil, labIDs); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete laboratories").Err()
	}

	return nil
}

func (u *EventUseCase) DeleteEventTeamsChallengeInfrastructureByExerciseID(
	ctx context.Context,
	eventID, exerciseID uuid.UUID,
) error {
	exercise, err := u.service.GetExercise(ctx, exerciseID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise by id").WithContext(
			"exerciseID",
			exerciseID.String(),
		).Err()
	}

	if len(exercise.Data.Instances) == 0 {
		return nil
	}

	teams, err := u.service.GetEventTeams(ctx, eventID, config.AllPages, 0)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event teams").WithContext(
			"eventID",
			eventID.String(),
		).Err()
	}

	labIDs := make([]uuid.UUID, 0)
	for _, team := range teams {
		labIDs = append(labIDs, team.LaboratoryID.UUID)
	}

	if len(labIDs) == 0 {
		return nil
	}

	if err = u.service.DeleteLaboratoriesChallenges(ctx, uuid.Nil, labIDs, []uuid.UUID{exerciseID}); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete laboratories challenges").Err()
	}

	return nil
}
