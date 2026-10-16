package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabGroupRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"github.com/gofrs/uuid"
	"time"
)

// Only the group's current VPN/network acknowledgement matters here. Challenge
// labs may still be queued or absent; downloading never starts a group.
func (u *EventUseCase) vpnGroupReady(ctx context.Context, eventID, teamID uuid.UUID) (bool, error) {
	group, err := eventLabGroupRepo.New(u.repo).Get(ctx, teamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return false, nil
		}
		return false, model.ErrPlatform.WithError(err).WithMessage("Failed to read VPN group readiness").Err()
	}
	name, err := labBindingModel.GroupName(eventID, teamID)
	if err != nil {
		return false, err
	}
	return group.EventID == eventID && group.TeamID == teamID && group.Name == name && group.AllowsChildren(time.Now()), nil
}

func (u *EventUseCase) requireReadyVPNGroup(ctx context.Context, eventID, teamID uuid.UUID) error {
	ready, err := u.vpnGroupReady(ctx, eventID, teamID)
	if err != nil {
		return err
	}
	if !ready {
		return eventStandModel.ErrStandNotDeployed.Err()
	}
	return nil
}

func (u *EventUseCase) GetModeratorsVPNProbeStatus(ctx context.Context, eventID, userID uuid.UUID) (VPNProbeStatusView, error) {
	if _, err := u.requireInfrastructureEvent(ctx, eventID); err != nil {
		return VPNProbeStatusView{}, err
	}
	team, err := u.stands.GetModeratorsTeam(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return VPNProbeStatusView{}, eventStandModel.ErrStandModeratorsTeamUnavailable.Err()
		}
		return VPNProbeStatusView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get moderators team").Err()
	}
	if err := u.requireReadyVPNGroup(ctx, eventID, team.ID); err != nil {
		return VPNProbeStatusView{}, err
	}
	group, err := labBindingModel.GroupName(eventID, team.ID)
	if err != nil {
		return VPNProbeStatusView{}, err
	}
	view, err := u.vpnProbeStatus(ctx, group)
	if err == nil {
		view.Connected = u.ownVPNConnection(ctx, eventID, userID, group)
	}
	return view, err
}
