package resourceCalendar

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withOutage(t *testing.T, a Agent, from, to *time.Time, left Amount) Agent {
	t.Helper()
	o, err := NewOutage(a.ID, "kernel", "upgrade", *from, to, left)
	require.NoError(t, err)
	a.Outages = append(a.Outages, o)
	return a
}

func at(h int) *time.Time { v := t0.Add(time.Duration(h) * time.Hour); return &v }

func TestOutageIsAlignedToSlotsAndOpenEndedHasNoEnd(t *testing.T) {
	from := t0.Add(7 * time.Minute)
	to := t0.Add(31 * time.Minute)
	o, err := NewOutage(agent(1, 0, 1, 1).ID, "k", "r", from, &to, Amount{})
	require.NoError(t, err)
	assert.Equal(t, t0, o.Window.Start)
	assert.Equal(t, t0.Add(45*time.Minute), o.Window.End, "a slot the maintenance touches is affected")
	assert.False(t, o.OpenEnded)
	open, err := NewOutage(agent(1, 0, 1, 1).ID, "k", "r", from, nil, Amount{})
	require.NoError(t, err)
	assert.True(t, open.OpenEnded)
	assert.True(t, open.Window.End.After(t0.Add(365*24*time.Hour)))
	_, err = NewOutage(agent(1, 0, 1, 1).ID, "k", "r", t0, &t0, Amount{})
	assert.Error(t, err, "a window that ends where it starts is no window")
}

func TestCapacityInAWindowIsWhatItLeaves(t *testing.T) {
	a := withOutage(t, agent(1, 0, 10000, 1<<40), at(2), at(4), Amount{})
	full := Amount{CPUMillicores: 10000, MemoryBytes: 1 << 40}
	assert.Equal(t, full, a.CapacityAt(t0.Add(time.Hour)))
	assert.Equal(t, Amount{}, a.CapacityAt(t0.Add(2*time.Hour)), "zero in the window")
	assert.Equal(t, full, a.CapacityAt(t0.Add(4*time.Hour)), "the end is open")
	assert.Equal(t, Amount{}, a.CapacityOver(win(0, 3)), "touching it is enough")
	assert.Equal(t, full, a.CapacityOver(win(0, 2)))

	left := withOutage(t, agent(1, 0, 10000, 1<<40), at(2), at(4), Amount{CPUMillicores: 4000, MemoryBytes: 1 << 30})
	assert.Equal(t, Amount{CPUMillicores: 4000, MemoryBytes: 1 << 30}, left.CapacityAt(t0.Add(3*time.Hour)))
	big := withOutage(t, agent(1, 0, 2000, 1<<20), at(2), at(4), Amount{CPUMillicores: 4000, MemoryBytes: 1 << 30})
	assert.Equal(t, Amount{CPUMillicores: 2000, MemoryBytes: 1 << 20}, big.CapacityAt(t0.Add(3*time.Hour)), "a window never gives more than the agent has")
}

// A reservation whose window touches a maintenance window goes to an agent without one; with none it is not placed.
func TestPlacementAvoidsAgentsInMaintenance(t *testing.T) {
	a := withOutage(t, agent(1, 0, 10000, 1<<40), at(3), at(5), Amount{})
	b := agent(2, 1, 10000, 1<<40)
	r := eventRes(t, win(0, 4), 2, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a, b}, nil, r)
	require.Len(t, r.Placement, 1)
	assert.Equal(t, b.ID, r.Placement[0].AgentID, "the first agent by priority is in maintenance")
	assert.Zero(t, r.Unplaced)

	later := eventRes(t, win(6, 8), 2, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a, b}, nil, later)
	assert.Equal(t, a.ID, later.Placement[0].AgentID, "after the window the agent is the first again")

	alone := eventRes(t, win(0, 4), 2, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{a}, nil, alone)
	assert.Equal(t, 2, alone.Unplaced)
}

// A window announced after a reservation was placed shows as a conflict in the slots of the window only.
func TestMaintenanceAnnouncedLaterIsAConflictInItsSlots(t *testing.T) {
	plain := agent(1, 0, 10000, 1<<40)
	r := eventRes(t, win(0, 6), 2, Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30})
	PlaceReservation([]Agent{plain}, nil, r)
	require.Zero(t, r.Unplaced)
	assert.Empty(t, FindConflicts(win(0, 6), []Agent{plain}, []*Reservation{r}, Amount{}))

	hit := withOutage(t, plain, at(2), at(3), Amount{})
	got := FindConflicts(win(0, 6), []Agent{hit}, []*Reservation{r}, Amount{})
	require.Len(t, got, 1)
	assert.Equal(t, t0.Add(2*time.Hour), got[0].Window.Start)
	assert.Equal(t, t0.Add(3*time.Hour), got[0].Window.End)
	assert.Equal(t, []Reservation{*r}[0].ID, got[0].Reservations[0])
	assert.Equal(t, int64(2000), got[0].Short.CPUMillicores, "the whole load of the two teams is short")

	// A window that leaves enough is no conflict.
	enough := withOutage(t, plain, at(2), at(3), Amount{CPUMillicores: 5000, MemoryBytes: 1 << 40})
	assert.Empty(t, FindConflicts(win(0, 6), []Agent{enough}, []*Reservation{r}, Amount{}))
}

// The guaranteed pool and the nearest free window of a test laboratory see the window too.
func TestTestLabRoomSkipsMaintenance(t *testing.T) {
	a := withOutage(t, agent(1, 0, 4000, 1<<40), at(0), at(2), Amount{})
	size := Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30}
	from, ok := NearestFree(t0, time.Hour, 24*time.Hour, []Agent{a}, nil, Amount{}, size, size)
	require.True(t, ok)
	assert.Equal(t, t0.Add(2*time.Hour), from, "the first slot after the window")
	assert.False(t, FitsWindow(win(1, 3), []Agent{a}, nil, Amount{}, size, size))
	assert.True(t, FitsWindow(win(2, 3), []Agent{a}, nil, Amount{}, size, size))
}
