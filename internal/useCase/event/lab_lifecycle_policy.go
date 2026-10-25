package event

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRetentionRepo"
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

// The capabilities are tri-state: reported and present, reported and absent,
// unknown (no report yet, the group's agent cannot be resolved). Only a positive
// report of their absence changes behaviour: such an agent keeps its labs and
// groups running until they are destroyed (finish, withdraw, delete), exactly
// like before the lifecycle work; nothing is dispatched and nothing is reported
// as a failure. Unknown keeps the behaviour with the capabilities (the agent
// client still checks them live before any command), so a failed lookup never
// destroys or settles anything.

// groupStopUnsupported: the group's agent reported no full group stop.
func (u *EventUseCase) groupStopUnsupported(ctx context.Context, group string) bool {
	caps, known := u.labCaps(ctx, group)
	return known && !(caps.FullGroupStop && caps.ConfirmedRuntime)
}

// labStopUnsupported: the lab's agent reported no per-lab stop for the lab's snapshot policy.
func (u *EventUseCase) labStopUnsupported(ctx context.Context, lab eventLabModel.Lab) bool {
	caps, known := u.labCaps(ctx, lab.Ref.Group)
	return known && !(caps.PerLabStop && caps.ConfirmedRuntime && (lab.SnapshotMode != "required" || caps.RequiredSnapshot))
}

// labRestartUnsupported: the group's agent reported no retained restart.
func (u *EventUseCase) labRestartUnsupported(ctx context.Context, group string) bool {
	caps, known := u.labCaps(ctx, group)
	return known && !(caps.RetainedRestart && caps.ConfirmedRuntime)
}

// lifecycleUnsupported: the group's agent reported that it can stop (and so
// retain) nothing of the group at all.
func (u *EventUseCase) lifecycleUnsupported(ctx context.Context, group string) bool {
	caps, known := u.labCaps(ctx, group)
	return known && !(caps.ConfirmedRuntime && (caps.PerLabStop || caps.FullGroupStop))
}

// retentionHoldsGroup reports a group whose destruction waits for its retained
// lifecycle. Retention exists only where the agent can stop and keep labs; on an
// agent that reported none of that, nothing is retained and destruction proceeds.
func (u *EventUseCase) retentionHoldsGroup(ctx context.Context, group string) (bool, error) {
	if !u.lifecycleControls || u.lifecycleUnsupported(ctx, group) {
		return false, nil
	}
	return eventLabRetentionRepo.New(u.repo).OwnedGroup(ctx, group)
}

// settleGoneGroup ends the lifecycle rows of a born object whose group the
// agents positively report as unknown (its owner answered not found, or, with
// no recorded owner, every agent did; destroyed by a cleanup or by hand, its
// team or event deleted), when the agent reported no lifecycle capabilities:
// there is nothing left to stop or observe, so the rows are settled like a group
// the cluster deleted with its reservation instead of being chased with errors.
// An outage or an unknown capability report settles nothing. True when it settled.
func (u *EventUseCase) settleGoneGroup(ctx context.Context, group string, born bool, observeErr error) bool {
	if !u.lifecycleControls || u.uow == nil || !born || !errors.Is(observeErr, infraModel.ErrAgentObjectNotFound) || !u.lifecycleUnsupported(ctx, group) {
		return false
	}
	return u.endLifecyclesWithReservation(ctx, group, time.Now().UTC()) == nil
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
