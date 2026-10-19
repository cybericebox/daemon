package resourceCalendarUseCase

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

const (
	// DefaultReservationNotice is how long before its end a cluster reservation gets the ending notice, when the
	// agent does not say (the laboratory default).
	DefaultReservationNotice = 5 * time.Minute
	// clusterSyncAlarmAfter is how long a part may stay unsent before an alarm is raised.
	clusterSyncAlarmAfter = 15 * time.Minute
	// minClusterWindow keeps a forced end in the future: the agent refuses an end that is not.
	minClusterWindow = time.Minute
)

// PartStore keeps the cluster reservation parts and the agent of each unit of a calendar reservation (satisfied by
// *clusterReservationRepo.Repository, embedded in the calendar store so a calendar decision writes its parts in the
// same transaction).
type PartStore interface {
	SavePart(ctx context.Context, p calModel.Part) error
	MarkSynced(ctx context.Context, id uuid.UUID, a calModel.SyncAnswer) error
	Observe(ctx context.Context, id uuid.UUID, s calModel.PartStatus) error
	DeletePart(ctx context.Context, id uuid.UUID) error
	GetPart(ctx context.Context, id uuid.UUID) (calModel.Part, error)
	PartsOfAgent(ctx context.Context, agent uuid.UUID) ([]calModel.Part, error)
	PartsOfCalendar(ctx context.Context, calendarID uuid.UUID) ([]calModel.Part, error)
	PartsOfCalendars(ctx context.Context, ids []uuid.UUID) ([]calModel.Part, error)
	RunPart(ctx context.Context, deployID uuid.UUID) (calModel.Part, error)
	RunPartsOf(ctx context.Context, owner, agent uuid.UUID) ([]calModel.Part, error)
	GroupPart(ctx context.Context, owner uuid.UUID) (calModel.Part, error)
	PartAgents(ctx context.Context) ([]uuid.UUID, error)
	UnsyncedAgents(ctx context.Context) ([]uuid.UUID, error)
	Units(ctx context.Context, calendarID uuid.UUID) (map[string]uuid.UUID, error)
	AssignUnit(ctx context.Context, calendarID uuid.UUID, unit string, agent uuid.UUID, at time.Time) (uuid.UUID, error)
	ReleaseUnit(ctx context.Context, calendarID uuid.UUID, unit string) error
}

// clusterSync is the wiring of the cluster reservations: the agents' reservation API and the ending notice each
// agent gives.
type clusterSync struct {
	port   infraModel.ReservationPort
	notice func(agentID uuid.UUID) time.Duration
	newID  func() uuid.UUID
	// locks serializes the pushes to one agent inside this process.
	locks sync.Map
	// listener acts on the ending and the end of parts.
	listener PartListener
}

// SetClusterReservations wires the agents' reservation API; notice says how long before its end an agent gives
// the ending notice (nil: the laboratory default). Without it the calendar keeps its parts and sends nothing.
func (u *ResourceCalendarUseCase) SetClusterReservations(port infraModel.ReservationPort, notice func(agentID uuid.UUID) time.Duration) {
	u.cluster.port, u.cluster.notice = port, notice
}

func (u *ResourceCalendarUseCase) noticeOf(agent uuid.UUID) time.Duration {
	if u.cluster.notice != nil {
		if n := u.cluster.notice(agent); n > 0 {
			return n
		}
	}
	return DefaultReservationNotice
}

func (u *ResourceCalendarUseCase) newPartID() uuid.UUID {
	if u.cluster.newID != nil {
		return u.cluster.newID()
	}
	return uuid.Must(uuid.NewV7())
}

// splitParts brings the parts of a calendar reservation in line with its placement (the caller holds the calendar
// lock and saved r). forced marks a change an admin forced past the shrink guard.
func (u *ResourceCalendarUseCase) splitParts(ctx context.Context, s Store, r *calModel.Reservation, forced bool, now time.Time) error {
	existing, err := s.PartsOfCalendar(ctx, r.ID)
	if err != nil {
		return platformErr(err, "Failed to read the cluster reservation parts")
	}
	before := make(map[uuid.UUID]calModel.Part, len(existing))
	for _, p := range existing {
		before[p.ID] = p
	}
	keep, add, retire := calModel.SplitCalendar(r, existing, u.newPartID, now)
	for _, group := range [][]calModel.Part{keep, add, retire} {
		for _, p := range group {
			// An unchanged part is saved again only when the admin now forces the shrink it holds back.
			if old, ok := before[p.ID]; ok && old.DesiredRev == p.DesiredRev && (!forced || !old.ShrinkHeld || old.Forced) {
				continue
			}
			p.Forced = forced
			if err = s.SavePart(ctx, p); err != nil {
				return platformErr(err, "Failed to save a cluster reservation part")
			}
		}
	}
	return nil
}

// pushParts sends every part that is not on its agent yet, after the commit of the change that made it. A failure
// is logged: the part stays unsent, the periodic reconcile retries it and raises the alarm.
func (u *ResourceCalendarUseCase) pushParts(ctx context.Context) {
	if u.cluster.port == nil {
		return
	}
	agents, err := u.store.UnsyncedAgents(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to list the agents with cluster reservations to send")
		return
	}
	for _, a := range agents {
		if err = u.SyncAgent(ctx, a); err != nil {
			log.Warn().Err(err).Str("agent_id", a.String()).Msg("Failed to send the cluster reservations to the agent")
		}
	}
}

// SyncCalendarReservation splits a calendar reservation again and sends its parts.
func (u *ResourceCalendarUseCase) SyncCalendarReservation(ctx context.Context, calendarID uuid.UUID) error {
	now := u.now().UTC()
	err := u.inTx(ctx, func(ctx context.Context, s Store) error {
		r, err := s.GetReservation(ctx, calendarID)
		if err != nil {
			if notFound(err) {
				return calModel.ErrReservationNotFound.Err()
			}
			return platformErr(err, "Failed to read the reservation")
		}
		return u.splitParts(ctx, s, r, false, now)
	})
	if err != nil {
		return err
	}
	u.pushParts(ctx)
	return nil
}

func (u *ResourceCalendarUseCase) agentLock(agent uuid.UUID) *sync.Mutex {
	m, _ := u.cluster.locks.LoadOrStore(agent, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// sameMillis compares two instants at the agent's precision (milliseconds).
func sameMillis(a, b time.Time) bool { return a.UnixMilli() == b.UnixMilli() }

func running(listed *infraModel.ClusterReservation) bool {
	return listed != nil && listed.Status != nil && listed.Status.Groups > 0
}

// clampDesired is the shrink guard: while labs run in a part, a change never sends an amount below what they use
// (or below what the agent holds, if that is less) and never an end earlier than the notice gives them. A part
// that is not on the agent, or has no groups, is not clamped. An admin's forced change is sent as it is (its end is
// kept in the future). The start of a part that has begun cannot change: the agent keeps it.
func clampDesired(p calModel.Part, listed *infraModel.ClusterReservation, notice time.Duration, now time.Time, forced bool) (calModel.Part, bool) {
	out := p
	if listed != nil && listed.Status != nil && listed.Status.Phase != calModel.PartPending && listed.Status.Phase != "" {
		out.Window.Start = listed.From
	}
	if forced {
		if !out.Window.End.After(now) {
			out.Window.End = now.Add(minClusterWindow)
		}
		return out, false
	}
	if !running(listed) {
		return out, false
	}
	clamped := false
	floor := listed.Status.Used.Min(listed.Amount)
	if out.Amount.CPUMillicores < floor.CPUMillicores {
		out.Amount.CPUMillicores, clamped = floor.CPUMillicores, true
	}
	if out.Amount.MemoryBytes < floor.MemoryBytes {
		out.Amount.MemoryBytes, clamped = floor.MemoryBytes, true
	}
	earliest := now.Add(notice)
	if listed.To.Before(earliest) {
		earliest = listed.To
	}
	if out.Window.End.Before(earliest) {
		out.Window.End, clamped = earliest, true
	}
	return out, clamped
}

func desiredOf(p calModel.Part) infraModel.ClusterReservation {
	return infraModel.ClusterReservation{ID: p.ID, From: p.Window.Start, To: p.Window.End, Amount: p.Amount}
}

func differs(want infraModel.ClusterReservation, listed infraModel.ClusterReservation) bool {
	return !sameMillis(want.From, listed.From) || !sameMillis(want.To, listed.To) || want.Amount != listed.Amount
}

// pending is one call to make for a part and what to record when the agent answers.
type pending struct {
	part       calModel.Part
	want       infraModel.ClusterReservation
	shrinkHeld bool
	// drop removes the row once the agent confirmed the delete.
	drop bool
}

// SyncAgent brings one agent in line with the parts it should hold: a desired part that is not listed is created,
// one that differs is updated (through the shrink guard), a retired one is deleted (after the notice when labs
// still run in it), and a listed reservation of this instance that no part names is deleted as an orphan. Every
// OK answer marks its revision synced; a refusal is recorded on the part and retried by the next pass.
func (u *ResourceCalendarUseCase) SyncAgent(ctx context.Context, agentID uuid.UUID) error {
	if u.cluster.port == nil {
		return nil
	}
	mu := u.agentLock(agentID)
	mu.Lock()
	defer mu.Unlock()
	now := u.now().UTC()
	parts, err := u.store.PartsOfAgent(ctx, agentID)
	if err != nil {
		return platformErr(err, "Failed to read the cluster reservation parts")
	}
	listed, err := u.cluster.port.ListReservations(ctx, agentID)
	if err != nil {
		u.recordFailure(ctx, parts, err, now)
		u.reconcilePartAlarms(ctx, parts)
		return fmt.Errorf("list the cluster reservations: %w", err)
	}
	byID := make(map[uuid.UUID]*infraModel.ClusterReservation, len(listed))
	for i := range listed {
		byID[listed[i].ID] = &listed[i]
	}
	notice := u.noticeOf(agentID)
	known := make(map[uuid.UUID]bool, len(parts))
	var creates, updates, deletes []pending
	for _, p := range parts {
		known[p.ID] = true
		l := byID[p.ID]
		// A live part whose window has passed is no longer desired: the agent ended it.
		if !p.Retired() && !p.Window.End.After(now) {
			p.Retire(now)
			if err = u.store.SavePart(ctx, p); err != nil {
				return platformErr(err, "Failed to retire an ended cluster reservation part")
			}
		}
		if p.Retired() {
			if l == nil {
				if err = u.store.DeletePart(ctx, p.ID); err != nil {
					return platformErr(err, "Failed to drop a cluster reservation part")
				}
				continue
			}
			// Labs still run in it: they get the notice first, the part ends after it.
			end := p.RetireAt.Add(notice)
			if running(l) && end.After(now) {
				if l.To.After(end) {
					want := *l
					want.Status, want.To = nil, end
					updates = append(updates, pending{part: p, want: want})
				}
				continue
			}
			deletes = append(deletes, pending{part: p, want: desiredOf(p), drop: true})
			continue
		}
		send, clamped := clampDesired(p, l, notice, now, p.Forced)
		want := desiredOf(send)
		switch {
		case l == nil:
			creates = append(creates, pending{part: p, want: want})
		case differs(want, *l):
			updates = append(updates, pending{part: p, want: want, shrinkHeld: clamped})
		case !p.Synced() || p.ShrinkHeld != clamped || p.SyncError != "":
			// The agent already holds what is sent: the revision is in place.
			u.answer(ctx, p, calModel.SyncAnswer{OK: true, Rev: p.DesiredRev, ShrinkHeld: clamped, At: now})
		}
	}
	for _, l := range listed {
		if !known[l.ID] {
			deletes = append(deletes, pending{want: infraModel.ClusterReservation{ID: l.ID}})
		}
	}
	var firstErr error
	if err = u.applyBatch(ctx, creates, now, u.cluster.port.CreateReservations, agentID); err != nil {
		firstErr = err
	}
	if err = u.applyBatch(ctx, updates, now, u.cluster.port.UpdateReservations, agentID); err != nil && firstErr == nil {
		firstErr = err
	}
	if len(deletes) > 0 {
		ids := make([]uuid.UUID, 0, len(deletes))
		for _, d := range deletes {
			ids = append(ids, d.want.ID)
		}
		out, dErr := u.cluster.port.DeleteReservations(ctx, agentID, ids)
		u.record(ctx, deletes, out, dErr, now)
		if dErr != nil && firstErr == nil {
			firstErr = dErr
		}
	}
	fresh, err := u.store.PartsOfAgent(ctx, agentID)
	if err == nil {
		u.reconcilePartAlarms(ctx, fresh)
	}
	return firstErr
}

type batchCall func(ctx context.Context, agentID uuid.UUID, rs []infraModel.ClusterReservation) ([]infraModel.ItemOutcome, error)

func (u *ResourceCalendarUseCase) applyBatch(ctx context.Context, items []pending, now time.Time, call batchCall, agentID uuid.UUID) error {
	if len(items) == 0 {
		return nil
	}
	rs := make([]infraModel.ClusterReservation, 0, len(items))
	for _, it := range items {
		rs = append(rs, it.want)
	}
	out, err := call(ctx, agentID, rs)
	u.record(ctx, items, out, err, now)
	return err
}

// record stores the agent's answers: the revision each OK confirms, the refusal of each failed item, or the call's
// error on every item when the call itself failed.
func (u *ResourceCalendarUseCase) record(ctx context.Context, items []pending, out []infraModel.ItemOutcome, callErr error, now time.Time) {
	byID := make(map[uuid.UUID]infraModel.ItemOutcome, len(out))
	for _, o := range out {
		byID[o.ID] = o
	}
	for _, it := range items {
		if it.part.ID == uuid.Nil {
			continue // an orphan has no row
		}
		o, ok := byID[it.want.ID]
		switch {
		case callErr != nil:
			u.answer(ctx, it.part, calModel.SyncAnswer{Error: callErr.Error(), At: now})
		case !ok:
			u.answer(ctx, it.part, calModel.SyncAnswer{Error: "the agent gave no answer for the reservation", At: now})
		case o.OK && it.drop:
			if err := u.store.DeletePart(ctx, it.part.ID); err != nil {
				log.Warn().Err(err).Str("part_id", it.part.ID.String()).Msg("Failed to drop a deleted cluster reservation part")
			}
		case o.OK:
			u.answer(ctx, it.part, calModel.SyncAnswer{OK: true, Rev: it.part.DesiredRev, ShrinkHeld: it.shrinkHeld, At: now})
		default:
			msg := "the agent refused the reservation"
			if o.Err != nil {
				msg = o.Err.Error()
			}
			u.answer(ctx, it.part, calModel.SyncAnswer{Error: msg, Reason: o.Reason, ShrinkHeld: it.shrinkHeld, At: now})
		}
	}
}

func (u *ResourceCalendarUseCase) answer(ctx context.Context, p calModel.Part, a calModel.SyncAnswer) {
	if err := u.store.MarkSynced(ctx, p.ID, a); err != nil {
		log.Warn().Err(err).Str("part_id", p.ID.String()).Msg("Failed to record the agent's answer for a cluster reservation part")
	}
}

// recordFailure marks every part that waits to be sent as failed with the agent's error.
func (u *ResourceCalendarUseCase) recordFailure(ctx context.Context, parts []calModel.Part, err error, now time.Time) {
	for _, p := range parts {
		if !p.Synced() || p.Retired() {
			u.answer(ctx, p, calModel.SyncAnswer{Error: err.Error(), ShrinkHeld: p.ShrinkHeld, At: now})
		}
	}
}

// reconcilePartAlarms brings the readiness alarms of the calendar reservations of these parts in line (a part not
// sent for a while, a shrink held back, the agent refusing over its quota).
func (u *ResourceCalendarUseCase) reconcilePartAlarms(ctx context.Context, parts []calModel.Part) {
	seen := map[uuid.UUID]bool{}
	for _, p := range parts {
		if p.CalendarID == nil || seen[*p.CalendarID] {
			continue
		}
		seen[*p.CalendarID] = true
		id := *p.CalendarID
		now := u.now().UTC()
		states, err := u.agentStates(ctx, now)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to read the agents for the cluster reservation alarms")
			return
		}
		var events []alarmEvent
		err = u.inTx(ctx, func(ctx context.Context, s Store) error {
			r, gErr := s.GetReservation(ctx, id)
			if gErr != nil {
				if notFound(gErr) {
					return nil
				}
				return gErr
			}
			var rErr error
			events, rErr = u.reconcileReservation(ctx, s, r, states, now)
			return rErr
		})
		if err != nil {
			log.Warn().Err(err).Str("reservation_id", id.String()).Msg("Failed to update the cluster reservation alarms")
			continue
		}
		u.emit(ctx, events)
	}
}

// partCauses are the alarms the cluster parts of a reservation raise.
func partCauses(parts []calModel.Part, now time.Time) []cause {
	var out []cause
	for _, p := range parts {
		if p.Retired() {
			continue
		}
		agent := p.AgentID
		switch {
		case p.SyncReason == infraModel.ReasonOverQuota:
			out = append(out, cause{kind: calModel.AlarmAgentShrunk, agent: &agent, short: p.Amount})
		case p.SyncFailingFor(clusterSyncAlarmAfter, now):
			out = append(out, cause{kind: calModel.AlarmClusterSyncFailed, agent: &agent})
		}
		if p.ShrinkHeld {
			out = append(out, cause{kind: calModel.AlarmClusterShrinkHeld, agent: &agent})
		}
	}
	return out
}

// ReconcileClusterReservations is the periodic pass: every live calendar reservation is split again, and every
// agent (the ones that hold parts and the ones in use) is brought in line, orphans included.
func (u *ResourceCalendarUseCase) ReconcileClusterReservations(ctx context.Context) error {
	if u.cluster.port == nil {
		return nil
	}
	now := u.now().UTC()
	live, err := u.store.ListEndingAfter(ctx, now)
	if err != nil {
		return platformErr(err, "Failed to list the reservations")
	}
	for _, r := range live {
		id := r.ID
		if txErr := u.inTx(ctx, func(ctx context.Context, s Store) error {
			fresh, gErr := s.GetReservation(ctx, id)
			if gErr != nil {
				if notFound(gErr) {
					return nil
				}
				return gErr
			}
			return u.splitParts(ctx, s, fresh, false, now)
		}); txErr != nil {
			log.Warn().Err(txErr).Str("reservation_id", id.String()).Msg("Failed to split a reservation into cluster parts")
		}
	}
	agents := map[uuid.UUID]bool{}
	held, err := u.store.PartAgents(ctx)
	if err != nil {
		return platformErr(err, "Failed to list the agents with cluster reservations")
	}
	for _, a := range held {
		agents[a] = true
	}
	states, err := u.agentStates(ctx, now)
	if err != nil {
		return err
	}
	for _, s := range states {
		if s.Used {
			agents[s.ID] = true
		}
	}
	var errs []error
	for _, s := range calModel.Ordered(agentList(states, agents)) {
		if sErr := u.SyncAgent(ctx, s.ID); sErr != nil {
			errs = append(errs, fmt.Errorf("agent %s: %w", s.ID, sErr))
		}
	}
	// Agents that hold parts but are no longer in the registry are still tried, in id order.
	var gone []uuid.UUID
	for a := range agents {
		if !inStates(states, a) {
			gone = append(gone, a)
		}
	}
	sort.Slice(gone, func(i, j int) bool { return gone[i].String() < gone[j].String() })
	for _, a := range gone {
		if sErr := u.SyncAgent(ctx, a); sErr != nil {
			errs = append(errs, fmt.Errorf("agent %s: %w", a, sErr))
		}
	}
	return errors.Join(errs...)
}

func agentList(states []agentState, want map[uuid.UUID]bool) []calModel.Agent {
	var out []calModel.Agent
	for _, s := range states {
		if want[s.ID] {
			out = append(out, s.Agent)
		}
	}
	return out
}

func inStates(states []agentState, id uuid.UUID) bool {
	for _, s := range states {
		if s.ID == id {
			return true
		}
	}
	return false
}

// partViews are the parts of reservations for the admin, by reservation.
func (u *ResourceCalendarUseCase) partViews(ctx context.Context, ids []uuid.UUID, states []agentState) (map[uuid.UUID][]PartView, error) {
	out := map[uuid.UUID][]PartView{}
	if len(ids) == 0 {
		return out, nil
	}
	parts, err := u.store.PartsOfCalendars(ctx, ids)
	if err != nil {
		return nil, platformErr(err, "Failed to read the cluster reservation parts")
	}
	names := map[uuid.UUID]string{}
	for _, s := range states {
		names[s.ID] = s.Name
	}
	for _, p := range parts {
		if p.CalendarID == nil {
			continue
		}
		out[*p.CalendarID] = append(out[*p.CalendarID], PartView{
			ID: p.ID, AgentID: p.AgentID, AgentName: names[p.AgentID], From: p.Window.Start, To: p.Window.End, Amount: p.Amount,
			Used: p.Status.Used, Groups: p.Status.Groups, Phase: p.Status.Phase, EndsAt: p.Status.EndsAt,
			Synced: p.Synced(), SyncError: p.SyncError, ShrinkHeld: p.ShrinkHeld, Retiring: p.Retired(),
		})
	}
	return out, nil
}
