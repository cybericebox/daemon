package resourceCalendar

import (
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

// PartSource says what a cluster reservation part comes from.
type PartSource string

const (
	// PartCalendar is one agent's share of a calendar reservation (an event or a booking).
	PartCalendar PartSource = "calendar"
	// PartTestRun is one test laboratory admitted by the pool or free room: its lab, from the start to the end of
	// its lease.
	PartTestRun PartSource = "test_run"
	// PartTestGroup is the shared group of an author's test laboratories on one agent (its VPN and gateway).
	PartTestGroup PartSource = "test_group"
)

// Unit keys of a calendar reservation: a unit is placed whole on one agent.
const (
	// UnitBooking is the single unit of a booking.
	UnitBooking       = "booking"
	teamUnitPrefix    = "team:"
	previewUnitPrefix = "preview:"
)

// TeamUnit is the unit of a team of an event.
func TeamUnit(team uuid.UUID) string { return teamUnitPrefix + team.String() }

// PreviewUnit is the unit of an organizer's preview group inside an event reservation.
func PreviewUnit(owner uuid.UUID) string { return previewUnitPrefix + owner.String() }

// Phases of a cluster reservation as the agent reports them.
const (
	PartPending = "Pending"
	PartActive  = "Active"
	PartEnding  = "Ending"
	PartEnded   = "Ended"
)

// PartStatus is what the agent last reported about a part.
type PartStatus struct {
	Phase string
	// NoticeAt is when the ending notice came; EndsAt when the groups under the part are deleted.
	NoticeAt     *time.Time
	EndsAt       *time.Time
	EndingReason string
	Used         Amount
	Groups       int
	Exceeded     bool
	ObservedAt   *time.Time
}

// Part is one cluster reservation on one agent: the backend's reservation split per agent (1:N), a test run or
// an author's test group. Its id is what the agent knows and stays the same when the part is extended.
type Part struct {
	ID      uuid.UUID
	AgentID uuid.UUID
	Source  PartSource
	// CalendarID is set for PartCalendar, TestDeployID for PartTestRun, OwnerID for PartTestGroup.
	CalendarID   *uuid.UUID
	TestDeployID *uuid.UUID
	OwnerID      *uuid.UUID
	Window       Window
	Amount       Amount

	// DesiredRev counts the changes of the desired state; SyncedRev is the last one the agent confirmed.
	DesiredRev int64
	SyncedRev  int64
	SyncedAt   *time.Time
	SyncError  string

	Status PartStatus

	// RetireAt is set once the part is no longer desired: it is deleted on the agent at the next sync and the
	// row is dropped once the agent confirmed.
	RetireAt *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewPart builds a part that is not on the agent yet.
func NewPart(id, agent uuid.UUID, source PartSource, w Window, amount Amount, now time.Time) Part {
	return Part{ID: id, AgentID: agent, Source: source, Window: w, Amount: amount, DesiredRev: 1, CreatedAt: now, UpdatedAt: now}
}

// Synced is true when the agent confirmed the last desired state.
func (p *Part) Synced() bool { return p.SyncedRev >= p.DesiredRev }

// Retired is true once the part is no longer desired.
func (p *Part) Retired() bool { return p.RetireAt != nil }

// SetDesired sets the window and the amount; a change is a new revision to send.
func (p *Part) SetDesired(w Window, amount Amount, now time.Time) {
	if p.Window == w && p.Amount == amount {
		return
	}
	p.Window, p.Amount = w, amount
	p.DesiredRev++
	p.UpdatedAt = now
}

// Retire marks the part as no longer desired.
func (p *Part) Retire(now time.Time) {
	if p.RetireAt == nil {
		p.RetireAt = &now
		p.DesiredRev++
		p.UpdatedAt = now
	}
}

// MarkSynced records the agent's OK for a revision; an answer for an older revision leaves a newer one unsynced.
func (p *Part) MarkSynced(rev int64, now time.Time) {
	if rev > p.SyncedRev {
		p.SyncedRev = rev
	}
	if p.Synced() {
		p.SyncError = ""
	}
	p.SyncedAt = &now
	p.UpdatedAt = now
}

// MarkSyncFailed records why the agent did not take the part; it stays unsynced and is retried.
func (p *Part) MarkSyncFailed(reason string, now time.Time) {
	p.SyncError = reason
	p.UpdatedAt = now
}

// Observe stores what the agent reported.
func (p *Part) Observe(s PartStatus) { p.Status = s }

// Has reports whether the part still has groups running under it, as the agent last said.
func (p *Part) Has() bool { return p.Status.Groups > 0 }

// shareAmount is one agent's part of a reservation's size: ceil(size x units / teams) per resource.
func shareAmount(r *Reservation, units int) Amount {
	teams := int64(max(r.Teams, 1))
	u := int64(units)
	return Amount{CPUMillicores: ceilDiv(r.Size.CPUMillicores*u, teams), MemoryBytes: ceilDiv(r.Size.MemoryBytes*u, teams)}
}

// SplitCalendar splits a calendar reservation over its placement: one part per share with units > 0, sized
// ceil(size x units / teams), over the reservation's window. existing are the parts the reservation has now (not
// retired); a part on an agent that still has a share keeps its id and is updated in place (keep), a new share
// gets a new part (add), and a part whose share went to zero, or every part of a canceled reservation, is retired.
func SplitCalendar(r *Reservation, existing []Part, newID func() uuid.UUID, now time.Time) (keep, add, retire []Part) {
	byAgent := map[uuid.UUID]int{}
	for i, p := range existing {
		byAgent[p.AgentID] = i
	}
	wanted := map[uuid.UUID]bool{}
	if r.Active() {
		for _, sh := range r.Placement {
			if sh.Units <= 0 || wanted[sh.AgentID] {
				continue
			}
			wanted[sh.AgentID] = true
			amount := shareAmount(r, sh.Units)
			if i, ok := byAgent[sh.AgentID]; ok {
				p := existing[i]
				p.SetDesired(r.Window, amount, now)
				keep = append(keep, p)
				continue
			}
			p := NewPart(newID(), sh.AgentID, PartCalendar, r.Window, amount, now)
			id := r.ID
			p.CalendarID = &id
			add = append(add, p)
		}
	}
	for _, p := range existing {
		if !wanted[p.AgentID] {
			p.Retire(now)
			retire = append(retire, p)
		}
	}
	return keep, add, retire
}

// AssignUnit picks the agent of a unit of a calendar reservation. An existing assignment stays (a team never
// moves while its group exists); otherwise the first share, in placement order, that still has a free unit. A
// preview owner is not one of the reservation's teams: it takes no unit and goes to the first share.
func AssignUnit(shares []Share, assigned map[string]uuid.UUID, unit string) (uuid.UUID, error) {
	if a, ok := assigned[unit]; ok {
		return a, nil
	}
	if strings.HasPrefix(unit, previewUnitPrefix) {
		for _, sh := range shares {
			if sh.Units > 0 {
				return sh.AgentID, nil
			}
		}
		return uuid.Nil, ErrUnitNotPlaced.Err()
	}
	used := map[uuid.UUID]int{}
	for u, a := range assigned {
		if !strings.HasPrefix(u, previewUnitPrefix) {
			used[a]++
		}
	}
	for _, sh := range shares {
		if used[sh.AgentID] < sh.Units {
			return sh.AgentID, nil
		}
	}
	return uuid.Nil, ErrUnitNotPlaced.Err()
}
