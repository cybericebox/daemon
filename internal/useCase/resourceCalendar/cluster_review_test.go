package resourceCalendarUseCase

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// The end of a test run's window reaches the test labs exactly once, whatever comes first: the agent's report or
// the calendar's own sync at the end of the window.
func TestWindowEndActsOnceMonitoringFirst(t *testing.T) {
	h, port, l := observeHarness(t)
	p := testRunPart(t, h)
	h.clock = p.Window.End.Add(time.Second)
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), p.AgentID, false, []infraModel.ClusterReservation{listed(p, calModel.PartEnded, "", 0, nil)}, nil))
	port.running(p.AgentID, p.ID, 0, Amount{}, calModel.PartEnded)
	require.NoError(t, h.uc.SyncAgent(context.Background(), p.AgentID))
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), p.AgentID, false, nil, []uuid.UUID{p.ID}))
	assert.Len(t, l.ended, 1)
}

func TestWindowEndActsOnceSyncFirst(t *testing.T) {
	h, port, l := observeHarness(t)
	p := testRunPart(t, h)
	h.clock = p.Window.End.Add(time.Second)
	port.running(p.AgentID, p.ID, 0, Amount{}, calModel.PartEnded)
	// The sync retires the part whose window passed and deletes it before the agent reported the end.
	require.NoError(t, h.uc.SyncAgent(context.Background(), p.AgentID))
	require.Len(t, l.ended, 1, "the end is acted on when the row goes")
	assert.Equal(t, p.ID, l.ended[0].Part.ID)
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), p.AgentID, false, nil, []uuid.UUID{p.ID}))
	assert.Len(t, l.ended, 1, "the late deleted key repeats nothing")
}

func TestWindowEndActsAfterRestartAtTheEnd(t *testing.T) {
	h, port, _ := observeHarness(t)
	p := testRunPart(t, h)
	port.running(p.AgentID, p.ID, 0, Amount{}, calModel.PartEnded)
	// The daemon restarts at the end of the window: a new use case over the same store reconciles first.
	restarted := New(Dependencies{Store: h.store, Tx: passTx{h.store}, Agents: h.agents, Events: fakeEvents{h.event}, Configs: fakeConfigs{}, Planner: h.planner,
		Config: DefaultConfig(), Now: func() time.Time { return p.Window.End }})
	l := &fakeListener{}
	restarted.SetClusterReservations(port, nil)
	restarted.SetPartListener(l)
	require.NoError(t, restarted.ReconcileClusterReservations(context.Background()))
	require.Len(t, l.ended, 1)
	assert.Equal(t, p.ID, l.ended[0].Part.ID)
}

// A test run its author ended is retired on purpose: its deletion is not an end to act on.
func TestReleasedRunIsNotEnded(t *testing.T) {
	h, _, l := observeHarness(t)
	p := testRunPart(t, h)
	require.NoError(t, h.uc.ReleaseTestLab(context.Background(), *p.TestDeployID))
	assert.Empty(t, l.ended)
}

// A canceled reservation's parts end when the agent drops them: the stands release their labs.
func TestCanceledReservationPartEnds(t *testing.T) {
	h, _, l := observeHarness(t)
	setEvent(t, h, EventReservationInput{})
	require.NoError(t, h.uc.DeleteEventResourceReservation(context.Background(), h.event.ID, uuid.Nil))
	require.Len(t, l.ended, 2, "both idle parts are deleted at once and their end is acted on")
	assert.Equal(t, h.event.ID, *l.ended[0].EventID)
}

func TestLivePartRefusesAnEndedPart(t *testing.T) {
	h, _, _ := observeHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	team := uuid.Must(uuid.NewV7())
	ref := infraModel.ReservationRef{EventID: &h.event.ID, Unit: calModel.TeamUnit(team)}
	agent, _, err := h.uc.Resolve(context.Background(), ref, nil)
	require.NoError(t, err)
	p := partsOf(t, h, res.Reservation.ID)[agent]
	require.NoError(t, h.store.Observe(context.Background(), p.ID, calModel.PartStatus{Phase: calModel.PartEnded}))
	_, _, err = h.uc.Resolve(context.Background(), ref, nil)
	assert.ErrorIs(t, err, infraModel.ErrReservationEnded)
	// A window that has passed takes no new work either.
	require.NoError(t, h.store.Observe(context.Background(), p.ID, calModel.PartStatus{Phase: calModel.PartActive}))
	h.clock = p.Window.End
	_, _, err = h.uc.Resolve(context.Background(), ref, nil)
	assert.ErrorIs(t, err, infraModel.ErrReservationEnded)
}

func TestNoPartsMultiplyAfterTheWindow(t *testing.T) {
	h, port, _ := observeHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	h.clock = res.Reservation.To.Add(time.Minute)
	for range 3 {
		require.NoError(t, h.uc.SyncCalendarReservation(context.Background(), res.Reservation.ID))
		require.NoError(t, h.uc.ReconcileClusterReservations(context.Background()))
	}
	all, _ := h.store.PartsOfCalendars(context.Background(), []uuid.UUID{res.Reservation.ID})
	assert.Empty(t, all, "the ended parts are dropped and none is made again")
	for _, held := range port.held {
		assert.Empty(t, held)
	}
}

// A share that leaves the placement while labs run in its part keeps the part (a held shrink), until the labs are
// gone or the admin forces it.
func TestRetireWithGroupsIsHeld(t *testing.T) {
	h, port, _ := observeHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	p := partsOf(t, h, res.Reservation.ID)[agentTwo]
	require.NoError(t, h.store.Observe(context.Background(), p.ID, calModel.PartStatus{Phase: calModel.PartActive, Groups: 1}))
	port.running(agentTwo, p.ID, 1, Amount{CPUMillicores: 500, MemoryBytes: 1}, calModel.PartActive)
	// Fewer teams: everything fits the first agent and the second share goes.
	setEvent(t, h, EventReservationInput{Teams: ptr(8)})
	got, err := h.store.GetPart(context.Background(), p.ID)
	require.NoError(t, err)
	assert.False(t, got.Retired(), "a part with labs is not retired")
	assert.True(t, got.RetireHeld)
	assert.Contains(t, port.held[agentTwo], p.ID)
	alarms, _ := h.store.ListAlarms(context.Background(), true, 10)
	found := false
	for _, a := range alarms {
		found = found || a.Kind == calModel.AlarmClusterShrinkHeld
	}
	assert.True(t, found, "the held part raises the alarm")
	// The admin forces it: the part is retired (deleted after the notice while the labs run).
	setEvent(t, h, EventReservationInput{Teams: ptr(8), ForceShrink: true})
	got, err = h.store.GetPart(context.Background(), p.ID)
	require.NoError(t, err)
	assert.True(t, got.Retired())
}

func TestUnitsReleased(t *testing.T) {
	h, _, _ := observeHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	team := uuid.Must(uuid.NewV7())
	_, _, err := h.uc.Resolve(context.Background(), infraModel.ReservationRef{EventID: &h.event.ID, Unit: calModel.TeamUnit(team)}, nil)
	require.NoError(t, err)
	require.NoError(t, h.uc.ReleaseTeamUnit(context.Background(), h.event.ID, team))
	units, _ := h.store.Units(context.Background(), res.Reservation.ID)
	assert.Empty(t, units, "a destroyed group's team is placed again next time")
	// A unit left on an agent that no longer has a part of the reservation is released by the split.
	_, err = h.store.AssignUnit(context.Background(), res.Reservation.ID, calModel.TeamUnit(team), uuid.UUID{15: 9}, now0)
	require.NoError(t, err)
	require.NoError(t, h.uc.SyncCalendarReservation(context.Background(), res.Reservation.ID))
	units, _ = h.store.Units(context.Background(), res.Reservation.ID)
	assert.Empty(t, units)
}

// A part another replica creates while this one lists the agent is not taken for an orphan.
func TestOrphanDeleteSeesPartsMadeMeanwhile(t *testing.T) {
	h, port, _ := observeHarness(t)
	setEvent(t, h, EventReservationInput{})
	var made calModel.Part
	port.afterList = func() {
		port.afterList = nil
		made = calModel.NewPart(uuid.Must(uuid.NewV7()), agentOne, calModel.PartTestGroup, calModel.Window{Start: now0, End: now0.Add(time.Hour)}, Amount{CPUMillicores: 1, MemoryBytes: 1}, now0)
		owner := uuid.Must(uuid.NewV7())
		made.OwnerID = &owner
		require.NoError(t, h.store.SavePart(context.Background(), made))
		port.agent(agentOne)[made.ID] = desiredOf(made)
	}
	require.NoError(t, h.uc.SyncAgent(context.Background(), agentOne))
	assert.NotContains(t, port.calls, "delete "+made.ID.String())
}

// A group on an agent with no part of the reservation does not pin its unit there.
func TestStaleGroupDoesNotPinItsUnit(t *testing.T) {
	h, _, _ := observeHarness(t)
	res := setEvent(t, h, EventReservationInput{Teams: ptr(8)}) // one share, on agent one
	require.Len(t, res.Reservation.Placement, 1)
	stale := agentTwo
	ref := infraModel.ReservationRef{EventID: &h.event.ID, Unit: calModel.TeamUnit(uuid.Must(uuid.NewV7()))}
	agent, _, err := h.uc.Resolve(context.Background(), ref, &stale)
	require.NoError(t, err)
	assert.Equal(t, agentOne, agent, "the unit follows the placement")
	units, _ := h.store.Units(context.Background(), res.Reservation.ID)
	assert.Equal(t, agentOne, units[ref.Unit])
}

func TestEndingActsOnlyOnThePhaseChange(t *testing.T) {
	h, _, l := observeHarness(t)
	p := testRunPart(t, h)
	endsAt := p.Window.End
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), p.AgentID, false, []infraModel.ClusterReservation{listed(p, calModel.PartEnding, EndingWindowClosing, 1, &endsAt)}, nil))
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), p.AgentID, false, []infraModel.ClusterReservation{listed(p, calModel.PartEnding, EndingTenantGone, 1, &endsAt)}, nil))
	assert.Len(t, l.ending, 1, "a new reason while Ending is not a new notice")
}

func TestDriftIsNotRecorded(t *testing.T) {
	h, port, _ := observeHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	p := partsOf(t, h, res.Reservation.ID)[agentOne]
	short := listed(p, calModel.PartEnding, EndingWindowClosing, 2, nil)
	short.To = p.Window.End.Add(-time.Hour)
	port.agent(agentOne)[p.ID] = short
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), agentOne, false, []infraModel.ClusterReservation{short}, nil))
	got, err := h.store.GetPart(context.Background(), p.ID)
	require.NoError(t, err)
	assert.NotEqual(t, calModel.PartEnding, got.Status.Phase, "the notice of a drift is not kept")
}

func TestRetiredPartsOfAGoneAgentAreDropped(t *testing.T) {
	h, port, _ := observeHarness(t)
	gone := uuid.UUID{15: 7}
	p := calModel.NewPart(uuid.Must(uuid.NewV7()), gone, calModel.PartTestGroup, calModel.Window{Start: now0, End: now0.Add(time.Hour)}, Amount{CPUMillicores: 1, MemoryBytes: 1}, now0)
	owner := uuid.Must(uuid.NewV7())
	p.OwnerID = &owner
	p.Retire(now0)
	require.NoError(t, h.store.SavePart(context.Background(), p))
	port.down[gone] = assert.AnError
	_ = h.uc.ReconcileClusterReservations(context.Background())
	_, err := h.store.GetPart(context.Background(), p.ID)
	assert.Error(t, err, "the retired part of an agent gone from the registry is dropped")
}
