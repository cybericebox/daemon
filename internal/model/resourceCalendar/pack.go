package resourceCalendar

import (
	"math"
	"sort"

	"github.com/gofrs/uuid"
)

// Unlimited stands for a resource an agent puts no limit on (no tenant quota).
const Unlimited int64 = math.MaxInt64 / 4

// Agent is what the calendar knows about one agent it may use: its recorded capacity (the tenant quota, else
// the cluster allocatable), its device maximum, and, when the agent reports it, the allocatable room of each
// of its nodes. The agents that are used are the enabled ones that meet the platform requirements.
type Agent struct {
	ID       uuid.UUID
	Name     string
	Priority int
	// Capacity is what the agent gives the platform; Unlimited for a resource without a limit.
	Capacity Amount
	// DeviceMax is the largest device the agent allows; zero is no limit.
	DeviceMax Amount
	// Nodes is the allocatable room of each node, when the agent reports it. Empty: the agent's per-node room
	// is not known and its whole capacity is treated as one node.
	Nodes []Amount
}

// Load is what is placed on each agent.
type Load map[uuid.UUID]Amount

// add puts a on top of the load of agent id.
func (l Load) add(id uuid.UUID, a Amount) { l[id] = l[id].Add(a) }

// Clone copies the load.
func (l Load) Clone() Load {
	out := make(Load, len(l))
	for k, v := range l {
		out[k] = v
	}
	return out
}

// Free is the room left on the agent under a load; never negative.
func (a Agent) Free(load Amount) Amount {
	return Amount{CPUMillicores: max(a.Capacity.CPUMillicores-load.CPUMillicores, 0), MemoryBytes: max(a.Capacity.MemoryBytes-load.MemoryBytes, 0)}
}

// Allows reports whether the agent can run a device of this size at all: within its device maximum and, when
// the nodes are known, on some node.
func (a Agent) Allows(device Amount) bool {
	if a.DeviceMax.CPUMillicores > 0 && device.CPUMillicores > a.DeviceMax.CPUMillicores {
		return false
	}
	if a.DeviceMax.MemoryBytes > 0 && device.MemoryBytes > a.DeviceMax.MemoryBytes {
		return false
	}
	if len(a.Nodes) == 0 {
		return true
	}
	for _, n := range a.Nodes {
		if device.Within(n) {
			return true
		}
	}
	return false
}

// unitsFit is how many equal units of this size fit into free room on the agent.
func (a Agent) unitsFit(free, slot, device Amount) int {
	if !a.Allows(device) {
		return 0
	}
	n := int64(math.MaxInt32)
	if slot.CPUMillicores > 0 {
		n = min(n, free.CPUMillicores/slot.CPUMillicores)
	}
	if slot.MemoryBytes > 0 {
		n = min(n, free.MemoryBytes/slot.MemoryBytes)
	}
	return int(n)
}

// Ordered returns the agents by priority, then name, then id: the order of placement.
func Ordered(agents []Agent) []Agent {
	out := append([]Agent(nil), agents...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID.String() < b.ID.String()
	})
	return out
}

// Place splits the units of a reservation over the agents. A unit (a team) stays whole on one agent; the
// fewest agents are used (each round takes the agent that holds the most of the remaining units, a tie goes to
// the agent first in priority order). It adds the placed room to load and returns the shares and the units no
// agent could take. Placement never sums free room across agents: "2 + 10 free" is not room for 12.
func Place(agents []Agent, load Load, r *Reservation) (shares []Share, unplaced int) {
	ordered := Ordered(agents)
	slot := r.TeamSlot()
	remaining := r.Teams
	for remaining > 0 {
		bestIdx, bestFit := -1, 0
		for i, a := range ordered {
			fit := min(a.unitsFit(a.Free(load[a.ID]), slot, r.LargestDevice), remaining)
			if fit > bestFit {
				bestIdx, bestFit = i, fit
			}
		}
		if bestIdx < 0 {
			break
		}
		id := ordered[bestIdx].ID
		load.add(id, Amount{CPUMillicores: slot.CPUMillicores * int64(bestFit), MemoryBytes: slot.MemoryBytes * int64(bestFit)})
		shares = append(shares, Share{AgentID: id, Units: bestFit})
		remaining -= bestFit
	}
	return shares, remaining
}

// PeakLoad is, per agent, the most the other reservations place on it in any slot of w. A placement valid
// against the peak holds in every slot of the window.
func PeakLoad(w Window, others []*Reservation) Load {
	n := w.Slots()
	per := map[uuid.UUID][]Amount{}
	for _, o := range others {
		inter, ok := w.Intersect(o.Window)
		if !o.Active() || !ok {
			continue
		}
		slot := o.TeamSlot()
		from, to := int(inter.Start.Sub(w.Start)/SlotDuration), int(inter.End.Sub(w.Start)/SlotDuration)
		for _, s := range o.Placement {
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
	peak := Load{}
	for id, series := range per {
		var m Amount
		for _, v := range series {
			m = m.Max(v)
		}
		peak[id] = m
	}
	return peak
}

// PlaceReservation places one reservation against the others placed in its window and stores the split.
func PlaceReservation(agents []Agent, others []*Reservation, r *Reservation) {
	load := PeakLoad(r.Window, others)
	shares, unplaced := Place(agents, load, r)
	r.Placement, r.Unplaced = shares, unplaced
}

// PlaceAll places every reservation from scratch, the largest team first (a big team is the hardest to fit).
// The admin uses it for a dry run: nothing is stored and nothing moves.
func PlaceAll(agents []Agent, all []*Reservation) []*Reservation {
	out := make([]*Reservation, 0, len(all))
	for _, r := range all {
		c := *r
		c.Placement, c.Unplaced = nil, 0
		out = append(out, &c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].TeamSlot(), out[j].TeamSlot()
		if a.CPUMillicores != b.CPUMillicores {
			return a.CPUMillicores > b.CPUMillicores
		}
		return a.MemoryBytes > b.MemoryBytes
	})
	placed := make([]*Reservation, 0, len(out))
	for _, r := range out {
		PlaceReservation(agents, placed, r)
		placed = append(placed, r)
	}
	return out
}
