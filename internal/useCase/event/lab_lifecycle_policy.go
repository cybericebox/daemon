package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRevealRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/gofrs/uuid"
	"time"
)

func (u *EventUseCase) SetLifecycleControls(enabled bool) {
	u.lifecycleControls = enabled
	if enabled {
		u.teamChallenges = teamChallengeRepo.NewWithRevealBarriers(u.repo)
	} else {
		u.teamChallenges = teamChallengeRepo.New(u.repo)
	}
}
func CanManual(mode eventConfigModel.TaskRevealMode, lab eventLabModel.Lab, reachable, retained bool) (stop, restart bool) {
	if !retained {
		return false, false
	}
	return lab.ManualCapabilities(mode == eventConfigModel.RevealAsReady, reachable, time.Now().UTC())
}
func (u *EventUseCase) labCaps(ctx context.Context, group string) (infraModel.LifecycleCapabilities, bool) {
	p, ok := u.infra.(interface {
		LabLifecycleCapabilities(context.Context, string) (infraModel.LifecycleCapabilities, bool)
	})
	if !ok {
		return infraModel.LifecycleCapabilities{}, false
	}
	return p.LabLifecycleCapabilities(ctx, group)
}
func (u *EventUseCase) participantLabCapabilities(ctx context.Context, lab eventLabModel.Lab) (ParticipantLabView, error) {
	out := participantLabView(lab)
	if !u.lifecycleControls {
		return out, nil
	}
	cfg, err := u.configs.Get(ctx, lab.EventID)
	if err != nil {
		return out, err
	}
	e, err := u.events.GetByID(ctx, lab.EventID)
	if err != nil {
		return out, err
	}
	reachable, err := eventLabRevealRepo.New(u.repo).Reachable(ctx, lab.ID, time.Now().UTC())
	if err != nil {
		return out, err
	}
	caps, known := u.labCaps(ctx, lab.Ref.Group)
	if !known {
		return out, nil
	}
	stop, restart := lab.ManualCapabilities(cfg.TaskRevealMode == eventConfigModel.RevealAsReady, reachable && e.Lifecycle.RuntimeOpen(time.Now().UTC()), time.Now().UTC())
	out.CanStop = stop && lab.ReadyForAccess() && caps.PerLabStop && caps.ConfirmedRuntime && (lab.SnapshotMode != "required" || caps.RequiredSnapshot)
	out.CanRestart = restart && caps.RetainedRestart && caps.ConfirmedRuntime
	if out.CanRestart {
		candidate := lab.RestartBudgetCandidate()
		out.CanRestart = u.checkLabCandidateBudget(ctx, u.repo, candidate, cfg, time.Now().UTC()) == nil
	}
	return out, nil
}

func (u *EventUseCase) labsForBudget(ctx context.Context, eventID uuid.UUID) ([]eventLabModel.Lab, error) {
	return eventLabAllocationRepo.New(u.repo).Labs(ctx, eventID)
}
