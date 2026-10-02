package resourceCalendarUseCase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	liberr "github.com/cybericebox/daemon/pkg/err"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

var now0 = time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC)

type harness struct {
	uc       *ResourceCalendarUseCase
	store    *memStore
	agents   *fakeAgents
	planner  *fakePlanner
	notifier *fakeNotifier
	usage    *fakeUsage
	clock    time.Time
	event    eventModel.Event
}

func i64(v int64) *int64 { return &v }

func record(n byte, priority int, cpu, mem int64, seen time.Time) infraModel.AgentRecord {
	return infraModel.AgentRecord{AgentRegistration: infraModel.AgentRegistration{
		ID: uuid.UUID{15: n}, Name: string('a' + rune(n-1)), Enabled: true, Priority: priority,
		CapacityCPUMillicores: i64(cpu), CapacityMemoryBytes: i64(mem), CapacitySeenAt: &seen,
	}}
}

// newHarness: an event 8:00-12:00 UTC on the day, now 6:00; two agents of 10 CPU each.
func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{store: newMemStore(), notifier: &fakeNotifier{}, usage: &fakeUsage{}, clock: now0}
	h.agents = &fakeAgents{records: []infraModel.AgentRecord{record(1, 0, 10000, 64<<30, now0), record(2, 1, 10000, 64<<30, now0)}}
	finish := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	h.event = eventModel.Event{ID: uuid.Must(uuid.NewV7()), Name: "CTF", Tag: "ctf", InfrastructureAllowed: true,
		Lifecycle: eventModel.Lifecycle{Configured: true, PublishAt: now0.Add(-24 * time.Hour), StartAt: time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC), FinishAt: &finish}}
	h.planner = &fakePlanner{need: Need{Teams: 8, PerTeam: Amount{CPUMillicores: 1000, MemoryBytes: 2 << 30}, LargestDevice: Amount{CPUMillicores: 250, MemoryBytes: 1 << 30}}}
	h.uc = New(Dependencies{
		Store: h.store, Tx: passTx{h.store}, Agents: h.agents, Events: fakeEvents{h.event}, Configs: fakeConfigs{}, Planner: h.planner,
		Usage: usageFunc(func() Usage { return h.usage.u }), Notifier: h.notifier, Config: DefaultConfig(), Now: func() time.Time { return h.clock },
	})
	return h
}

type usageFunc func() Usage

func (f usageFunc) Usage(context.Context, time.Time) (Usage, error) { return f(), nil }

func is(err error, target liberr.ErrorCreator) bool {
	var e liberr.Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Is(target.Err())
}

func TestEventReservationSizeWindowAndPlacement(t *testing.T) {
	h := newHarness(t)
	res, err := h.uc.SetEventResourceReservation(context.Background(), h.event.ID, EventReservationInput{}, uuid.Nil)
	require.NoError(t, err)
	assert.True(t, res.Saved)
	assert.True(t, res.Reservation.Covered)

	// size = 8 teams x (1 CPU, 2Gi) + 15% buffer.
	assert.Equal(t, int64(8*1000*115/100), res.Reservation.Size.CPUMillicores)
	assert.Equal(t, int64((8*(2<<30)*115+99)/100), res.Reservation.Size.MemoryBytes)
	// window = deploy lead (30m) + readiness margin (30m) before the start .. finish + 1h tail gap.
	assert.Equal(t, time.Date(2026, 10, 5, 7, 0, 0, 0, time.UTC), res.Reservation.From)
	assert.Equal(t, time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC), res.Reservation.To)
	// 8 teams of 1.15 CPU: the fewest agents - all on the first by priority (9.2 of 10 CPU).
	require.Len(t, res.Reservation.Placement, 1)
	assert.Equal(t, 8, res.Reservation.Placement[0].Units)
	assert.Equal(t, "a", res.Reservation.Placement[0].AgentName)

	again, err := h.uc.SetEventResourceReservation(context.Background(), h.event.ID, EventReservationInput{DryRun: true, Teams: ptr(10)}, uuid.Nil)
	require.NoError(t, err)
	assert.False(t, again.Saved)
	stored, _ := h.store.GetEventReservation(context.Background(), h.event.ID)
	assert.Equal(t, 8, stored.Teams, "a dry run stores nothing")
}

func ptr[T any](v T) *T { return &v }

func TestTailGapNeverShorterAndAdminMaySetMore(t *testing.T) {
	h := newHarness(t)
	res, err := h.uc.SetEventResourceReservation(context.Background(), h.event.ID, EventReservationInput{TailGap: ptr(5 * time.Minute)}, uuid.Nil)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC), res.Reservation.To)
	res, err = h.uc.SetEventResourceReservation(context.Background(), h.event.ID, EventReservationInput{TailGap: ptr(3 * time.Hour)}, uuid.Nil)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC), res.Reservation.To)
}

func TestReservationThatDoesNotFitIsRefusedUnlessAllowed(t *testing.T) {
	h := newHarness(t)
	h.planner.need.Teams = 40 // 40 x 1.15 CPU > 2 agents of 10 CPU
	_, err := h.uc.SetEventResourceReservation(context.Background(), h.event.ID, EventReservationInput{}, uuid.Nil)
	require.Error(t, err)
	assert.True(t, is(err, calModel.ErrReservationConflict), "%v", err)
	_, getErr := h.store.GetEventReservation(context.Background(), h.event.ID)
	assert.Error(t, getErr, "nothing was stored")

	res, err := h.uc.SetEventResourceReservation(context.Background(), h.event.ID, EventReservationInput{AllowConflicts: true}, uuid.Nil)
	require.NoError(t, err)
	assert.True(t, res.Saved)
	assert.False(t, res.Reservation.Covered)
	assert.Positive(t, res.Reservation.Unplaced)
	require.NotEmpty(t, res.Conflicts)
	// A readiness alarm was raised, the admins were told.
	alarms, err := h.uc.ListResourceAlarms(context.Background(), true)
	require.NoError(t, err)
	kinds := map[calModel.AlarmKind]bool{}
	for _, a := range alarms {
		kinds[a.Kind] = true
	}
	assert.True(t, kinds[calModel.AlarmNotPlaced], "the teams without an agent")
	assert.Len(t, h.notifier.raised, len(alarms))
}

func TestNothingIsMovedWhenAReservationGrows(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{Teams: ptr(8)}, uuid.Nil)
	require.NoError(t, err)
	res, err := h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{Teams: ptr(12)}, uuid.Nil)
	require.NoError(t, err)
	require.Len(t, res.Reservation.Placement, 2)
	assert.Equal(t, "a", res.Reservation.Placement[0].AgentName, "the 8 teams stay on the first agent")
	assert.Equal(t, 8, res.Reservation.Placement[0].Units)
	assert.Equal(t, 4, res.Reservation.Placement[1].Units)
}

func TestAnotherEventOverlappingTakesTheOtherAgent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{}, uuid.Nil)
	require.NoError(t, err)
	// A second event at the same time: its 8 teams do not fit next to the first on agent a.
	other := eventModel.Event{ID: uuid.Must(uuid.NewV7()), Name: "Other", Tag: "other", InfrastructureAllowed: true, Lifecycle: h.event.Lifecycle}
	h.uc.events = multiEvents{h.event, other}
	res, err := h.uc.SetEventResourceReservation(ctx, other.ID, EventReservationInput{}, uuid.Nil)
	require.NoError(t, err)
	require.Len(t, res.Reservation.Placement, 1)
	assert.Equal(t, "b", res.Reservation.Placement[0].AgentName)
}

type multiEvents []eventModel.Event

func (m multiEvents) GetByID(_ context.Context, id uuid.UUID) (eventModel.Event, error) {
	for _, e := range m {
		if e.ID == id {
			return e, nil
		}
	}
	return eventModel.Event{}, errors.New("no event")
}

func TestChangeRequestApprovalExtendsTheReservation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	organizer := uuid.Must(uuid.NewV7())
	_, err := h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{}, uuid.Nil)
	require.NoError(t, err)

	end := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	_, err = h.uc.RequestResourceChange(ctx, h.event.ID, organizer, ChangeInput{WindowEnd: &end})
	require.Error(t, err, "a reason is required")
	view, err := h.uc.RequestResourceChange(ctx, h.event.ID, organizer, ChangeInput{WindowEnd: &end, Reason: "the event runs longer"})
	require.NoError(t, err)
	assert.Equal(t, calModel.ChangePending, view.Status)
	assert.Len(t, h.notifier.requested, 1)
	_, err = h.uc.RequestResourceChange(ctx, h.event.ID, organizer, ChangeInput{WindowEnd: &end, Reason: "again"})
	assert.True(t, is(err, calModel.ErrChangeRequestPending))

	decided, err := h.uc.DecideResourceChangeRequest(ctx, view.ID, true, "ok", uuid.Nil, false)
	require.NoError(t, err)
	assert.Equal(t, calModel.ChangeApproved, decided.Status)
	r, _ := h.store.GetEventReservation(ctx, h.event.ID)
	assert.Equal(t, end.Add(time.Hour), r.Window.End, "the 1h gap still applies on top of the longer window")
	assert.Equal(t, []bool{true}, h.notifier.decided)
	_, err = h.uc.DecideResourceChangeRequest(ctx, view.ID, false, "", uuid.Nil, false)
	assert.True(t, is(err, calModel.ErrChangeRequestDecided))

	// A rejected request changes nothing.
	size := Amount{CPUMillicores: 100000, MemoryBytes: 1 << 40}
	v2, err := h.uc.RequestResourceChange(ctx, h.event.ID, organizer, ChangeInput{Size: &size, Reason: "huge"})
	require.NoError(t, err)
	_, err = h.uc.DecideResourceChangeRequest(ctx, v2.ID, false, "no", uuid.Nil, false)
	require.NoError(t, err)
	after, _ := h.store.GetEventReservation(ctx, h.event.ID)
	assert.Equal(t, r.Size, after.Size)
}

func TestChangeApprovalThatDoesNotFitNeedsTheAdminToAllowIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{}, uuid.Nil)
	require.NoError(t, err)
	size := Amount{CPUMillicores: 90000, MemoryBytes: 100 << 30}
	v, err := h.uc.RequestResourceChange(ctx, h.event.ID, uuid.Must(uuid.NewV7()), ChangeInput{Size: &size, Reason: "growth"})
	require.NoError(t, err)
	_, err = h.uc.DecideResourceChangeRequest(ctx, v.ID, true, "", uuid.Nil, false)
	assert.True(t, is(err, calModel.ErrReservationConflict), "%v", err)
	pending := calModel.ChangePending
	list, _ := h.uc.ListResourceChangeRequests(ctx, &pending, nil)
	assert.Len(t, list, 1, "still pending after the refused approval")
	_, err = h.uc.DecideResourceChangeRequest(ctx, v.ID, true, "", uuid.Nil, true)
	require.NoError(t, err)
}

func TestOrganizerSeesAllocatedVsUsedWithoutAgents(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	empty, err := h.uc.GetEventResources(ctx, h.event.ID)
	require.NoError(t, err)
	assert.False(t, empty.Reserved)
	_, err = h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{}, uuid.Nil)
	require.NoError(t, err)
	h.usage.u = Usage{ByEvent: map[uuid.UUID]Amount{h.event.ID: {CPUMillicores: 3000, MemoryBytes: 4 << 30}}}
	v, err := h.uc.GetEventResources(ctx, h.event.ID)
	require.NoError(t, err)
	assert.True(t, v.Reserved)
	assert.True(t, v.Covered)
	assert.Equal(t, int64(3000), v.InUse.CPUMillicores)
	assert.Equal(t, v.Allocated.CPUMillicores-3000, v.Free.CPUMillicores)
}

func TestTaskOfARunningEventNeedsTheReservationForAllTeams(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	assert.NoError(t, h.uc.HoldsForAllTeams(ctx, h.event.ID, Amount{CPUMillicores: 50000}, 8, Amount{}), "no reservation, no check")
	_, err := h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{}, uuid.Nil)
	require.NoError(t, err)
	// Per-team room is 1.15 CPU / 2.3Gi.
	assert.NoError(t, h.uc.HoldsForAllTeams(ctx, h.event.ID, Amount{CPUMillicores: 1100, MemoryBytes: 2 << 30}, 8, Amount{CPUMillicores: 250}))
	err = h.uc.HoldsForAllTeams(ctx, h.event.ID, Amount{CPUMillicores: 1400, MemoryBytes: 2 << 30}, 8, Amount{})
	assert.True(t, is(err, calModel.ErrNotEnoughReserved), "does not fit for all teams: %v", err)
	err = h.uc.HoldsForAllTeams(ctx, h.event.ID, Amount{CPUMillicores: 1000, MemoryBytes: 2 << 30}, 9, Amount{})
	assert.True(t, is(err, calModel.ErrNotEnoughReserved), "more teams than reserved")
}

func TestAgentLostRaisesAlarmAndResolvesWhenBack(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{}, uuid.Nil)
	require.NoError(t, err)
	assert.Empty(t, h.notifier.raised)

	// Agent a (where the teams are) is disabled.
	h.agents.records[0].Enabled = false
	require.NoError(t, h.uc.ReconcileResourceCalendar(ctx))
	alarms, _ := h.uc.ListResourceAlarms(ctx, true)
	require.Len(t, alarms, 1)
	assert.Equal(t, calModel.AlarmAgentLost, alarms[0].Kind)
	assert.Equal(t, "a", alarms[0].AgentName)
	assert.Len(t, h.notifier.raised, 1)
	// The next pass does not raise it again.
	require.NoError(t, h.uc.ReconcileResourceCalendar(ctx))
	assert.Len(t, h.notifier.raised, 1)
	// Nothing was moved: the placement still names agent a.
	r, _ := h.store.GetEventReservation(ctx, h.event.ID)
	assert.Equal(t, uuid.UUID{15: 1}, r.Placement[0].AgentID)

	h.agents.records[0].Enabled = true
	require.NoError(t, h.uc.ReconcileResourceCalendar(ctx))
	open, _ := h.uc.ListResourceAlarms(ctx, true)
	assert.Empty(t, open)
	assert.NotEmpty(t, h.notifier.closed)
}

func TestAgentShrinkRaisesAlarm(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{}, uuid.Nil)
	require.NoError(t, err)
	h.agents.records[0].CapacityCPUMillicores = i64(4000)
	require.NoError(t, h.uc.ReconcileResourceCalendar(ctx))
	alarms, _ := h.uc.ListResourceAlarms(ctx, true)
	require.Len(t, alarms, 1)
	assert.Equal(t, calModel.AlarmAgentShrunk, alarms[0].Kind)
	tl, err := h.uc.GetResourceCalendarTimeline(ctx, now0, now0.Add(12*time.Hour))
	require.NoError(t, err)
	require.NotEmpty(t, tl.Conflicts, "the shrink shows as a conflict on the timeline")
	assert.False(t, tl.Reservations[0].Covered)
	// The admin's manual answer: re-place; the teams that no longer fit move to the other agent.
	res, err := h.uc.ReplanResourceReservation(ctx, tl.Reservations[0].ID, false)
	require.NoError(t, err)
	assert.True(t, res.Reservation.Covered)
}

func TestNotConnectedAlarmEscalatesTowardsTheLead(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{}, uuid.Nil)
	require.NoError(t, err)
	// Both agents stop answering (their capacity was read long ago); the window starts at 7:00, now is 6:00.
	long := now0.Add(-3 * time.Hour)
	h.agents.records[0].CapacitySeenAt, h.agents.records[1].CapacitySeenAt = &long, &long
	require.NoError(t, h.uc.ReconcileResourceCalendar(ctx))
	alarms, _ := h.uc.ListResourceAlarms(ctx, true)
	require.Len(t, alarms, 1)
	assert.Equal(t, calModel.AlarmNotConnected, alarms[0].Kind)
	assert.Equal(t, StageTwoHours, alarms[0].Stage)
	require.Len(t, h.notifier.raised, 1)

	h.clock = time.Date(2026, 10, 5, 7, 0, 0, 0, time.UTC) // the deploy lead
	require.NoError(t, h.uc.ReconcileResourceCalendar(ctx))
	require.Len(t, h.notifier.raised, 2, "escalated at the lead")
	assert.True(t, h.notifier.raised[1].Escalated)
	assert.Equal(t, StageAtLead, h.notifier.raised[1].Stage)
}

func TestUnplacedTeamsAreCompletedWhenCapacityAppears(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.agents.records = h.agents.records[:1]
	h.planner.need.Teams = 12 // 12 x 1.15 CPU > 10 CPU
	res, err := h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{AllowConflicts: true}, uuid.Nil)
	require.NoError(t, err)
	require.Positive(t, res.Reservation.Unplaced)
	h.agents.records = append(h.agents.records, record(2, 1, 10000, 64<<30, now0))
	require.NoError(t, h.uc.ReconcileResourceCalendar(ctx))
	r, _ := h.store.GetEventReservation(ctx, h.event.ID)
	assert.Zero(t, r.Unplaced)
	open, _ := h.uc.ListResourceAlarms(ctx, true)
	assert.Empty(t, open)
}

func TestTestLabAdmissionPoolFreeBookingAndNearestWindow(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	owner := uuid.Must(uuid.NewV7())
	lab := Amount{CPUMillicores: 2000, MemoryBytes: 4 << 30}
	_, _, err := h.uc.SetResourceTestPool(ctx, Amount{CPUMillicores: 2000, MemoryBytes: 4 << 30}, false)
	require.NoError(t, err)

	// Inside the pool.
	room, err := h.uc.AdmitTestLab(ctx, TestLabRequest{ID: uuid.Must(uuid.NewV7()), Owner: owner, Size: lab})
	require.NoError(t, err)
	assert.Equal(t, calModel.ViaPool, room.Via)
	// The pool is used up; unplanned room (event not yet started) takes the next one.
	room, err = h.uc.AdmitTestLab(ctx, TestLabRequest{ID: uuid.Must(uuid.NewV7()), Owner: owner, Size: lab})
	require.NoError(t, err)
	assert.Equal(t, calModel.ViaFree, room.Via)

	// An event takes all but the pool (18 of 20 CPU, 2 held for the pool is not enough for a lab), 7:00-13:00.
	h.planner.need.Teams = 18
	h.planner.need.PerTeam = Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30}
	_, err = h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{BufferPercent: ptr(0)}, uuid.Nil)
	require.NoError(t, err)
	_, err = h.uc.AdmitTestLab(ctx, TestLabRequest{ID: uuid.Must(uuid.NewV7()), Owner: owner, Size: lab, Lease: 2 * time.Hour})
	require.Error(t, err)
	assert.True(t, is(err, calModel.ErrNoTestLabRoom), "%v", err)
	check, err := h.uc.CheckTestLabRoom(ctx, owner, lab, Amount{}, 2*time.Hour)
	require.NoError(t, err)
	assert.False(t, check.Available)
	require.NotNil(t, check.NearestFrom)
	assert.Equal(t, time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC), *check.NearestFrom, "after the event and its tail gap")

	// The author books that window; the booking holds room in the calendar and covers the lab then.
	b, err := h.uc.BookTestLab(ctx, owner, *check.NearestFrom, 2*time.Hour, lab, Amount{})
	require.NoError(t, err)
	assert.Equal(t, 8, int(b.To.Sub(b.From)/(15*time.Minute)))
	bookings, _ := h.uc.ListTestLabBookings(ctx, owner)
	assert.Len(t, bookings, 1)
	require.NoError(t, h.uc.CancelTestLabBooking(ctx, owner, b.ID))
	bookings, _ = h.uc.ListTestLabBookings(ctx, owner)
	assert.Empty(t, bookings)
}

func TestTestLabRunsInsideTheBookedWindow(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	owner := uuid.Must(uuid.NewV7())
	lab := Amount{CPUMillicores: 2000, MemoryBytes: 4 << 30}
	_, err := h.uc.BookTestLab(ctx, owner, now0, 2*time.Hour, lab, Amount{})
	require.NoError(t, err)
	room, err := h.uc.AdmitTestLab(ctx, TestLabRequest{ID: uuid.Must(uuid.NewV7()), Owner: owner, Size: lab})
	require.NoError(t, err)
	assert.Equal(t, calModel.ViaBooking, room.Via)
	// The booking holds one lab of that size; a second one goes through the normal rules.
	room, err = h.uc.AdmitTestLab(ctx, TestLabRequest{ID: uuid.Must(uuid.NewV7()), Owner: owner, Size: lab})
	require.NoError(t, err)
	assert.Equal(t, calModel.ViaFree, room.Via)
}

func TestFutureImpactListsTheReservationsOfAnAgent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{}, uuid.Nil)
	require.NoError(t, err)
	impacts, err := h.uc.FutureImpact(ctx, uuid.UUID{15: 1}, now0)
	require.NoError(t, err)
	require.Len(t, impacts, 1)
	assert.Equal(t, h.event.ID, impacts[0].EventID)
	other, err := h.uc.FutureImpact(ctx, uuid.UUID{15: 2}, now0)
	require.NoError(t, err)
	assert.Empty(t, other)
}

func TestStatsAndCapacityShowAllocatedUsedFree(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.clock = time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	_, err := h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{}, uuid.Nil)
	require.NoError(t, err)
	h.usage.u = Usage{ByEvent: map[uuid.UUID]Amount{h.event.ID: {CPUMillicores: 2000}}, ByAgent: map[uuid.UUID]Amount{{15: 1}: {CPUMillicores: 2000}}}
	st, err := h.uc.GetResourceCalendarStats(ctx)
	require.NoError(t, err)
	require.Len(t, st.Agents, 2)
	assert.Equal(t, int64(9200), st.Agents[0].Allocated.CPUMillicores)
	assert.Equal(t, int64(800), st.Agents[0].Free.CPUMillicores)
	assert.Equal(t, int64(2000), st.Agents[0].InUse.CPUMillicores)
	assert.Equal(t, int64(10000), st.Agents[1].Free.CPUMillicores)
	require.Len(t, st.Events, 1)
	assert.Equal(t, int64(2000), st.Events[0].InUse.CPUMillicores)
	assert.True(t, st.Events[0].Covered)
	capView, err := h.uc.GetResourceCalendarCapacity(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(20000), capView.Total.CPUMillicores)
	assert.False(t, capView.PerNodeRoomReported)
}

func TestAgentsThatAreNotUsedHaveNoCapacity(t *testing.T) {
	h := newHarness(t)
	h.agents.records[0].Enabled = false
	h.agents.records[1].CapacitySeenAt = nil
	capView, err := h.uc.GetResourceCalendarCapacity(context.Background())
	require.NoError(t, err)
	assert.Zero(t, capView.Total.CPUMillicores)
	assert.Equal(t, "disabled", capView.Agents[0].Why)
	assert.Equal(t, "no_capacity", capView.Agents[1].Why)
	// With no capacity at all a reservation is refused unless allowed (it is then "not covered").
	_, err = h.uc.SetEventResourceReservation(context.Background(), h.event.ID, EventReservationInput{}, uuid.Nil)
	assert.True(t, is(err, calModel.ErrReservationConflict))
}

func TestReportedTenantQuotaLimitsTheCapacity(t *testing.T) {
	h := newHarness(t)
	h.agents.records[0].Features = &infraModel.AgentFeatures{TenantQuota: infraModel.TenantQuotaFeature{HasCPU: true, CPUMillicores: 4000}}
	h.agents.records[1].CapacityCPUMillicores, h.agents.records[1].CapacityMemoryBytes = nil, nil // no quota: no limit
	c, err := h.uc.GetResourceCalendarCapacity(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(4000), c.Agents[0].Capacity.CPUMillicores)
	assert.True(t, c.Agents[1].CPUUnlimited)
}
