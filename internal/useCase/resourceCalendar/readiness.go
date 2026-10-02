package resourceCalendarUseCase

import (
	"context"
	"strconv"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
)

// Escalation stages of a not_connected alarm: a day before the deploy lead, two hours before, at the lead.
const (
	StageDayBefore   = 1
	StageTwoHours    = 2
	StageAtLead      = 3
	dayBefore        = 24 * time.Hour
	twoHoursBefore   = 2 * time.Hour
	alarmListLimit   = 200
	alarmSourceLabel = "resource_calendar"
)

// alarmEvent is a change of an alarm that people must hear about once the transaction is committed.
type alarmEvent struct {
	alarm     calModel.Alarm
	resolved  bool
	escalated bool
	label     calModel.Label
	agentName string
}

type cause struct {
	kind  calModel.AlarmKind
	agent *uuid.UUID
	units int
	short Amount
	stage int
}

// causes finds why a reservation cannot be served as promised now. A canceled or ended reservation has none.
func (u *ResourceCalendarUseCase) causes(ctx context.Context, s Store, r *calModel.Reservation, states []agentState, now time.Time) ([]cause, error) {
	if !r.Active() || !now.Before(r.Window.End) || r.Kind != calModel.KindEvent || states == nil {
		return nil, nil
	}
	var out []cause
	slot := r.TeamSlot()
	times := func(n int) Amount {
		return Amount{CPUMillicores: slot.CPUMillicores * int64(n), MemoryBytes: slot.MemoryBytes * int64(n)}
	}
	if r.Unplaced > 0 {
		out = append(out, cause{kind: calModel.AlarmNotPlaced, units: r.Unplaced, short: times(r.Unplaced)})
	}
	used := usedAgents(states)
	byID := make(map[uuid.UUID]calModel.Agent, len(used))
	for _, a := range used {
		byID[a.ID] = a
	}
	all, err := s.ListInWindow(ctx, r.Window)
	if err != nil {
		return nil, platformErr(err, "Failed to read the reservations of the window")
	}
	peak := calModel.PeakLoad(r.Window, all)
	for _, sh := range r.Placement {
		id := sh.AgentID
		a, ok := byID[id]
		if !ok {
			out = append(out, cause{kind: calModel.AlarmAgentLost, agent: &id, units: sh.Units, short: times(sh.Units)})
			continue
		}
		p := peak[id]
		if p.CPUMillicores > a.Capacity.CPUMillicores || p.MemoryBytes > a.Capacity.MemoryBytes {
			short := Amount{CPUMillicores: max(p.CPUMillicores-a.Capacity.CPUMillicores, 0), MemoryBytes: max(p.MemoryBytes-a.Capacity.MemoryBytes, 0)}
			out = append(out, cause{kind: calModel.AlarmAgentShrunk, agent: &id, units: sh.Units, short: short})
		}
	}
	// The connected capacity at the deploy lead: placing the reservation on the agents that answer now.
	start := r.Window.Start
	if !now.Before(start.Add(-dayBefore)) {
		stage := StageDayBefore
		if !now.Before(start.Add(-twoHoursBefore)) {
			stage = StageTwoHours
		}
		if !now.Before(start) {
			stage = StageAtLead
		}
		probe := *r
		probe.Placement, probe.Unplaced = nil, 0
		calModel.PlaceReservation(connectedAgents(states), others(all, r.ID), &probe)
		if probe.Unplaced > 0 {
			out = append(out, cause{kind: calModel.AlarmNotConnected, units: probe.Unplaced, short: times(probe.Unplaced), stage: stage})
		}
	}
	return out, nil
}

func sameAgent(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// reconcileReservation brings the alarms of one reservation in line with its causes: a new cause raises an
// alarm, a known one is updated (and escalates as the lead comes closer), a gone one resolves its alarm. The
// changes people must hear about are returned; they are sent after the commit.
func (u *ResourceCalendarUseCase) reconcileReservation(ctx context.Context, s Store, r *calModel.Reservation, states []agentState, now time.Time) ([]alarmEvent, error) {
	found, err := u.causes(ctx, s, r, states, now)
	if err != nil {
		return nil, err
	}
	open, err := s.ListOpenAlarmsOf(ctx, r.ID)
	if err != nil {
		return nil, platformErr(err, "Failed to read the readiness alarms")
	}
	labels := map[uuid.UUID]calModel.Label{}
	if r.EventID != nil && (len(found) > 0 || len(open) > 0) {
		if l, lErr := s.Labels(ctx, []uuid.UUID{r.ID}); lErr == nil {
			labels = l
		}
	}
	names := map[uuid.UUID]string{}
	for _, st := range states {
		names[st.ID] = st.Name
	}
	var events []alarmEvent
	matched := map[uuid.UUID]bool{}
	for _, c := range found {
		var current *calModel.Alarm
		for _, a := range open {
			if a.Kind == c.kind && sameAgent(a.AgentID, c.agent) {
				current = a
			}
		}
		if current == nil {
			a, nErr := calModel.NewAlarm(c.kind, r, c.agent, c.units, c.short, now)
			if nErr != nil {
				return nil, nErr
			}
			a.Stage = c.stage
			if cErr := s.CreateAlarm(ctx, a); cErr != nil {
				return nil, platformErr(cErr, "Failed to raise the readiness alarm")
			}
			events = append(events, alarmEvent{alarm: *a, label: labels[r.ID], agentName: agentName(names, a.AgentID)})
			continue
		}
		matched[current.ID] = true
		escalated := c.stage > current.Stage
		current.Units, current.Shortage, current.Stage, current.UpdatedAt = c.units, c.short, max(current.Stage, c.stage), now
		if _, uErr := s.UpdateAlarm(ctx, current); uErr != nil {
			return nil, platformErr(uErr, "Failed to update the readiness alarm")
		}
		if escalated {
			events = append(events, alarmEvent{alarm: *current, escalated: true, label: labels[r.ID], agentName: agentName(names, current.AgentID)})
		}
	}
	for _, a := range open {
		if matched[a.ID] {
			continue
		}
		stillOpen := false
		for _, c := range found {
			if c.kind == a.Kind && sameAgent(c.agent, a.AgentID) {
				stillOpen = true
			}
		}
		if stillOpen {
			continue
		}
		a.Resolve(now)
		if _, uErr := s.UpdateAlarm(ctx, a); uErr != nil {
			return nil, platformErr(uErr, "Failed to resolve the readiness alarm")
		}
		events = append(events, alarmEvent{alarm: *a, resolved: true, label: labels[r.ID]})
	}
	return events, nil
}

func agentName(names map[uuid.UUID]string, id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return names[*id]
}

// emit tells the people who act on the calendar: the super admins through the notification system and the
// error journal. A failure is logged, never raised: the alarm itself is stored.
func (u *ResourceCalendarUseCase) emit(ctx context.Context, events []alarmEvent) {
	for _, ev := range events {
		a := ev.alarm
		if ev.resolved {
			if u.notifier != nil {
				if err := u.notifier.ResourceAlarmClosed(ctx, a.ID, uuid.Nil); err != nil {
					log.Warn().Err(err).Str("alarm_id", a.ID.String()).Msg("Failed to close the readiness alarm request")
				}
			}
			continue
		}
		if u.notifier != nil {
			err := u.notifier.ResourceAlarmRaised(ctx, inboxUseCase.ResourceAlarm{
				ID: a.ID, Kind: string(a.Kind), EventID: derefID(a.EventID), EventName: ev.label.EventName, EventTag: ev.label.EventTag,
				AgentName: ev.agentName, Units: a.Units, Stage: a.Stage, RaisedAt: a.RaisedAt, Escalated: ev.escalated,
			})
			if err != nil {
				log.Warn().Err(err).Str("alarm_id", a.ID.String()).Msg("Failed to notify about the readiness alarm")
			}
		}
		details := map[string]string{
			"alarm": string(a.Kind), "reservation": a.ReservationID.String(), "units": strconv.Itoa(a.Units), "stage": strconv.Itoa(a.Stage),
			"event": ev.label.EventTag,
		}
		if ev.agentName != "" {
			details["agent"] = ev.agentName
		}
		errorJournal.Report(errorJournal.Event{
			Kind: errorJournal.KindLabReadiness, Source: alarmSourceLabel + "/" + ev.label.EventTag,
			Message: "Resource reservation cannot be served: " + string(a.Kind),
			Key:     string(a.Kind) + ":" + a.ReservationID.String() + ":" + derefID(a.AgentID).String() + ":" + strconv.Itoa(a.Stage),
			Details: details, At: a.UpdatedAt,
		})
	}
}

func derefID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

// Reconcile is the periodic readiness check: it completes the teams that had no agent when capacity appears (it
// only adds, nothing placed is moved), raises, escalates and resolves the alarms, and drops the expired test
// laboratory holds.
func (u *ResourceCalendarUseCase) ReconcileResourceCalendar(ctx context.Context) error {
	now := u.now().UTC()
	states, err := u.agentStates(ctx, now)
	if err != nil {
		return err
	}
	if _, err = u.store.PurgeHolds(ctx, now); err != nil {
		log.Warn().Err(err).Msg("Failed to purge the expired test laboratory holds")
	}
	upcoming, err := u.store.ListEndingAfter(ctx, now)
	if err != nil {
		return platformErr(err, "Failed to list the reservations")
	}
	// Alarms of reservations that ended or were canceled close with the last pass over them; only the live ones
	// are looked at here.
	for _, r := range upcoming {
		if r.Kind != calModel.KindEvent {
			continue
		}
		var events []alarmEvent
		txErr := u.inTx(ctx, func(ctx context.Context, s Store) error {
			fresh, gErr := s.GetReservation(ctx, r.ID)
			if gErr != nil || !fresh.Active() {
				return nil
			}
			if fresh.Unplaced > 0 {
				all, lErr := s.ListInWindow(ctx, fresh.Window)
				if lErr != nil {
					return platformErr(lErr, "Failed to read the reservations of the window")
				}
				if calModel.CompleteUnplaced(usedAgents(states), others(all, fresh.ID), fresh) {
					fresh.UpdatedAt = now
					if _, uErr := s.UpdateReservation(ctx, fresh); uErr != nil {
						return platformErr(uErr, "Failed to save the completed placement")
					}
				}
			}
			var rErr error
			events, rErr = u.reconcileReservation(ctx, s, fresh, states, now)
			return rErr
		})
		if txErr != nil {
			log.Warn().Err(txErr).Str("reservation_id", r.ID.String()).Msg("Readiness check of a reservation failed")
			continue
		}
		u.emit(ctx, events)
	}
	return nil
}

// ListAlarms lists the readiness alarms, open ones first.
func (u *ResourceCalendarUseCase) ListResourceAlarms(ctx context.Context, onlyOpen bool) ([]AlarmView, error) {
	rows, err := u.store.ListAlarms(ctx, onlyOpen, alarmListLimit)
	if err != nil {
		return nil, platformErr(err, "Failed to list the readiness alarms")
	}
	states, err := u.agentStates(ctx, u.now().UTC())
	if err != nil {
		return nil, err
	}
	names := map[uuid.UUID]string{}
	for _, st := range states {
		names[st.ID] = st.Name
	}
	out := make([]AlarmView, 0, len(rows))
	for _, row := range rows {
		out = append(out, alarmView(row.Alarm, calModel.Label{EventName: row.EventName, EventTag: row.EventTag}, names))
	}
	return out, nil
}

func alarmView(a calModel.Alarm, l calModel.Label, names map[uuid.UUID]string) AlarmView {
	return AlarmView{
		ID: a.ID, Kind: a.Kind, ReservationID: a.ReservationID, EventID: a.EventID, EventName: l.EventName, EventTag: l.EventTag,
		AgentID: a.AgentID, AgentName: agentName(names, a.AgentID), Units: a.Units, Stage: a.Stage, Shortage: a.Shortage,
		RaisedAt: a.RaisedAt, UpdatedAt: a.UpdatedAt, ResolvedAt: a.ResolvedAt, AckedBy: a.AckedBy, AckedAt: a.AckedAt,
	}
}

// AcknowledgeAlarm records that an admin saw an alarm and closes its inbox request; the alarm stays open while
// its cause lasts.
func (u *ResourceCalendarUseCase) AcknowledgeResourceAlarm(ctx context.Context, id, by uuid.UUID) (AlarmView, error) {
	a, err := u.store.GetAlarm(ctx, id)
	if err != nil {
		if notFound(err) {
			return AlarmView{}, calModel.ErrAlarmNotFound.Err()
		}
		return AlarmView{}, platformErr(err, "Failed to read the readiness alarm")
	}
	a.Acknowledge(by, u.now().UTC())
	if _, err = u.store.UpdateAlarm(ctx, a); err != nil {
		return AlarmView{}, platformErr(err, "Failed to acknowledge the readiness alarm")
	}
	if u.notifier != nil {
		if nErr := u.notifier.ResourceAlarmClosed(ctx, a.ID, by); nErr != nil {
			log.Warn().Err(nErr).Msg("Failed to close the readiness alarm request")
		}
	}
	label := calModel.Label{}
	if labels, lErr := u.store.Labels(ctx, []uuid.UUID{a.ReservationID}); lErr == nil {
		label = labels[a.ReservationID]
	}
	return alarmView(*a, label, nil), nil
}
