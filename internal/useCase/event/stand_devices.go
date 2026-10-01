package event

import (
	"context"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
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
	Labs                  []StandLabDetailView
}

type StandLabDetailView struct {
	ChallengeID   uuid.UUID
	ChallengeName string
	Readiness     labBindingModel.Readiness
	Reason        string
	// Live is the agent's status of the Lab; nil while the Lab is not deployed yet or the agent
	// cannot answer (LiveUnavailable).
	Live            *exerciseModel.LabDeployStatus
	LiveUnavailable bool
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
	for _, lab := range labs {
		if lab.TeamID != teamID {
			continue
		}
		item := StandLabDetailView{ChallengeID: lab.ChallengeID, ChallengeName: lab.ChallengeName, Readiness: lab.Readiness, Reason: lab.FailureReason}
		if binding, found := byChallenge[lab.ChallengeID]; found && binding.DeployedAt != nil && usable {
			status, statusErr := u.infra.LabStatus(ctx, binding.LabGroupName, binding.LabName)
			if statusErr != nil {
				item.LiveUnavailable = true
			} else {
				item.Live = &status
			}
		}
		view.Labs = append(view.Labs, item)
	}
	return view, nil
}

// ResetStandDevice discards the snapshots of one device of a team Lab and restarts it from its
// base image. The route gates admit organizers and administrators only.
func (u *EventUseCase) ResetStandDevice(ctx context.Context, eventID, teamID, challengeID uuid.UUID, device string) error {
	group, lab, controller, err := u.standDeviceTarget(ctx, eventID, teamID, challengeID)
	if err != nil {
		return err
	}
	return controller.ResetDevice(ctx, group, lab, device)
}

// RescueStandDevice starts one device of a team Lab from its latest snapshot with a shell
// (enable) or back to its normal start.
func (u *EventUseCase) RescueStandDevice(ctx context.Context, eventID, teamID, challengeID uuid.UUID, device string, enable bool) error {
	group, lab, controller, err := u.standDeviceTarget(ctx, eventID, teamID, challengeID)
	if err != nil {
		return err
	}
	return controller.RescueDevice(ctx, group, lab, device, enable)
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
