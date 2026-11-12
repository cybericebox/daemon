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
	"github.com/cybericebox/daemon/internal/delivery/repository/revealDeclineRepo"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
	"time"
)

// errPersistenceUnavailable: a lab of the cohort keeps device state and the agents report no state persistence, so
// no snapshot room can be reserved for it. It is not a shortage of the reservation, and the wait says so.
var errPersistenceUnavailable = errors.New("state persistence is not available on the laboratory agents")

// errSnapshotQuotaShort: the snapshot quota of the reservation is smaller than what the labs may keep. It also is a
// calModel.ErrNotEnoughReserved (the callers that only ask "does it fit" are unchanged); the wait says which room is short.
var errSnapshotQuotaShort = errors.New("the snapshot quota of the reservation is too small")

// snapshotQuotaShort is the refusal for a snapshot quota of need bytes against have bytes.
func snapshotQuotaShort(need, have int64) error {
	return fmt.Errorf("%w: %w", reservationShortage{detail: eventStandModel.WaitDetail{NeedStorageBytes: need, HaveStorageBytes: have}}, errSnapshotQuotaShort)
}

// errCohortRoomShort: the reservation has no room for the cohort as it is now (the compute and the team slots of
// the placed size). It is the one refusal a late team may be the cause of, and the only one that keeps it out; it also
// is a calModel.ErrNotEnoughReserved through reservationShortage. Other refusals (a missing planner, a lab-count
// mismatch, the snapshot quota, no persistence) say nothing about the newcomer and never eject a team.
var errCohortRoomShort = errors.New("the reservation has no room for the cohort")

// errCohortTrimmed: a late team was declined and the decision is saved; the reservation is tried again without it.
var errCohortTrimmed = errors.New("a late team was kept out of the cohort")

// An all_ready cohort's initial copies are reserved together before any create
// dispatch. A partial reservation cannot send a subset to the controller.
//
// The cohort is all or nothing for the teams it started with. A team that joins it afterwards (before or after the
// start, until the tasks are shown) joins the same reservation; when the reservation has no room for it, it stays out
// (declined, with the reason) and the others, and the show, do not wait for it. The choice of the team and its
// decline are made in the same locked unit of work as the reservation.
func (u *EventUseCase) reserveRevealSet(ctx context.Context, eventID, setID uuid.UUID, now time.Time) error {
	const maxDeclines = 64
	for range maxDeclines {
		err := u.reserveRevealCohort(ctx, eventID, setID, now)
		if !errors.Is(err, errCohortTrimmed) {
			return err
		}
	}
	return calModel.ErrNotEnoughReserved.Err()
}

// trimLateTeam keeps the newest late team out of the cohort when the room is short (cause is errCohortRoomShort) and
// some labs of the cohort already hold their reservation, so the first reservation stays all or nothing. It records
// the decline on store, which belongs to the caller's unit of work, and reports whether a team was declined.
func trimLateTeam(ctx context.Context, store RevealDeclineStore, eventID, setID uuid.UUID, labs []eventLabModel.Lab, cause error, now time.Time) (bool, error) {
	if store == nil || !errors.Is(cause, errCohortRoomShort) {
		return false, nil
	}
	team, ok := lateTeamToDecline(labs)
	if !ok {
		return false, nil
	}
	reason := "the cohort does not fit the reservation with this team: " + cause.Error()
	if err := store.Decline(ctx, eventID, setID, team, reason, now); err != nil {
		return false, err
	}
	log.Info().Str("event_id", eventID.String()).Str("team_id", team.String()).Msg("A late team is kept out of the cohort: its labs do not fit the reservation")
	return true, nil
}

// lateTeamToDecline picks the newest team whose lab holds no reservation yet, provided some other lab of the cohort
// already does; false when the whole cohort is still unreserved (all or nothing) or every lab is reserved.
func lateTeamToDecline(labs []eventLabModel.Lab) (uuid.UUID, bool) {
	anchored := false
	var late *eventLabModel.Lab
	for i := range labs {
		if labs[i].Allocation.ConfiguredRequestsKnown {
			anchored = true
			continue
		}
		if late == nil || labs[i].CreatedAt.After(late.CreatedAt) {
			late = &labs[i]
		}
	}
	if !anchored || late == nil {
		return uuid.Nil, false
	}
	return late.TeamID, true
}

func (u *EventUseCase) reserveRevealCohort(ctx context.Context, eventID, setID uuid.UUID, now time.Time) error {
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
	var newGroups []eventLabAllocationRepo.Group
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
			newGroups = append(newGroups, g) // written once the cohort is known to fit
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
		shortage := fmt.Errorf("%w: %w", reservationShortage{detail: eventStandModel.WaitDetail{
			NeedCPUMillicores: held.CPUMillicores, NeedMemoryBytes: held.MemoryBytes,
			HaveCPUMillicores: placedSize.CPUMillicores, HaveMemoryBytes: placedSize.MemoryBytes,
		}}, errCohortRoomShort)
		var store RevealDeclineStore
		if u.revealDecline != nil {
			store = revealDeclineRepo.New(q)
		}
		trimmed, trimErr := trimLateTeam(txCtx, store, eventID, setID, labs, shortage, now)
		if trimErr != nil {
			return trimErr
		}
		if trimmed {
			// Only the decline is saved: the groups of this attempt were not written yet.
			if err = unit.Save(); err != nil {
				return err
			}
			return errCohortTrimmed
		}
		return shortage
	}
	if quota > budget.SizeSnapshotQuotaBytes {
		return snapshotQuotaShort(quota, budget.SizeSnapshotQuotaBytes)
	}
	for _, g := range newGroups {
		if err = allocations.CreateGroup(txCtx, g); err != nil {
			return err
		}
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

// revealWait is the wait reason of a refused cohort reservation, with what was needed and what there is when the
// error says it; waiting is false for an error that is not a wait.
func revealWait(err error) (reason string, detail *eventStandModel.WaitDetail, waiting bool) {
	var shortage reservationShortage
	if errors.As(err, &shortage) {
		detail = &shortage.detail
	}
	switch {
	case errors.Is(err, errSnapshotQuotaShort):
		return eventStandModel.WaitSnapshotQuota, detail, true
	case errors.Is(err, errPersistenceUnavailable):
		return eventStandModel.WaitPersistenceUnavailable, nil, true
	case errors.Is(err, calModel.ErrNotEnoughReserved.Err()):
		return eventStandModel.WaitNotEnoughReserved, detail, true
	}
	return "", nil, false
}
