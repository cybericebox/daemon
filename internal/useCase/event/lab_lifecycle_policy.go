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
	out.CanStop = stop && caps.PerLabStop && caps.ConfirmedRuntime && (lab.SnapshotMode != "required" || caps.RequiredSnapshot)
	out.CanRestart = restart && caps.RetainedRestart && caps.ConfirmedRuntime
	if out.CanRestart {
		rows, e := u.labsForBudget(ctx, lab.EventID)
		if e != nil {
			return out, e
		}
		active := int32(0)
		for _, other := range rows {
			if other.ID != lab.ID && other.TeamID == lab.TeamID && other.HoldsRuntime() {
				active++
			}
		}
		if limit := cfg.EffectiveLabPolicy().MaxActiveLabsPerTeam; limit != nil && active >= *limit {
			out.CanRestart = false
			return out, nil
		}
		totals, err := u.labResourceTotals(ctx, lab.EventID, time.Now().UTC())
		if err != nil {
			return out, err
		}
		budget, err := u.allocationBudget(ctx, lab.EventID)
		if err != nil {
			out.CanRestart = false
			return out, nil
		}
		need := lab.Allocation.ConfiguredRequests
		out.CanRestart = totals.Held.CPUMillicores+need.CPUMillicores <= budget.Size.CPUMillicores && totals.Held.MemoryBytes+need.MemoryBytes <= budget.Size.MemoryBytes && totals.Storage.SnapshotQuotaBytes <= budget.SizeSnapshotQuotaBytes
	}
	return out, nil
}

func (u *EventUseCase) labsForBudget(ctx context.Context, eventID uuid.UUID) ([]eventLabModel.Lab, error) {
	return eventLabAllocationRepo.New(u.repo).Labs(ctx, eventID)
}
