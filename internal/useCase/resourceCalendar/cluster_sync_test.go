package resourceCalendarUseCase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

var (
	agentOne = uuid.UUID{15: 1}
	agentTwo = uuid.UUID{15: 2}
)

// syncHarness is the calendar harness with an agent reservation API: 12 teams of 1 CPU, so the event spans both
// agents (10 + 2).
func syncHarness(t *testing.T) (*harness, *fakePort) {
	t.Helper()
	h := newHarness(t)
	port := newFakePort()
	h.uc.SetClusterReservations(port, nil)
	return h, port
}

func setEvent(t *testing.T, h *harness, in EventReservationInput) ReservationResult {
	t.Helper()
	if in.Teams == nil {
		in.Teams = ptr(12)
	}
	res, err := h.uc.SetEventResourceReservation(context.Background(), h.event.ID, in, uuid.Nil)
	require.NoError(t, err)
	return res
}

func partsOf(t *testing.T, h *harness, calendar uuid.UUID) map[uuid.UUID]calModel.Part {
	t.Helper()
	ps, err := h.store.PartsOfCalendar(context.Background(), calendar)
	require.NoError(t, err)
	out := map[uuid.UUID]calModel.Part{}
	for _, p := range ps {
		out[p.AgentID] = p
	}
	return out
}

func TestSyncCreatesPartsForPlacedShares(t *testing.T) {
	h, port := syncHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	require.Len(t, res.Reservation.Placement, 2)
	parts := partsOf(t, h, res.Reservation.ID)
	require.Len(t, parts, 2)
	for _, sh := range res.Reservation.Placement {
		p := parts[sh.AgentID]
		held, ok := port.held[sh.AgentID][p.ID]
		require.True(t, ok, "the part is on its agent")
		// 12 CPU over 12 teams: one CPU per unit.
		assert.Equal(t, int64(sh.Units)*1000, held.Amount.CPUMillicores)
		assert.Equal(t, res.Reservation.From, held.From)
		assert.Equal(t, res.Reservation.To, held.To)
		assert.True(t, p.Synced())
	}
}

func TestSyncUpdatesOnWindowChangeKeepsID(t *testing.T) {
	h, port := syncHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	before := partsOf(t, h, res.Reservation.ID)
	later := res.Reservation.To.Add(time.Hour)
	setEvent(t, h, EventReservationInput{WindowEnd: &later})
	after := partsOf(t, h, res.Reservation.ID)
	for agent, p := range before {
		assert.Equal(t, p.ID, after[agent].ID, "extension keeps the part id")
		assert.Equal(t, later, port.held[agent][p.ID].To)
		assert.Len(t, port.held[agent], 1)
	}
	assert.Contains(t, port.calls, "update "+before[agentOne].ID.String())
}

func TestSyncDeletesOrphansOfThisInstanceOnly(t *testing.T) {
	h, port := syncHarness(t)
	setEvent(t, h, EventReservationInput{})
	// A reservation of this instance that no part names (the port lists this instance's only).
	orphan := uuid.Must(uuid.NewV7())
	port.agent(agentOne)[orphan] = infraModel.ClusterReservation{ID: orphan, From: now0, To: now0.Add(time.Hour), Amount: Amount{CPUMillicores: 1, MemoryBytes: 1}}
	require.NoError(t, h.uc.SyncAgent(context.Background(), agentOne))
	assert.NotContains(t, port.held[agentOne], orphan)
	assert.Contains(t, port.calls, "delete "+orphan.String())
	assert.Len(t, port.held[agentOne], 1, "the desired part stays")
}

func TestSyncNeverShrinksBelowUsed(t *testing.T) {
	h, port := syncHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	p := partsOf(t, h, res.Reservation.ID)[agentOne]
	held := port.held[agentOne][p.ID]
	port.running(agentOne, p.ID, 3, Amount{CPUMillicores: held.Amount.CPUMillicores - 1000, MemoryBytes: held.Amount.MemoryBytes}, calModel.PartActive)
	// The admin halves the teams: the part on agent one would shrink below what runs there.
	setEvent(t, h, EventReservationInput{Teams: ptr(6)})
	sent := port.held[agentOne][p.ID]
	assert.Equal(t, held.Amount.CPUMillicores-1000, sent.Amount.CPUMillicores, "the amount stays at what is used")
	assert.Equal(t, held.Amount.MemoryBytes, sent.Amount.MemoryBytes)
	stored := partsOf(t, h, res.Reservation.ID)[agentOne]
	assert.True(t, stored.ShrinkHeld)
	alarms, _ := h.store.ListAlarms(context.Background(), true, 10)
	kinds := map[calModel.AlarmKind]bool{}
	for _, a := range alarms {
		kinds[a.Kind] = true
	}
	assert.True(t, kinds[calModel.AlarmClusterShrinkHeld], "the held shrink raises an alarm")
}

func TestSyncNeverEndsEarlierThanNotice(t *testing.T) {
	h, port := syncHarness(t)
	h.clock = time.Date(2026, 10, 5, 9, 12, 0, 0, time.UTC) // the event runs; a window end is slot aligned (9:15)
	res := setEvent(t, h, EventReservationInput{})
	p := partsOf(t, h, res.Reservation.ID)[agentOne]
	port.running(agentOne, p.ID, 2, Amount{CPUMillicores: 1000, MemoryBytes: 1}, calModel.PartActive)
	end := h.clock.Add(time.Minute)
	setEvent(t, h, EventReservationInput{WindowEnd: &end})
	sent := port.held[agentOne][p.ID]
	assert.Equal(t, h.clock.Add(DefaultReservationNotice), sent.To, "the labs get the full notice")
}

func TestForcedShrinkIsSent(t *testing.T) {
	h, port := syncHarness(t)
	h.clock = time.Date(2026, 10, 5, 9, 12, 0, 0, time.UTC)
	res := setEvent(t, h, EventReservationInput{})
	p := partsOf(t, h, res.Reservation.ID)[agentOne]
	held := port.held[agentOne][p.ID]
	port.running(agentOne, p.ID, 3, held.Amount, calModel.PartActive)
	end := h.clock.Add(time.Minute)
	// Allowing a conflict is not forcing a shrink: the admin panel always allows conflicts.
	setEvent(t, h, EventReservationInput{Teams: ptr(6), WindowEnd: &end, AllowConflicts: true})
	assert.Equal(t, held.Amount, port.held[agentOne][p.ID].Amount, "a shrink without the admin's confirmation is held")
	setEvent(t, h, EventReservationInput{Teams: ptr(6), WindowEnd: &end, AllowConflicts: true, ForceShrink: true})
	sent := port.held[agentOne][p.ID]
	assert.Less(t, sent.Amount.CPUMillicores, held.Amount.CPUMillicores, "a forced shrink is sent")
	assert.True(t, sent.To.Before(h.clock.Add(DefaultReservationNotice)))
	assert.False(t, partsOf(t, h, res.Reservation.ID)[agentOne].ShrinkHeld)
}

func TestCancelledReservationEndsAfterNotice(t *testing.T) {
	h, port := syncHarness(t)
	h.clock = time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	res := setEvent(t, h, EventReservationInput{})
	parts := partsOf(t, h, res.Reservation.ID)
	busy, idle := parts[agentOne], parts[agentTwo]
	port.running(agentOne, busy.ID, 1, Amount{CPUMillicores: 1, MemoryBytes: 1}, calModel.PartActive)
	require.NoError(t, h.uc.DeleteEventResourceReservation(context.Background(), h.event.ID, uuid.Nil))
	// The idle part is deleted at once; the busy one gets the notice first.
	assert.NotContains(t, port.held[agentTwo], idle.ID)
	require.Contains(t, port.held[agentOne], busy.ID)
	assert.Equal(t, h.clock.Add(DefaultReservationNotice), port.held[agentOne][busy.ID].To)
	// After the notice the part is deleted and its row dropped.
	h.clock = h.clock.Add(DefaultReservationNotice + time.Second)
	require.NoError(t, h.uc.SyncAgent(context.Background(), agentOne))
	assert.NotContains(t, port.held[agentOne], busy.ID)
	_, err := h.store.GetPart(context.Background(), busy.ID)
	assert.Error(t, err)
}

func TestSyncFailureRetriedAndAlarmed(t *testing.T) {
	h, port := syncHarness(t)
	port.down[agentTwo] = errors.New("agent unreachable")
	res := setEvent(t, h, EventReservationInput{})
	p := partsOf(t, h, res.Reservation.ID)[agentTwo]
	assert.False(t, p.Synced())
	assert.Equal(t, "agent unreachable", p.SyncError)
	// Not yet alarmed: the failures are recent.
	alarms, _ := h.store.ListAlarms(context.Background(), true, 10)
	for _, a := range alarms {
		assert.NotEqual(t, calModel.AlarmClusterSyncFailed, a.Kind)
	}
	h.clock = h.clock.Add(clusterSyncAlarmAfter)
	require.Error(t, h.uc.ReconcileClusterReservations(context.Background()))
	alarms, _ = h.store.ListAlarms(context.Background(), true, 10)
	found := false
	for _, a := range alarms {
		if a.Kind == calModel.AlarmClusterSyncFailed && a.AgentID != nil && *a.AgentID == agentTwo {
			found = true
		}
	}
	assert.True(t, found, "a part unsent for 15 minutes raises the alarm")
	// The agent is back: the retry sends it and the alarm resolves.
	delete(port.down, agentTwo)
	require.NoError(t, h.uc.ReconcileClusterReservations(context.Background()))
	assert.Contains(t, port.held[agentTwo], p.ID)
	alarms, _ = h.store.ListAlarms(context.Background(), true, 10)
	for _, a := range alarms {
		assert.NotEqual(t, calModel.AlarmClusterSyncFailed, a.Kind)
	}
}

func TestOverQuotaRaisesAgentShrunk(t *testing.T) {
	h, port := syncHarness(t)
	h.uc.cluster.newID = func() uuid.UUID { return uuid.UUID{0: 0xaa, 15: 1} }
	port.refuse[uuid.UUID{0: 0xaa, 15: 1}] = infraModel.ReasonOverQuota
	setEvent(t, h, EventReservationInput{Teams: ptr(8)})
	alarms, _ := h.store.ListAlarms(context.Background(), true, 10)
	found := false
	for _, a := range alarms {
		if a.Kind == calModel.AlarmAgentShrunk {
			found = true
		}
	}
	assert.True(t, found)
}

func TestReconcileIsIdempotent(t *testing.T) {
	h, port := syncHarness(t)
	setEvent(t, h, EventReservationInput{})
	require.NoError(t, h.uc.ReconcileClusterReservations(context.Background()))
	calls := len(port.calls)
	require.NoError(t, h.uc.ReconcileClusterReservations(context.Background()))
	assert.Equal(t, calls, len(port.calls), "a second pass makes no calls")
}

func TestClampDesired(t *testing.T) {
	now := now0
	w := calModel.Window{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}
	p := calModel.Part{Window: w, Amount: Amount{CPUMillicores: 1000, MemoryBytes: 1000}}
	listed := &infraModel.ClusterReservation{From: w.Start, To: now.Add(2 * time.Hour), Amount: Amount{CPUMillicores: 4000, MemoryBytes: 4000},
		Status: &infraModel.ClusterReservationStatus{Phase: calModel.PartActive, Groups: 1, Used: Amount{CPUMillicores: 3000, MemoryBytes: 500}}}
	got, clamped := clampDesired(p, listed, 5*time.Minute, now, false)
	assert.True(t, clamped)
	assert.Equal(t, int64(3000), got.Amount.CPUMillicores)
	assert.Equal(t, int64(1000), got.Amount.MemoryBytes, "a resource above its use is sent as is")
	assert.Equal(t, w.End, got.Window.End, "an end after the notice is sent as is")
	// No groups: nothing is clamped.
	listed.Status.Groups = 0
	_, clamped = clampDesired(p, listed, 5*time.Minute, now, false)
	assert.False(t, clamped)
	// A started part keeps its start.
	p.Window.Start = now
	got, _ = clampDesired(p, listed, 5*time.Minute, now, false)
	assert.Equal(t, w.Start, got.Window.Start)
}

func TestReservationViewHasParts(t *testing.T) {
	h, port := syncHarness(t)
	port.down[agentTwo] = assert.AnError
	setEvent(t, h, EventReservationInput{})
	got, err := h.uc.GetEventResourceReservation(context.Background(), h.event.ID)
	require.NoError(t, err)
	require.Len(t, got.Reservation.Parts, 2)
	byAgent := map[uuid.UUID]PartView{}
	for _, p := range got.Reservation.Parts {
		byAgent[p.AgentID] = p
	}
	assert.True(t, byAgent[agentOne].Synced)
	assert.Equal(t, "a", byAgent[agentOne].AgentName)
	assert.False(t, byAgent[agentTwo].Synced)
	assert.Equal(t, assert.AnError.Error(), byAgent[agentTwo].SyncError)
	tl, err := h.uc.GetResourceCalendarTimeline(context.Background(), now0, now0.Add(12*time.Hour))
	require.NoError(t, err)
	require.Len(t, tl.Reservations, 1)
	assert.Len(t, tl.Reservations[0].Parts, 2)
}
