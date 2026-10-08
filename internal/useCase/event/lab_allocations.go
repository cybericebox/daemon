package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	"github.com/gofrs/uuid"
	"time"
)

// SetAllocationAccounting enables production ledger admission/reporting. Narrow
// legacy fixtures without the capacity dependency do not exercise admission.
func (u *EventUseCase) SetAllocationAccounting(enabled bool) { u.allocationAccounting = enabled }
func (u *EventUseCase) HeldCompute(ctx context.Context, eventID uuid.UUID) (eventLabModel.Compute, error) {
	v, err := u.labResourceTotals(ctx, eventID, time.Now())
	return v.Held, err
}
func (u *EventUseCase) HeldStorage(ctx context.Context, eventID uuid.UUID) (eventLabModel.StorageBudget, error) {
	v, err := u.labResourceTotals(ctx, eventID, time.Now())
	return v.Storage, err
}
func (u *EventUseCase) labResourceTotals(ctx context.Context, eventID uuid.UUID, now time.Time) (eventLabModel.ResourceTotals, error) {
	r := eventLabAllocationRepo.New(u.repo)
	labs, err := r.Labs(ctx, eventID)
	if err != nil {
		return eventLabModel.ResourceTotals{}, err
	}
	groups, err := r.Groups(ctx, eventID)
	if err != nil {
		return eventLabModel.ResourceTotals{}, err
	}
	unknown, e := r.UnaccountedStarts(ctx)
	if e != nil {
		return eventLabModel.ResourceTotals{}, e
	}
	out := eventLabModel.ResourceTotals{Complete: !unknown[eventID], Storage: eventLabModel.StorageBudget{PhysicalKnown: true}}
	for _, l := range labs {
		out.AddLab(l, now)
	}
	for _, g := range groups {
		held := g.Lifecycle.HeldCompute()
		total := resourcesModel.Amount{CPUMillicores: held.CPUMillicores, MemoryBytes: held.MemoryBytes}
		out.GroupServices.CPUMillicores += total.CPUMillicores
		out.GroupServices.MemoryBytes += total.MemoryBytes
		out.Held.CPUMillicores += total.CPUMillicores
		out.Held.MemoryBytes += total.MemoryBytes
		observation, e := u.observations.Group(ctx, eventID, g.TeamID, g.Name)
		if e != nil || observation.UID == "" || observation.Revision <= 0 || observation.ObservedRevision != observation.Revision || observation.ObservedAt == nil || observation.ObservedAt.After(now) || now.Sub(*observation.ObservedAt) > 30*time.Second || observation.Allocation.RuntimeState != "Allocated" {
			out.Complete = false
		}
		if observation.ObservedAt != nil && (out.ObservedAt == nil || observation.ObservedAt.Before(*out.ObservedAt)) {
			at := *observation.ObservedAt
			out.ObservedAt = &at
		}
	}
	if len(labs) == 0 && len(groups) == 0 {
		out.Complete = false
		out.Storage.PhysicalKnown = false
	}
	return out, nil
}

// AdmitEventLabStart owns one atomic event budget decision. Placement/queueing
// remains independent and never makes this admission a readiness certificate.
func (u *EventUseCase) AdmitEventLabStart(ctx context.Context, eventID, teamID, labID uuid.UUID, need eventLabModel.Compute, storageBytes int64) error {
	if labID == uuid.Nil {
		return calModel.ErrNotEnoughReserved.Err()
	}
	return u.admitEventAllocation(ctx, eventID, teamID, labID, need, storageBytes)
}
func (u *EventUseCase) admitGroupAllocation(ctx context.Context, eventID, teamID uuid.UUID) error {
	if !u.allocationAccounting {
		return nil
	}
	return u.admitEventAllocation(ctx, eventID, teamID, uuid.Nil, eventLabModel.Compute{}, 0)
}
func (u *EventUseCase) admitEventAllocation(ctx context.Context, eventID, teamID, labID uuid.UUID, need eventLabModel.Compute, storageBytes int64) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Laboratory allocation transaction is not configured").Err()
	}
	txCtx, q, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	// Event budget precedes the established team -> Lab lock order. Concurrent
	// admissions cannot both claim the same remaining event reservation.
	if _, err = q.LockEventForLabSourceChange(txCtx, eventID); err != nil {
		return err
	}
	if err = q.LockResourceCalendar(txCtx); err != nil {
		return err
	}
	if _, err = q.LockEventConfigForLabSizing(txCtx, eventID); err != nil {
		return err
	}
	planning := NewEventUseCase(Dependencies{Repo: q, Infra: u.infra, Resources: u.resources})
	planning.allocationAccounting = u.allocationAccounting
	inputs, err := planning.resourcePlanInputs(txCtx, eventID)
	if err != nil {
		return err
	}
	placement := inputs.placementNeed()
	planner, ok := u.infra.(resourcePlanner)
	if !ok {
		return calModel.ErrNotEnoughReserved.Err()
	}
	if planner.NeedFit(placement) != nil {
		return calModel.ErrNotEnoughReserved.Err()
	}
	sizes, known := planner.GroupSizes(placement.Plan)
	if !known || sizes.VPN.CPUMillicores <= 0 || sizes.VPN.MemoryBytes <= 0 || sizes.Gateway.CPUMillicores <= 0 || sizes.Gateway.MemoryBytes <= 0 {
		return calModel.ErrNotEnoughReserved.Err()
	}
	if err = eventLabRepo.New(q).LockAdmission(txCtx, teamID); err != nil {
		return err
	}

	groupName, err := labBindingModel.GroupName(eventID, teamID)
	if err != nil {
		return err
	}
	lab := eventLabModel.Lab{Ref: eventLabModel.Ref{Group: groupName}}
	if labID != uuid.Nil {
		lab, err = eventLabRepo.New(q).Lock(txCtx, labID)
		if err != nil {
			return err
		}
		if lab.EventID != eventID || lab.TeamID != teamID {
			return calModel.ErrNotEnoughReserved.Err()
		}
	}
	before := lab.Allocation
	knownDemand := true
	if labID != uuid.Nil && need == (eventLabModel.Compute{}) {
		knownDemand, err = u.knownZeroDemand(txCtx, q, lab)
		if err != nil {
			return err
		}
	}
	if labID != uuid.Nil && ((!knownDemand) || (!lab.Admit(need, storageBytes, time.Now().UTC()) && !lab.AdmitKnown(need, storageBytes, lab.DefinitionHash, lab.Generation, time.Now().UTC()))) {
		return calModel.ErrNotEnoughReserved.Err()
	}
	r := eventLabAllocationRepo.New(q)
	unknown, e := r.UnaccountedStarts(txCtx)
	if e != nil {
		return e
	}
	if unknown[eventID] {
		return calModel.ErrNotEnoughReserved.Err()
	}
	labs, err := r.Labs(txCtx, eventID)
	if err != nil {
		return err
	}
	groups, err := r.Groups(txCtx, eventID)
	if err != nil {
		return err
	}
	var held, teamHeld eventLabModel.Compute
	var storage int64
	config, err := eventConfigRepo.New(q).Get(txCtx, eventID)
	if err != nil {
		return err
	}
	active := int32(0)
	for _, l := range labs {
		if l.TeamID == teamID && l.ID != labID && l.HoldsRuntime() {
			active++
		}
	}
	if labID != uuid.Nil && config.LabPolicy.MaxActiveLabsPerTeam != nil && active >= *config.LabPolicy.MaxActiveLabsPerTeam {
		return calModel.ErrNotEnoughReserved.Err()
	}
	foundGroup := false
	for _, l := range labs {
		if l.ID == lab.ID {
			l = lab
		}
		c := l.HeldCompute()
		held.CPUMillicores += c.CPUMillicores
		held.MemoryBytes += c.MemoryBytes
		if l.TeamID == teamID {
			teamHeld.CPUMillicores += c.CPUMillicores
			teamHeld.MemoryBytes += c.MemoryBytes
		}
		storage += l.Allocation.SnapshotQuotaBytes
	}
	for _, g := range groups {
		c := g.Sizes.Total()
		held.CPUMillicores += c.CPUMillicores
		held.MemoryBytes += c.MemoryBytes
		if g.TeamID == teamID {
			teamHeld.CPUMillicores += c.CPUMillicores
			teamHeld.MemoryBytes += c.MemoryBytes
			foundGroup = true
			if g.Name != lab.Ref.Group || !g.Sizes.Holds(sizes) {
				return calModel.ErrNotEnoughReserved.Err()
			}
		}
	}
	if !foundGroup {
		c := sizes.Total()
		held.CPUMillicores += c.CPUMillicores
		held.MemoryBytes += c.MemoryBytes
		teamHeld.CPUMillicores += c.CPUMillicores
		teamHeld.MemoryBytes += c.MemoryBytes
	}
	reservation, err := r.Budget(txCtx, eventID)
	if err != nil {
		return calModel.ErrNotEnoughReserved.Err()
	}
	slot := reservation.TeamSlot()
	if teamHeld.CPUMillicores > slot.CPUMillicores || teamHeld.MemoryBytes > slot.MemoryBytes {
		return calModel.ErrNotEnoughReserved.Err()
	}
	if reservation.Unplaced > 0 || len(reservation.Placement) == 0 || !reservation.Window.Contains(time.Now().UTC()) || held.CPUMillicores > reservation.Size.CPUMillicores || held.MemoryBytes > reservation.Size.MemoryBytes || storage > reservation.SizeSnapshotQuotaBytes || (storageBytes > 0 && reservation.SizeSnapshotQuotaBytes == 0) {
		return calModel.ErrNotEnoughReserved.Err()
	}
	if !foundGroup {
		if err = r.CreateGroup(txCtx, eventLabAllocationRepo.Group{EventID: eventID, TeamID: teamID, Name: lab.Ref.Group, Sizes: sizes, Plan: placement.Plan, CreatedAt: time.Now().UTC()}); err != nil {
			return err
		}
	}
	// Preserve an allocated/releasing observation on idempotent retries.
	if before.RuntimeState == "Allocated" || before.RuntimeState == "Releasing" {
		lab.Allocation.RuntimeState = before.RuntimeState
	}
	if labID == uuid.Nil {
		if u.lifecycleControls {
			if err = u.requestGroupRunningInTransaction(txCtx, q, eventID, teamID, lab.Ref.Group, time.Now().UTC()); err != nil {
				return err
			}
		}
		return unit.Save()
	}
	changed, err := eventLabRepo.New(q).Update(txCtx, lab, lab.Revision)
	if err != nil {
		return err
	}
	if !changed {
		return calModel.ErrNotEnoughReserved.Err()
	}
	if u.lifecycleControls {
		if err = u.requestGroupRunningInTransaction(txCtx, q, eventID, teamID, lab.Ref.Group, time.Now().UTC()); err != nil {
			return err
		}
	}
	return unit.Save()
}

func (u *EventUseCase) snapshotQuotaFor(t exerciseModel.Topology) (int64, bool) {
	persistent := false
	for _, d := range t.Devices {
		if d.Persistence != nil && d.Persistence.Enabled {
			persistent = true
		}
	}
	if !persistent {
		return 0, true
	}
	if p, ok := u.infra.(interface {
		SnapshotQuotaFor(exerciseModel.Topology) (int64, bool)
	}); ok {
		return p.SnapshotQuotaFor(t)
	}
	return 0, false
}

func (u *EventUseCase) ResourceTotals(ctx context.Context, eventID uuid.UUID, now time.Time) (eventLabModel.ResourceTotals, error) {
	return u.labResourceTotals(ctx, eventID, now)
}
