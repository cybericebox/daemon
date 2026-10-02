package resourceCalendar

import (
	"sort"
	"time"

	"github.com/gofrs/uuid"
)

// slotLoads is the load of each agent in each slot of a window, from the placements of the reservations.
func slotLoads(w Window, rs []*Reservation) map[uuid.UUID][]Amount {
	n := w.Slots()
	per := map[uuid.UUID][]Amount{}
	for _, r := range rs {
		inter, ok := w.Intersect(r.Window)
		if !r.Active() || !ok {
			continue
		}
		slot := r.TeamSlot()
		from, to := int(inter.Start.Sub(w.Start)/SlotDuration), int(inter.End.Sub(w.Start)/SlotDuration)
		for _, s := range r.Placement {
			series := per[s.AgentID]
			if series == nil {
				series = make([]Amount, n)
				per[s.AgentID] = series
			}
			add := Amount{CPUMillicores: slot.CPUMillicores * int64(s.Units), MemoryBytes: slot.MemoryBytes * int64(s.Units)}
			for i := from; i < to; i++ {
				series[i] = series[i].Add(add)
			}
		}
	}
	return per
}

// Conflict is a range of slots where the invariant "the reservations fit the capacity by packing" breaks:
// an agent holds more than its capacity (it shrank, or was removed), a team has no agent, or the guaranteed
// test pool no longer fits. Nothing is moved automatically: the admin resolves it.
type Conflict struct {
	Window Window
	// Reservations are the ones in the conflict; PoolShort is set when the test pool does not fit.
	Reservations []uuid.UUID
	PoolShort    bool
	// Unplaced is the most teams without an agent at once; Short the most room missing at once.
	Unplaced int
	Short    Amount
}

// FindConflicts checks every slot of w. The check is the packing the reservations already carry (each team on
// one agent) against the capacity of the agents; free room is never summed across agents, except for the
// divisible test pool, which is laboratories of any agent.
func FindConflicts(w Window, agents []Agent, rs []*Reservation, pool Amount) []Conflict {
	n := w.Slots()
	loads := slotLoads(w, rs)
	capOf := map[uuid.UUID]Amount{}
	for _, a := range agents {
		capOf[a.ID] = a.Capacity
	}
	type slotState struct {
		ids       []uuid.UUID
		poolShort bool
		unplaced  int
		short     Amount
	}
	states := make([]slotState, n)
	for i := 0; i < n; i++ {
		var st slotState
		over := map[uuid.UUID]bool{}
		var free Amount
		for _, a := range agents {
			var l Amount
			if series := loads[a.ID]; series != nil {
				l = series[i]
			}
			free = addSat(free, a.Free(l))
			if l.CPUMillicores > a.Capacity.CPUMillicores || l.MemoryBytes > a.Capacity.MemoryBytes {
				over[a.ID] = true
				st.short = st.short.Add(Amount{CPUMillicores: max(l.CPUMillicores-a.Capacity.CPUMillicores, 0), MemoryBytes: max(l.MemoryBytes-a.Capacity.MemoryBytes, 0)})
			}
		}
		for id, series := range loads {
			if _, known := capOf[id]; !known && (series[i] != Amount{}) {
				over[id] = true
				st.short = st.short.Add(series[i])
			}
		}
		slotTime := w.At(i)
		for _, r := range rs {
			if !r.Active() || !r.Window.Contains(slotTime) {
				continue
			}
			if r.Unplaced > 0 {
				st.ids = append(st.ids, r.ID)
				st.unplaced += r.Unplaced
				slot := r.TeamSlot()
				st.short = st.short.Add(Amount{CPUMillicores: slot.CPUMillicores * int64(r.Unplaced), MemoryBytes: slot.MemoryBytes * int64(r.Unplaced)})
				continue
			}
			for _, s := range r.Placement {
				if over[s.AgentID] {
					st.ids = append(st.ids, r.ID)
					break
				}
			}
		}
		if pool != (Amount{}) && (free.CPUMillicores < pool.CPUMillicores || free.MemoryBytes < pool.MemoryBytes) {
			st.poolShort = true
			st.short = st.short.Add(Amount{CPUMillicores: max(pool.CPUMillicores-free.CPUMillicores, 0), MemoryBytes: max(pool.MemoryBytes-free.MemoryBytes, 0)})
		}
		sort.Slice(st.ids, func(a, b int) bool { return st.ids[a].String() < st.ids[b].String() })
		states[i] = st
	}
	var out []Conflict
	for i := 0; i < n; i++ {
		st := states[i]
		if len(st.ids) == 0 && !st.poolShort {
			continue
		}
		last := &Conflict{}
		if len(out) > 0 {
			last = &out[len(out)-1]
		}
		if len(out) > 0 && last.Window.End.Equal(w.At(i)) && last.PoolShort == st.poolShort && sameIDs(last.Reservations, st.ids) {
			last.Window.End = w.At(i + 1)
			last.Unplaced = max(last.Unplaced, st.unplaced)
			last.Short = last.Short.Max(st.short)
			continue
		}
		out = append(out, Conflict{Window: Window{Start: w.At(i), End: w.At(i + 1)}, Reservations: st.ids, PoolShort: st.poolShort, Unplaced: st.unplaced, Short: st.short})
	}
	return out
}

func sameIDs(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Segment is a range of slots with one reserved total.
type Segment struct {
	Window   Window
	Reserved Amount
}

// ReservedSegments is the total reserved over the slots of w (every reservation, any agent), run-length
// compressed; ranges with nothing reserved are left out.
func ReservedSegments(w Window, rs []*Reservation) []Segment {
	n := w.Slots()
	totals := make([]Amount, n)
	for _, r := range rs {
		inter, ok := w.Intersect(r.Window)
		if !r.Active() || !ok {
			continue
		}
		from, to := int(inter.Start.Sub(w.Start)/SlotDuration), int(inter.End.Sub(w.Start)/SlotDuration)
		for i := from; i < to; i++ {
			totals[i] = totals[i].Add(r.Size)
		}
	}
	var out []Segment
	for i := 0; i < n; i++ {
		if totals[i] == (Amount{}) {
			continue
		}
		if k := len(out); k > 0 && out[k-1].Window.End.Equal(w.At(i)) && out[k-1].Reserved == totals[i] {
			out[k-1].Window.End = w.At(i + 1)
			continue
		}
		out = append(out, Segment{Window: Window{Start: w.At(i), End: w.At(i + 1)}, Reserved: totals[i]})
	}
	return out
}

// NearestFree finds the earliest start, at or after from, of a window of the given length in which a single
// unit of this size fits one agent in every slot, while the guaranteed test pool stays free. It looks at most
// horizon ahead. ok is false when there is none.
func NearestFree(from time.Time, length, horizon time.Duration, agents []Agent, rs []*Reservation, pool, size, device Amount) (time.Time, bool) {
	from = SlotCeil(from)
	span, err := NewWindow(from, from.Add(horizon+length))
	if err != nil {
		return time.Time{}, false
	}
	need := int(length / SlotDuration)
	if need < 1 {
		need = 1
	}
	loads := slotLoads(span, rs)
	run := 0
	for i := 0; i < span.Slots(); i++ {
		if slotHolds(i, agents, loads, pool, size, device) {
			run++
			if run == need {
				return span.At(i - need + 1), true
			}
		} else {
			run = 0
		}
	}
	return time.Time{}, false
}

// FitsWindow reports whether a single unit fits one agent in every slot of w with the test pool kept free.
func FitsWindow(w Window, agents []Agent, rs []*Reservation, pool, size, device Amount) bool {
	loads := slotLoads(w, rs)
	for i := 0; i < w.Slots(); i++ {
		if !slotHolds(i, agents, loads, pool, size, device) {
			return false
		}
	}
	return true
}

func slotHolds(i int, agents []Agent, loads map[uuid.UUID][]Amount, pool, size, device Amount) bool {
	var free Amount
	fits := false
	for _, a := range agents {
		var l Amount
		if series := loads[a.ID]; series != nil {
			l = series[i]
		}
		f := a.Free(l)
		free = addSat(free, f)
		if a.Allows(device) && size.Within(f) {
			fits = true
		}
	}
	rest := Amount{CPUMillicores: free.CPUMillicores - size.CPUMillicores, MemoryBytes: free.MemoryBytes - size.MemoryBytes}
	return fits && pool.Within(rest)
}

// Slack is the room left in every slot of w on one agent, at the least: capacity minus the peak load.
func Slack(w Window, a Agent, rs []*Reservation) Amount {
	peak := PeakLoad(w, rs)
	return a.Free(peak[a.ID])
}

// addSat adds two amounts without passing Unlimited, so a few unlimited agents never overflow.
func addSat(a, b Amount) Amount {
	return Amount{CPUMillicores: min(a.CPUMillicores+b.CPUMillicores, Unlimited), MemoryBytes: min(a.MemoryBytes+b.MemoryBytes, Unlimited)}
}
