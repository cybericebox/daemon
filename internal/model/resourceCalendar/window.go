// Package resourceCalendar is the platform's resource calendar: reservations of lab resources over 15-minute
// slots, the packing that decides whether they fit the agents, and the placement of a reservation over agents.
// Reservations live only in the backend; the agent holds none, only the tenant quota the capacity comes from.
package resourceCalendar

import (
	"time"
)

// SlotDuration is the grain of the calendar.
const SlotDuration = 15 * time.Minute

// SlotFloor is the start of the slot that contains t.
func SlotFloor(t time.Time) time.Time { return t.UTC().Truncate(SlotDuration) }

// SlotCeil is the first slot boundary at or after t.
func SlotCeil(t time.Time) time.Time {
	f := SlotFloor(t)
	if f.Equal(t.UTC()) {
		return f
	}
	return f.Add(SlotDuration)
}

// Window is a half-open range of whole slots [Start, End).
type Window struct {
	Start time.Time
	End   time.Time
}

// NewWindow aligns the range outward to whole slots (the start down, the end up). It fails for an empty or
// reversed range.
func NewWindow(start, end time.Time) (Window, error) {
	w := Window{Start: SlotFloor(start), End: SlotCeil(end)}
	if !w.End.After(w.Start) {
		return Window{}, ErrWindowInvalid.Err()
	}
	return w, nil
}

// Slots is the number of slots in the window.
func (w Window) Slots() int { return int(w.End.Sub(w.Start) / SlotDuration) }

// Duration is the length of the window.
func (w Window) Duration() time.Duration { return w.End.Sub(w.Start) }

// At is the start of slot i of the window.
func (w Window) At(i int) time.Time { return w.Start.Add(time.Duration(i) * SlotDuration) }

// Contains reports whether the slot that starts at t lies in the window.
func (w Window) Contains(t time.Time) bool { return !t.Before(w.Start) && t.Before(w.End) }

// Overlaps reports whether the two windows share a slot.
func (w Window) Overlaps(o Window) bool { return w.Start.Before(o.End) && o.Start.Before(w.End) }

// Intersect is the part both windows share; ok is false when there is none.
func (w Window) Intersect(o Window) (Window, bool) {
	r := Window{Start: w.Start, End: w.End}
	if o.Start.After(r.Start) {
		r.Start = o.Start
	}
	if o.End.Before(r.End) {
		r.End = o.End
	}
	return r, r.End.After(r.Start)
}
