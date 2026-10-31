package event

import (
	"context"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventExerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRevealRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/gofrs/uuid"
	"time"
)

// errPersistenceUnavailable: a lab of the cohort keeps device state and the agents report no state persistence, so
// no snapshot room can be reserved for it. It is not a shortage of the reservation, and the wait says so.
var errPersistenceUnavailable = errors.New("state persistence is not available on the laboratory agents")

// errSnapshotQuotaShort: the snapshot quota of the reservation is smaller than what the labs may keep. It also is a
// calModel.ErrNotEnoughReserved (the callers that only ask "does it fit" are unchanged); the wait says which room is short.
var errSnapshotQuotaShort = errors.New("the snapshot quota of the reservation is too small")

func snapshotQuotaShort() error {
	return fmt.Errorf("%w: %w", calModel.ErrNotEnoughReserved.Err(), errSnapshotQuotaShort)
}

// An all_ready cohort's initial copies are reserved together before any create
// dispatch. A partial reservation cannot send a subset to the controller.
func (u *EventUseCase) reserveRevealSet(ctx context.Context, eventID, setID uuid.UUID, now time.Time) error {
	cfg, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return err
	}
	if cfg.TaskRevealMode != eventConfigModel.RevealAllReady {
		return nil
	}
	txCtx, q, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	if _, err = q.LockEventForLabSourceChange(txCtx, eventID); err != nil {
		return err
	}
	if err = q.LockResourceCalendar(txCtx); err != nil {
		return err
	}
	if _, err = q.LockEventConfigForLabSizing(txCtx, eventID); err != nil {
		return err
	}
	reveal := eventLabRevealRepo.New(q)
	cohort, err := reveal.Freeze(txCtx, eventID, setID, now)
	if err != nil {
		return err
	}
	if len(cohort) == 0 {
		return nil // legitimate deferred preparation has no copies to reserve
	}
	barrier, err := reveal.Barrier(txCtx, eventID, setID)
	if err != nil {
		return err
	}
	if barrier.OpenedAt.Valid {
		return nil
	}
	labs, err := eventLabRevealRepo.New(q).Labs(txCtx, eventID, setID, barrier.EligibleTeamIds)
	if err != nil {
		return err
	}
	if len(labs) != len(barrier.EligibleTeamIds) {
		return calModel.ErrNotEnoughReserved.Err()
	}
	for _, l := range labs {
		if err = eventLabRepo.New(q).LockAdmission(txCtx, l.TeamID); err != nil {
			return err
		}
	}
	config, err := eventConfigRepo.New(q).Get(txCtx, eventID)
	if err != nil {
		return err
	}
	set, err := eventExerciseRepo.New(q).GetByID(txCtx, eventID, setID)
	if err != nil {
		return err
	}
	version, err := exerciseRepo.New(q).GetVersion(txCtx, set.ExerciseVersionID)
	if err != nil {
		return err
	}
	planning := NewEventUseCase(Dependencies{Repo: q, Infra: u.infra, Resources: u.resources})
	planning.allocationAccounting = true
	inputs, err := planning.resourcePlanInputs(txCtx, eventID)
	if err != nil {
		return err
	}
	planner, ok := u.infra.(resourcePlanner)
	if !ok {
		return calModel.ErrNotEnoughReserved.Err()
	}
	sizes, known := planner.GroupSizes(inputs.placementNeed().Plan)
	if !known {
		return calModel.ErrNotEnoughReserved.Err()
	}
	allocations := eventLabAllocationRepo.New(q)
	groups, err := allocations.Groups(txCtx, eventID)
	if err != nil {
		return err
	}
	groupByTeam := map[uuid.UUID]eventLabAllocationRepo.Group{}
	for _, g := range groups {
		groupByTeam[g.TeamID] = g
	}
	candidates := map[uuid.UUID]eventLabModel.Lab{}
	candidateTeams := map[uuid.UUID]bool{}
	for _, snapshot := range labs {
		l, err := eventLabRepo.New(q).Lock(txCtx, snapshot.ID)
		if err != nil {
			return err
		}
		if l.DesiredState != "Running" || l.CloseReason == "solved" {
			return calModel.ErrNotEnoughReserved.Err()
		}
		if l.VariantIndex < 0 || int(l.VariantIndex) >= len(version.Variants) {
			return fmt.Errorf("cohort variant is absent")
		}
		topo := version.Variants[l.VariantIndex].Topology
		quota, known := u.snapshotQuotaFor(topo)
		if !known {
			return errPersistenceUnavailable
		}
		amount := u.resourcePolicy().Total(topo).Amount
		if !l.Allocation.ConfiguredRequestsKnown {
			if !l.Admit(eventLabModel.Compute{CPUMillicores: amount.CPUMillicores, MemoryBytes: amount.MemoryBytes}, quota, now) && !l.AdmitKnown(eventLabModel.Compute{CPUMillicores: amount.CPUMillicores, MemoryBytes: amount.MemoryBytes}, quota, l.DefinitionHash, l.Generation, now) {
				return calModel.ErrNotEnoughReserved.Err()
			}
		}
		if g, exists := groupByTeam[l.TeamID]; exists {
			if !g.Sizes.Holds(sizes) || g.Name != l.Ref.Group {
				return calModel.ErrNotEnoughReserved.Err()
			}
		} else {
			g := eventLabAllocationRepo.Group{TeamID: l.TeamID, EventID: eventID, Name: l.Ref.Group, Sizes: sizes, Plan: inputs.placementNeed().Plan, CreatedAt: now}
			if err = allocations.CreateGroup(txCtx, g); err != nil {
				return err
			}
			groupByTeam[l.TeamID] = g
		}
		candidates[l.ID] = l
		candidateTeams[l.TeamID] = true
	}
	all, err := allocations.Labs(txCtx, eventID)
	if err != nil {
		return err
	}
	budget, err := allocations.Budget(txCtx, eventID)
	if err != nil {
		return calModel.ErrNotEnoughReserved.Err()
	}
	held := eventLabModel.Compute{}
	var quota int64
	perTeam := map[uuid.UUID]eventLabModel.Compute{}
	counts := map[uuid.UUID]int{}
	for _, l := range all {
		if candidate, ok := candidates[l.ID]; ok {
			l = candidate
		}
		h := l.HeldCompute()
		held.CPUMillicores += h.CPUMillicores
		held.MemoryBytes += h.MemoryBytes
		quota += l.Allocation.SnapshotQuotaBytes
		t := perTeam[l.TeamID]
		t.CPUMillicores += h.CPUMillicores
		t.MemoryBytes += h.MemoryBytes
		perTeam[l.TeamID] = t
		if l.HoldsRuntime() {
			counts[l.TeamID]++
		}
	}
	slot := budget.TeamSlot()
	holdingTeams := map[uuid.UUID]bool{}
	for id := range candidateTeams {
		holdingTeams[id] = true
	}
	for id, c := range perTeam {
		if c != (eventLabModel.Compute{}) {
			holdingTeams[id] = true
		}
	}
	for id, g := range groupByTeam {
		if g.Lifecycle.HeldCompute() != (eventLabModel.Compute{}) {
			holdingTeams[id] = true
		}
		h := g.Lifecycle.HeldCompute()
		if candidateTeams[id] {
			total := g.Sizes.Total()
			h.CPUMillicores = max(h.CPUMillicores, total.CPUMillicores)
			h.MemoryBytes = max(h.MemoryBytes, total.MemoryBytes)
		}
		held.CPUMillicores += h.CPUMillicores
		held.MemoryBytes += h.MemoryBytes
		t := perTeam[id]
		t.CPUMillicores += h.CPUMillicores
		t.MemoryBytes += h.MemoryBytes
		if t.CPUMillicores > slot.CPUMillicores || t.MemoryBytes > slot.MemoryBytes {
			return calModel.ErrNotEnoughReserved.Err()
		}
		if max := config.EffectiveLabPolicy().MaxActiveLabsPerTeam; max != nil && counts[id] > int(*max) {
			return calModel.ErrNotEnoughReserved.Err()
		}
	}
	placedSize := budget.PlacedSize()
	if !budget.HoldsTeams(len(holdingTeams)) || len(budget.Placement) == 0 || !budget.Window.Contains(now) || held.CPUMillicores > placedSize.CPUMillicores || held.MemoryBytes > placedSize.MemoryBytes {
		return calModel.ErrNotEnoughReserved.Err()
	}
	if quota > budget.SizeSnapshotQuotaBytes {
		return snapshotQuotaShort()
	}
	for _, snapshot := range labs {
		l := candidates[snapshot.ID]
		if ok, err := eventLabRepo.New(q).Update(txCtx, l, l.Revision); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("cohort lifecycle revision changed")
		}
		if err = u.requestGroupRunningInTransaction(txCtx, q, eventID, l.TeamID, l.Ref.Group, now); err != nil {
			return err
		}
	}
	return unit.Save()
}

// revealWait is the wait reason of a refused cohort reservation; waiting is false for an error that is not a wait.
func revealWait(err error) (reason string, waiting bool) {
	switch {
	case errors.Is(err, errSnapshotQuotaShort):
		return eventStandModel.WaitSnapshotQuota, true
	case errors.Is(err, errPersistenceUnavailable):
		return eventStandModel.WaitPersistenceUnavailable, true
	case errors.Is(err, calModel.ErrNotEnoughReserved.Err()):
		return eventStandModel.WaitNotEnoughReserved, true
	}
	return "", false
}
