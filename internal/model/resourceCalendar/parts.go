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
	// SyncError and SyncReason (the agent's machine code) say why the last send failed; SyncFailedSince is when
	// the failures started (nil once the agent confirmed).
	SyncError       string
	SyncReason      string
	SyncFailedSince *time.Time
	// ShrinkHeld: the last send held back a shrink because labs still run in the part.
	ShrinkHeld bool
	// Forced: the desired state was forced by an admin past the shrink guard.
	Forced bool
	// RetireHeld: the part is no longer in the placement, but labs still run in it, so it stays until they are gone
	// (or the admin forces the change, or the reservation is canceled).
	RetireHeld bool

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

// SyncAnswer is the agent's answer for one revision of a part.
type SyncAnswer struct {
	OK  bool
	Rev int64
	// Error and Reason describe a failure.
	Error, Reason string
	ShrinkHeld    bool
	At            time.Time
}

// Answer applies the agent's answer: an OK for an older revision leaves a newer one unsynced; a failure keeps the
// part unsynced (it is retried) and keeps the time the failures started.
func (p *Part) Answer(a SyncAnswer) {
	if a.OK {
		p.SyncedRev = max(p.SyncedRev, a.Rev)
		p.SyncedAt = &a.At
		p.SyncFailedSince = nil
	} else if p.SyncFailedSince == nil {
		p.SyncFailedSince = &a.At
	}
	p.SyncError, p.SyncReason, p.ShrinkHeld = a.Error, a.Reason, a.ShrinkHeld
	p.UpdatedAt = a.At
}

// MarkSynced records the agent's OK for a revision.
func (p *Part) MarkSynced(rev int64, now time.Time) {
	p.Answer(SyncAnswer{OK: true, Rev: rev, At: now})
}

// MarkSyncFailed records why the agent did not take the part.
func (p *Part) MarkSyncFailed(reason string, now time.Time) {
	p.Answer(SyncAnswer{Error: reason, At: now})
}

// SyncFailingFor reports whether the sends have failed for at least d.
func (p *Part) SyncFailingFor(d time.Duration, now time.Time) bool {
	return p.SyncFailedSince != nil && !now.Before(p.SyncFailedSince.Add(d))
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
	// A reservation whose window has passed gets no new part: its existing parts end with the window (the sync
	// retires them), so nothing is created again behind them.
	if r.Active() && !r.Window.End.After(now) {
		return existing, nil, nil
	}
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
				p.RetireHeld = false
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
	preview := strings.HasPrefix(unit, previewUnitPrefix)
	used := map[uuid.UUID]int{}
	for u, a := range assigned {
		if !strings.HasPrefix(u, previewUnitPrefix) {
			used[a]++
		}
	}
	for _, sh := range shares {
		if (preview && sh.Units > 0) || (!preview && used[sh.AgentID] < sh.Units) {
			return sh.AgentID, nil
		}
	}
	return uuid.Nil, ErrUnitNotPlaced.Err()
}
