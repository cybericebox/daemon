package resourceCalendar

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)

func agent(n byte, priority int, cpu int64, mem int64) Agent {
	return Agent{ID: uuid.UUID{15: n}, Name: string('a' + rune(n)), Priority: priority, Capacity: Amount{CPUMillicores: cpu, MemoryBytes: mem}}
}

func win(startHours, endHours int) Window {
	w, _ := NewWindow(t0.Add(time.Duration(startHours)*time.Hour), t0.Add(time.Duration(endHours)*time.Hour))
	return w
}

func eventRes(t *testing.T, w Window, teams int, perTeam Amount) *Reservation {
	t.Helper()
	r, err := NewEventReservation(EventInput{EventID: uuid.Must(uuid.NewV7()), Window: w, Teams: teams, PerTeam: perTeam}, uuid.Nil, t0)
	require.NoError(t, err)
	return r
}

func TestWindowAlignsOutwardToSlots(t *testing.T) {
	w, err := NewWindow(t0.Add(7*time.Minute), t0.Add(31*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, t0, w.Start)
	assert.Equal(t, t0.Add(45*time.Minute), w.End)
	assert.Equal(t, 3, w.Slots())
	_, err = NewWindow(t0, t0)
	assert.Error(t, err)
}

func TestSizeIsPlanTimesTeamsPlusBufferPlusDynamic(t *testing.T) {
	size := ComputeSize(10, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30}, 15, Amount{CPUMillicores: 500, MemoryBytes: 1 << 29})
	assert.Equal(t, int64(10*1000*115/100+500), size.CPUMillicores)
	assert.Equal(t, int64(10*(1<<30)*115/100+(1<<29)), size.MemoryBytes)
}

func TestEventWindowKeepsTheTailGap(t *testing.T) {
	end := t0.Add(5 * time.Hour)
	w, err := EventWindow(t0, end, 10*time.Minute, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, end.Add(time.Hour), w.End, "the gap is never shorter than the minimum")
	w, err = EventWindow(t0, end, 3*time.Hour, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, end.Add(3*time.Hour), w.End, "the admin may set more")
}

func TestPackingNeverSumsFreeRoomAcrossAgents(t *testing.T) {
	// 2 + 10 free is not room for one team of 12.
	a, b := agent(1, 0, 2000, 1<<40), agent(2, 1, 10000, 1<<40)
	r := eventRes(t, win(0, 4), 1, Amount{CPUMillicores: 12000, MemoryBytes: 1 << 30})
	shares, unplaced := Place([]Agent{a, b}, Load{}, r)
	assert.Empty(t, shares)
	assert.Equal(t, 1, unplaced)
}

func TestTeamStaysWholeAndFewestAgentsThenPriority(t *testing.T) {
	small, big := agent(1, 0, 3000, 1<<40), agent(2, 5, 10000, 1<<40)
	r := eventRes(t, win(0, 4), 8, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	r.Size = Amount{CPUMillicores: 8000, MemoryBytes: 8 << 30}
	shares, unplaced := Place([]Agent{small, big}, Load{}, r)
	assert.Zero(t, unplaced)
	require.Len(t, shares, 1, "fits one agent, so one agent is used even though the other has priority")
	assert.Equal(t, big.ID, shares[0].AgentID)
	assert.Equal(t, 8, shares[0].Units)

	// Equal fit: the agent first in priority order wins.
	a, b := agent(1, 0, 10000, 1<<40), agent(2, 1, 10000, 1<<40)
	shares, _ = Place([]Agent{b, a}, Load{}, r)
	require.Len(t, shares, 1)
	assert.Equal(t, a.ID, shares[0].AgentID)

	// It spills over only when it must, and never splits a team.
	r12 := eventRes(t, win(0, 4), 12, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	shares, unplaced = Place([]Agent{small, big}, Load{}, r12)
	assert.Zero(t, unplaced)
	total := 0
	for _, s := range shares {
		total += s.Units
	}
	assert.Equal(t, 12, total)
	assert.Len(t, shares, 2)
}

func TestElevatedTaskOnlyOnAgentsWhoseMaximaFit(t *testing.T) {
	low, high := agent(1, 0, 10000, 1<<40), agent(2, 1, 10000, 1<<40)
	low.DeviceMax = Amount{CPUMillicores: 250, MemoryBytes: 1 << 30}
	high.DeviceMax = Amount{CPUMillicores: 1000, MemoryBytes: 4 << 30}
	r := eventRes(t, win(0, 4), 2, Amount{CPUMillicores: 1500, MemoryBytes: 2 << 30})
	r.LargestDevice = Amount{CPUMillicores: 800, MemoryBytes: 2 << 30}
	shares, unplaced := Place([]Agent{low, high}, Load{}, r)
	assert.Zero(t, unplaced)
	require.Len(t, shares, 1)
	assert.Equal(t, high.ID, shares[0].AgentID)
}

func TestDeviceMustFitSomeNode(t *testing.T) {
	a := agent(1, 0, 10000, 1<<40)
	a.Nodes = []Amount{{CPUMillicores: 500, MemoryBytes: 1 << 30}, {CPUMillicores: 500, MemoryBytes: 1 << 30}}
	assert.True(t, a.Allows(Amount{CPUMillicores: 500, MemoryBytes: 1 << 30}))
	assert.False(t, a.Allows(Amount{CPUMillicores: 800, MemoryBytes: 1 << 30}), "1000m free in total, but no node holds one 800m device")
}

func TestPlacementRespectsOtherReservationsPeak(t *testing.T) {
	a := agent(1, 0, 10000, 1<<40)
	first := eventRes(t, win(0, 4), 8, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a}, nil, first)
	require.Zero(t, first.Unplaced)

	overlapping := eventRes(t, win(2, 6), 4, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a}, []*Reservation{first}, overlapping)
	assert.Equal(t, 2, overlapping.Unplaced, "only 2 of 4 teams fit next to the first event")

	later := eventRes(t, win(4, 6), 4, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a}, []*Reservation{first}, later)
	assert.Zero(t, later.Unplaced, "windows that do not overlap share the room")
}

func TestPlaceAllPlacesTheLargestTeamFirst(t *testing.T) {
	a, b := agent(1, 0, 10000, 1<<40), agent(2, 1, 6000, 1<<40)
	small := eventRes(t, win(0, 4), 1, Amount{CPUMillicores: 6000, MemoryBytes: 1 << 30})
	large := eventRes(t, win(0, 4), 1, Amount{CPUMillicores: 10000, MemoryBytes: 1 << 30})
	for _, r := range PlaceAll([]Agent{a, b}, []*Reservation{small, large}) {
		assert.Zero(t, r.Unplaced)
	}
}

func TestConflictWhenAgentShrinks(t *testing.T) {
	a := agent(1, 0, 10000, 1<<40)
	r := eventRes(t, win(1, 3), 8, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a}, nil, r)
	require.Zero(t, r.Unplaced)
	assert.Empty(t, FindConflicts(win(0, 6), []Agent{a}, []*Reservation{r}, Amount{}))

	a.Capacity.CPUMillicores = 5000
	conflicts := FindConflicts(win(0, 6), []Agent{a}, []*Reservation{r}, Amount{})
	require.Len(t, conflicts, 1)
	assert.Equal(t, win(1, 3), conflicts[0].Window)
	assert.Equal(t, []uuid.UUID{r.ID}, conflicts[0].Reservations)
	assert.Equal(t, int64(3000), conflicts[0].Short.CPUMillicores)

	// The agent is gone: its placement has no capacity.
	conflicts = FindConflicts(win(0, 6), nil, []*Reservation{r}, Amount{})
	require.Len(t, conflicts, 1)
}

func TestConflictForUnplacedTeamsAndForThePool(t *testing.T) {
	a := agent(1, 0, 4000, 1<<40)
	r := eventRes(t, win(0, 2), 6, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a}, nil, r)
	require.Equal(t, 2, r.Unplaced)
	conflicts := FindConflicts(win(0, 2), []Agent{a}, []*Reservation{r}, Amount{})
	require.Len(t, conflicts, 1)
	assert.Equal(t, 2, conflicts[0].Unplaced)

	// The guaranteed pool counts as demand: the rest of the room must hold it.
	full := eventRes(t, win(0, 2), 4, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a}, nil, full)
	conflicts = FindConflicts(win(0, 2), []Agent{a}, []*Reservation{full}, Amount{CPUMillicores: 500})
	require.Len(t, conflicts, 1)
	assert.True(t, conflicts[0].PoolShort)
}

func TestCanceledReservationFreesItsRoom(t *testing.T) {
	a := agent(1, 0, 4000, 1<<40)
	r := eventRes(t, win(0, 2), 4, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a}, nil, r)
	r.Cancel(t0)
	other := eventRes(t, win(0, 2), 4, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a}, []*Reservation{r}, other)
	assert.Zero(t, other.Unplaced)
}

func TestNearestFreeWindowForATestLab(t *testing.T) {
	a := agent(1, 0, 4000, 1<<40)
	busy := eventRes(t, win(0, 3), 4, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a}, nil, busy)
	size := Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30}

	from, ok := NearestFree(t0, 2*time.Hour, 48*time.Hour, []Agent{a}, []*Reservation{busy}, Amount{}, size, Amount{})
	require.True(t, ok)
	assert.Equal(t, t0.Add(3*time.Hour), from)

	// The pool stays free: a lab that would eat into it is not given the room.
	_, ok = NearestFree(t0.Add(3*time.Hour), time.Hour, 2*time.Hour, []Agent{a}, nil, Amount{CPUMillicores: 4000}, size, Amount{})
	assert.False(t, ok)

	assert.True(t, FitsWindow(win(3, 5), []Agent{a}, []*Reservation{busy}, Amount{}, size, Amount{}))
	assert.False(t, FitsWindow(win(1, 5), []Agent{a}, []*Reservation{busy}, Amount{}, size, Amount{}))
}

func TestReservedSegmentsCompressRuns(t *testing.T) {
	r1 := eventRes(t, win(0, 2), 1, Amount{CPUMillicores: 1000, MemoryBytes: 1})
	r2 := eventRes(t, win(1, 3), 1, Amount{CPUMillicores: 500, MemoryBytes: 1})
	segs := ReservedSegments(win(0, 4), []*Reservation{r1, r2})
	require.Len(t, segs, 3)
	assert.Equal(t, int64(1000), segs[0].Reserved.CPUMillicores)
	assert.Equal(t, int64(1500), segs[1].Reserved.CPUMillicores)
	assert.Equal(t, int64(500), segs[2].Reserved.CPUMillicores)
}

func TestChangeRequestAndAlarmLifecycle(t *testing.T) {
	r := eventRes(t, win(0, 2), 2, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	by := uuid.Must(uuid.NewV7())
	_, err := NewChangeRequest(r, by, nil, nil, nil, nil, "why", t0)
	assert.Error(t, err, "it must change something")
	size := Amount{CPUMillicores: 4000, MemoryBytes: 4 << 30}
	_, err = NewChangeRequest(r, by, &size, nil, nil, nil, "", t0)
	assert.Error(t, err, "it needs a reason")
	cr, err := NewChangeRequest(r, by, &size, nil, nil, nil, "more teams", t0)
	require.NoError(t, err)
	require.NoError(t, cr.Decide(true, by, "ok", t0))
	assert.Equal(t, ChangeApproved, cr.Status)
	assert.Error(t, cr.Decide(false, by, "", t0), "decided once")

	require.Error(t, r.SetSize(Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30}, t0), "not below the plan of the teams")
	require.NoError(t, r.SetSize(size, t0))

	al, err := NewAlarm(AlarmNotPlaced, r, nil, 1, Amount{}, t0)
	require.NoError(t, err)
	assert.True(t, al.Open())
	al.Resolve(t0.Add(time.Minute))
	assert.False(t, al.Open())
}

func TestBookingWindowBounds(t *testing.T) {
	_, err := NewBookingWindow(t0, 5*time.Minute, t0)
	assert.Error(t, err)
	_, err = NewBookingWindow(t0, 9*time.Hour, t0)
	assert.Error(t, err)
	_, err = NewBookingWindow(t0.Add(30*24*time.Hour), time.Hour, t0)
	assert.Error(t, err)
	w, err := NewBookingWindow(t0.Add(time.Hour), 2*time.Hour, t0)
	require.NoError(t, err)
	assert.Equal(t, 8, w.Slots())
}

func TestPlaceKeepingMovesNothingThatStillFits(t *testing.T) {
	a, b := agent(1, 0, 4000, 1<<40), agent(2, 1, 4000, 1<<40)
	r := eventRes(t, win(0, 4), 4, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a, b}, nil, r)
	require.Equal(t, []Share{{AgentID: a.ID, Units: 4}}, r.Placement)

	// The event grows to 6 teams: the 4 stay on a, only the 2 new ones go to b.
	require.NoError(t, r.Recalculate(6, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30}, Amount{}, 0, Amount{}, t0))
	PlaceKeeping([]Agent{a, b}, nil, r)
	assert.Zero(t, r.Unplaced)
	assert.Equal(t, []Share{{AgentID: a.ID, Units: 4}, {AgentID: b.ID, Units: 2}}, r.Placement)

	// Agent a shrinks: the teams that no longer fit there are placed elsewhere, the rest stay.
	a.Capacity.CPUMillicores = 2000
	PlaceKeeping([]Agent{a, b}, nil, r)
	assert.Zero(t, r.Unplaced)
	assert.Equal(t, []Share{{AgentID: a.ID, Units: 2}, {AgentID: b.ID, Units: 4}}, r.Placement)

	// An agent that is gone is not kept.
	PlaceKeeping([]Agent{b}, nil, r)
	assert.Equal(t, 2, r.Unplaced)
}

func TestCompleteUnplacedOnlyAdds(t *testing.T) {
	a := agent(1, 0, 4000, 1<<40)
	r := eventRes(t, win(0, 4), 6, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a}, nil, r)
	require.Equal(t, 2, r.Unplaced)
	assert.False(t, CompleteUnplaced([]Agent{a}, nil, r), "no room yet")

	b := agent(2, 1, 4000, 1<<40)
	assert.True(t, CompleteUnplaced([]Agent{a, b}, nil, r))
	assert.Zero(t, r.Unplaced)
	assert.Equal(t, []Share{{AgentID: a.ID, Units: 4}, {AgentID: b.ID, Units: 2}}, r.Placement)
}
