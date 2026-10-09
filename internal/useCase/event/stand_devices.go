package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"sort"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

// StandDetailView is one team stand with the live state of each of its Labs: launch queue,
// devices (with their snapshot state) and image warnings. It is read for organizers and
// administrators, never for participants.
type StandDetailView struct {
	TeamID                uuid.UUID
	TeamName              string
	Moderators            bool
	Status                eventStandModel.Status
	Reason                string
	Generation            int32
	LaboratoriesAvailable bool
	Group                 ManagedGroupView
	Labs                  []StandLabDetailView
}

type StandLabDetailView struct {
	Lab           *ManagedLabView
	Questions     []StandLabQuestionView
	ChallengeID   uuid.UUID
	ChallengeName string
	Readiness     labBindingModel.Readiness
	Reason        string
	// Live is the agent's status of the Lab; nil while the Lab is not deployed yet or the agent
	// cannot answer (LiveUnavailable).
	Live            *exerciseModel.LabDeployStatus
	LiveUnavailable bool
}

type StandLabQuestionView struct {
	EventChallengeID uuid.UUID `json:"EventChallengeID"`
	Name             string    `json:"Name"`
}

// GetTeamStandDetail reads the stand of one team (the moderators team included) with the live
// agent state of every deployed Lab. A Lab the agent cannot answer for does not fail the read.
func (u *EventUseCase) GetTeamStandDetail(ctx context.Context, eventID, teamID uuid.UUID) (StandDetailView, error) {
	if _, err := u.requireInfrastructureEvent(ctx, eventID); err != nil {
		return StandDetailView{}, err
	}
	team, err := u.standTeam(ctx, eventID, teamID)
	if err != nil {
		return StandDetailView{}, err
	}
	labs, err := u.stands.ListLabs(ctx, eventID)
	if err != nil {
		return StandDetailView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list stand labs").Err()
	}
	bindings, err := u.labBindings.ListTeamLive(ctx, teamID)
	if err != nil {
		return StandDetailView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list team stand labs").Err()
	}
	byChallenge := make(map[uuid.UUID]labBindingModel.Binding, len(bindings))
	for _, binding := range bindings {
		byChallenge[binding.EventChallengeID] = binding
	}
	usable := u.laboratoriesUsable(ctx)
	view := StandDetailView{
		TeamID: team.TeamID, TeamName: team.PublicName, Moderators: team.Moderators, Status: team.Status,
		Reason: team.Reason, Generation: team.Generation, LaboratoriesAvailable: usable, Labs: []StandLabDetailView{},
	}
	if team.Moderators {
		// The stored name is technical; clients label the moderators team.
		view.TeamName = ""
	}
	index := map[uuid.UUID]int{}
	for _, lab := range labs {
		if lab.TeamID != teamID {
			continue
		}
		item := StandLabDetailView{Questions: []StandLabQuestionView{{EventChallengeID: lab.ChallengeID, Name: lab.ChallengeName}}, ChallengeID: lab.ChallengeID, ChallengeName: lab.ChallengeName, Readiness: lab.Readiness, Reason: lab.FailureReason}
		binding, found := byChallenge[lab.ChallengeID]
		if found && binding.LabID.Valid {
			if at, seen := index[binding.LabID.UUID]; seen {
				view.Labs[at].Questions = append(view.Labs[at].Questions, item.Questions...)
				continue
			}
			canonical, getErr := u.labs.Get(ctx, binding.LabID.UUID)
			if getErr != nil {
				return StandDetailView{}, model.ErrPlatform.WithError(getErr).WithMessage("Failed to read managed laboratory lifecycle").Err()
			}
			set, getErr := u.eventExercises.GetByID(ctx, eventID, canonical.EventExerciseID)
			if getErr != nil {
				return StandDetailView{}, getErr
			}
			exercise, getErr := u.exercises.GetByID(ctx, set.ExerciseID)
			if getErr != nil {
				return StandDetailView{}, getErr
			}
			managed := managedLabView(canonical, exercise.Name)
			item.Lab = &managed
			index[canonical.ID] = len(view.Labs)
		}
		if found && binding.DeployedAt != nil && usable && (item.Lab == nil || item.Lab.ClosedAt == nil) {
			status, statusErr := u.infra.LabStatus(ctx, binding.LabGroupName, binding.LabName)
			if statusErr != nil {
				item.LiveUnavailable = true
			} else {
				item.Live = &status
			}
		}
		view.Labs = append(view.Labs, item)
	}
	for i := range view.Labs {
		row := &view.Labs[i]
		sort.Slice(row.Questions, func(a, b int) bool {
			return row.Questions[a].EventChallengeID.String() < row.Questions[b].EventChallengeID.String()
		})
		if len(row.Questions) > 0 {
			row.ChallengeID = row.Questions[0].EventChallengeID
			row.ChallengeName = row.Questions[0].Name
		}
	}
	group, _ := labBindingModel.GroupName(eventID, teamID)
	view.Group = ManagedGroupView{Name: group, Revision: "0", ObservedRevision: "0", DesiredState: "Running", ActualState: "Unknown", Resources: allocationView(eventLabModel.Allocation{RuntimeState: "Unknown", StorageState: "Unknown"})}
	if observation, readErr := u.observations.Group(ctx, eventID, teamID, group); readErr == nil {
		view.Group = managedGroupView(observation)
	} else if !repositoryTools.IsObjectNotFoundError(readErr) {
		return StandDetailView{}, model.ErrPlatform.WithError(readErr).WithMessage("Failed to read managed group lifecycle").Err()
	}
	return view, nil
}

// standDeviceOperationTimeout bounds one command submission, including its
// preflight and admission/Lab lock wait. Like the existing agent enrollment
// bound, 30s covers one control RPC; it never waits the 20m deploy timeout while
// holding a team's closure locks. A shorter caller deadline/cancellation wins.
const standDeviceOperationTimeout = 30 * time.Second

// ResetStandDevice discards the snapshots of one device of a team Lab and restarts it from its
// base image. The route gates admit organizers and administrators only.
func (u *EventUseCase) ResetStandDevice(ctx context.Context, eventID, teamID, challengeID uuid.UUID, device string) error {
	ctx, cancel := context.WithTimeout(ctx, standDeviceOperationTimeout)
	defer cancel()
	group, lab, controller, err := u.standDeviceTarget(ctx, eventID, teamID, challengeID)
	if err != nil {
		return err
	}
	return u.mutateStandDevice(ctx, eventID, teamID, challengeID, group, lab, func(ctx context.Context) error { return controller.ResetDevice(ctx, group, lab, device) })
}

// RescueStandDevice starts one device of a team Lab from its latest snapshot with a shell
// (enable) or back to its normal start.
func (u *EventUseCase) RescueStandDevice(ctx context.Context, eventID, teamID, challengeID uuid.UUID, device string, enable bool) error {
	ctx, cancel := context.WithTimeout(ctx, standDeviceOperationTimeout)
	defer cancel()
	group, lab, controller, err := u.standDeviceTarget(ctx, eventID, teamID, challengeID)
	if err != nil {
		return err
	}
	return u.mutateStandDevice(ctx, eventID, teamID, challengeID, group, lab, func(ctx context.Context) error { return controller.RescueDevice(ctx, group, lab, device, enable) })
}

// standDeviceTarget resolves the deployed Lab of one team challenge and the agent capability.
func (u *EventUseCase) standDeviceTarget(ctx context.Context, eventID, teamID, challengeID uuid.UUID) (group, lab string, controller infraModel.DeviceController, err error) {
	if _, err = u.requireInfrastructureEvent(ctx, eventID); err != nil {
		return "", "", nil, err
	}
	if _, err = u.standTeam(ctx, eventID, teamID); err != nil {
		return "", "", nil, err
	}
	binding, err := u.labBindings.Get(ctx, teamID, challengeID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return "", "", nil, eventStandModel.ErrStandLabNotFound.Err()
		}
		return "", "", nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get lab binding").Err()
	}
	if binding.EventID != eventID || binding.DeployedAt == nil || binding.Readiness == labBindingModel.ReadinessDestroyed {
		return "", "", nil, eventStandModel.ErrStandLabNotFound.Err()
	}
	if !u.laboratoriesUsable(ctx) {
		return "", "", nil, infraUnavailable()
	}
	controller, ok := u.infra.(infraModel.DeviceController)
	if !ok {
		return "", "", nil, infraUnavailable()
	}
	return binding.LabGroupName, binding.LabName, controller, nil
}

// Device mutations serialize with final solves on the canonical admission/Lab
// locks. A queued write rechecks closure after taking those locks; an already
// admitted write finishes before the logical close commits. The legacy RPC has
// no revision target, so releasing this guard before the mutation would reopen
// the solve-vs-reset race.
func (u *EventUseCase) mutateStandDevice(ctx context.Context, eventID, teamID, challengeID uuid.UUID, group, lab string, mutate func(context.Context) error) error {
	binding, err := u.labBindings.Get(ctx, teamID, challengeID)
	if err != nil {
		return err
	}
	if !binding.LabID.Valid {
		return mutate(ctx)
	}
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, repo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	labs := eventLabRepo.New(repo)
	if err = labs.LockAdmission(txCtx, teamID); err != nil {
		return err
	}
	currentBinding, err := labBindingRepo.New(repo).Get(txCtx, teamID, challengeID)
	if err != nil {
		return err
	}
	if currentBinding.EventID != eventID || currentBinding.LabID != binding.LabID || currentBinding.LabGroupName != group || currentBinding.LabName != lab {
		return eventStandModel.ErrStandLabNotFound.Err()
	}
	canonical, err := labs.Lock(txCtx, binding.LabID.UUID)
	if err != nil {
		return err
	}
	if err = requireLabOpen(canonical); err != nil {
		return err
	}
	if err = mutate(txCtx); err != nil {
		return err
	}
	return unit.Save()
}
