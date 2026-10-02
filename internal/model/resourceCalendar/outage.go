package resourceCalendar

import (
	"time"

	"github.com/gofrs/uuid"
)

// openEndedFor is how long a maintenance window without an end is taken to last: until the cluster operator deletes
// or edits it, which the next read shows. Far beyond any reservation.
const openEndedFor = 10 * 365 * 24 * time.Hour

// Outage is a period in which an agent gives less than its capacity: a maintenance window announced by the cluster
// operator on the agent (the platform admin never sets one). In it the agent's capacity is Left, zero unless the
// window leaves some.
type Outage struct {
	AgentID uuid.UUID
	Name    string
	Reason  string
	// Window is the window aligned outward to whole slots: a slot that overlaps the maintenance at all is affected.
	Window Window
	// OpenEnded is true when the announced window has no end (the Window then ends far ahead).
	OpenEnded bool
	// Left is the capacity the window leaves; zero by default.
	Left Amount
}

// NewOutage aligns an announced window to slots. A nil end is an open-ended window; an end that is not after the
// start is refused.
func NewOutage(agent uuid.UUID, name, reason string, from time.Time, to *time.Time, left Amount) (Outage, error) {
	end := from.Add(openEndedFor)
	if to != nil {
		end = *to
	}
	w, err := NewWindow(from, end)
	if err != nil {
		return Outage{}, err
	}
	return Outage{AgentID: agent, Name: name, Reason: reason, Window: w, OpenEnded: to == nil, Left: left}, nil
}

// CapacityAt is the capacity of the agent in the slot that starts at t: its capacity, less what the maintenance windows
// of that slot take (the smaller of the capacity and what a window leaves).
func (a Agent) CapacityAt(t time.Time) Amount {
	c := a.Capacity
	for _, o := range a.Outages {
		if o.Window.Contains(t) {
			c = c.Min(o.Left)
		}
	}
	return c
}

// CapacityOver is the least capacity the agent has in any slot of w: what a reservation of the whole window can count
// on. A window of maintenance that touches w at all counts.
func (a Agent) CapacityOver(w Window) Amount {
	c := a.Capacity
	for _, o := range a.Outages {
		if o.Window.Overlaps(w) {
			c = c.Min(o.Left)
		}
	}
	return c
}

// during is the agent as it is for a reservation of window w: its capacity is the least it has in w.
func (a Agent) during(w Window) Agent {
	if len(a.Outages) > 0 {
		a.Capacity = a.CapacityOver(w)
	}
	return a
}

// at is the agent as it is in the slot that starts at t.
func (a Agent) at(t time.Time) Agent {
	if len(a.Outages) > 0 {
		a.Capacity = a.CapacityAt(t)
	}
	return a
}

func duringAll(agents []Agent, w Window) []Agent {
	out := make([]Agent, len(agents))
	for i, a := range agents {
		out[i] = a.during(w)
	}
	return out
}
