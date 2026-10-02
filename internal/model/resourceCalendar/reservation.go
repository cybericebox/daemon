package resourceCalendar

import (
	"time"

	"github.com/gofrs/uuid"

	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// Amount is the CPU and memory of a reservation, an agent or a team.
type Amount = resourcesModel.Amount

// Defaults of the calendar settings (the environment overrides them).
const (
	DefaultBufferPercent = 0
	DefaultTailGap       = time.Hour
	// MaxBufferPercent bounds the buffer a reservation may carry.
	MaxBufferPercent = 200
)

// Kind says what a reservation is for.
type Kind string

const (
	// KindEvent is the reservation of an event, set by a platform admin.
	KindEvent Kind = "event"
	// KindBooking is a catalog author's booked window for a test laboratory.
	KindBooking Kind = "test_booking"
)

// Share is the number of a reservation's teams (units) placed on one agent. A team is whole on one agent.
type Share struct {
	AgentID uuid.UUID
	Units   int
}

// Reservation holds lab resources over a window. The size is stored, so a manual change by the admin is
// kept; the inputs (teams, per-team plan, buffer, dynamic estimate) are kept to recompute it.
type Reservation struct {
	ID   uuid.UUID
	Kind Kind
	// EventID is set for an event reservation, OwnerID for a booking.
	EventID *uuid.UUID
	OwnerID *uuid.UUID
	Window  Window

	// Teams is the number of units the reservation is split into: the event's teams, 1 for a booking.
	Teams int
	// PerTeam is the plan of one team (its tasks plus the group's own pods); LargestDevice the largest device
	// any of its labs runs (an agent must allow it).
	PerTeam       Amount
	LargestDevice Amount
	// BufferPercent and Dynamic are what the size adds to Teams x PerTeam: the buffer and the organizer's
	// estimate for tasks that appear later.
	BufferPercent int
	Dynamic       Amount
	// TailGap is the gap kept after the event end (inside Window); informational once the window is set.
	TailGap time.Duration
	// Size is the reservation's total (what the packing splits over the agents).
	Size Amount

	// Placement is the split over agents; Unplaced the units no agent could take.
	Placement []Share
	Unplaced  int

	CreatedBy  uuid.UUID
	CreatedAt  time.Time
	UpdatedAt  time.Time
	CanceledAt *time.Time
}

// EventInput is what an event reservation is built from.
type EventInput struct {
	EventID       uuid.UUID
	Window        Window
	Teams         int
	PerTeam       Amount
	LargestDevice Amount
	BufferPercent int
	Dynamic       Amount
	TailGap       time.Duration
}

// ComputeSize is the size of a reservation: the plan of every team plus the buffer, plus the dynamic estimate.
func ComputeSize(teams int, perTeam Amount, bufferPercent int, dynamic Amount) Amount {
	base := Amount{CPUMillicores: perTeam.CPUMillicores * int64(teams), MemoryBytes: perTeam.MemoryBytes * int64(teams)}
	pct := int64(100 + bufferPercent)
	buffered := Amount{CPUMillicores: ceilDiv(base.CPUMillicores*pct, 100), MemoryBytes: ceilDiv(base.MemoryBytes*pct, 100)}
	return buffered.Add(dynamic)
}

func ceilDiv(a, b int64) int64 {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

func validInput(teams int, perTeam, dynamic Amount, bufferPercent int) error {
	if teams < 1 || perTeam.CPUMillicores < 0 || perTeam.MemoryBytes < 0 || (perTeam == Amount{}) ||
		dynamic.CPUMillicores < 0 || dynamic.MemoryBytes < 0 || bufferPercent < 0 || bufferPercent > MaxBufferPercent {
		return ErrReservationInvalid.Err()
	}
	return nil
}

// NewEventReservation builds the reservation of an event.
func NewEventReservation(in EventInput, by uuid.UUID, now time.Time) (*Reservation, error) {
	if err := validInput(in.Teams, in.PerTeam, in.Dynamic, in.BufferPercent); err != nil {
		return nil, err
	}
	if !in.Window.End.After(in.Window.Start) {
		return nil, ErrWindowInvalid.Err()
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	event := in.EventID
	return &Reservation{
		ID: id, Kind: KindEvent, EventID: &event, Window: in.Window, Teams: in.Teams, PerTeam: in.PerTeam,
		LargestDevice: in.LargestDevice, BufferPercent: in.BufferPercent, Dynamic: in.Dynamic, TailGap: in.TailGap,
		Size: ComputeSize(in.Teams, in.PerTeam, in.BufferPercent, in.Dynamic), CreatedBy: by, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// NewBooking builds an author's booking of one test laboratory: a single unit of the given size.
func NewBooking(owner uuid.UUID, window Window, size, largestDevice Amount, now time.Time) (*Reservation, error) {
	if size.CPUMillicores < 0 || size.MemoryBytes < 0 || size == (Amount{}) {
		return nil, ErrReservationInvalid.Err()
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	return &Reservation{
		ID: id, Kind: KindBooking, OwnerID: &owner, Window: window, Teams: 1, PerTeam: size, LargestDevice: largestDevice,
		Size: size, CreatedBy: owner, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// TeamSlot is the room one unit holds: the size split evenly over the teams, rounded up. The buffer and the
// dynamic estimate travel with the teams, so a team stays whole on one agent.
func (r *Reservation) TeamSlot() Amount {
	teams := int64(max(r.Teams, 1))
	return Amount{CPUMillicores: ceilDiv(r.Size.CPUMillicores, teams), MemoryBytes: ceilDiv(r.Size.MemoryBytes, teams)}
}

// Active reports whether the reservation counts: not canceled.
func (r *Reservation) Active() bool { return r.CanceledAt == nil }

// Recalculate sets the inputs and recomputes the size.
func (r *Reservation) Recalculate(teams int, perTeam, largestDevice Amount, bufferPercent int, dynamic Amount, now time.Time) error {
	if err := validInput(teams, perTeam, dynamic, bufferPercent); err != nil {
		return err
	}
	r.Teams, r.PerTeam, r.LargestDevice, r.BufferPercent, r.Dynamic = teams, perTeam, largestDevice, bufferPercent, dynamic
	r.Size = ComputeSize(teams, perTeam, bufferPercent, dynamic)
	r.UpdatedAt = now
	return nil
}

// SetSize sets the total by hand (an approved change request, an admin edit). It cannot go below the plan of
// the teams, the part the event really needs.
func (r *Reservation) SetSize(size Amount, now time.Time) error {
	need := Amount{CPUMillicores: r.PerTeam.CPUMillicores * int64(r.Teams), MemoryBytes: r.PerTeam.MemoryBytes * int64(r.Teams)}
	if size.CPUMillicores < need.CPUMillicores || size.MemoryBytes < need.MemoryBytes {
		return ErrReservationInvalid.Err()
	}
	r.Size = size
	r.UpdatedAt = now
	return nil
}

// Reschedule moves the window.
func (r *Reservation) Reschedule(w Window, now time.Time) error {
	if !w.End.After(w.Start) {
		return ErrWindowInvalid.Err()
	}
	r.Window = w
	r.UpdatedAt = now
	return nil
}

// Cancel ends the reservation; its room is free at once.
func (r *Reservation) Cancel(now time.Time) {
	if r.CanceledAt == nil {
		r.CanceledAt = &now
		r.UpdatedAt = now
	}
}

// SetPlacement stores the split over agents.
func (r *Reservation) SetPlacement(shares []Share, unplaced int, now time.Time) {
	r.Placement, r.Unplaced, r.UpdatedAt = shares, unplaced, now
}

// EventWindow is the window of an event: from the deploy lead (the caller subtracts the lead and the
// readiness margin from the start) to the event end plus the tail gap. The gap is never shorter than minTail:
// events never run back to back on the same resources.
func EventWindow(from, eventEnd time.Time, tailGap, minTail time.Duration) (Window, error) {
	if tailGap < minTail {
		tailGap = minTail
	}
	return NewWindow(from, eventEnd.Add(tailGap))
}

// ChangeStatus is the state of a change request.
type ChangeStatus string

const (
	ChangePending  ChangeStatus = "pending"
	ChangeApproved ChangeStatus = "approved"
	ChangeRejected ChangeStatus = "rejected"
)

// ChangeRequest is an organizer's request to change an event's reservation: its size, its window, the
// estimate for future dynamic tasks, with a reason. The admin approves or rejects it.
type ChangeRequest struct {
	ID            uuid.UUID
	ReservationID uuid.UUID
	EventID       uuid.UUID
	RequestedBy   uuid.UUID
	RequestedAt   time.Time
	// Each is nil when the request leaves it as it is. WindowStart / WindowEnd are the wanted window; the tail
	// gap still applies on top of an end the organizer names (it is added when approved).
	Size        *Amount
	Dynamic     *Amount
	WindowStart *time.Time
	WindowEnd   *time.Time
	Reason      string

	Status       ChangeStatus
	DecidedBy    *uuid.UUID
	DecidedAt    *time.Time
	DecisionNote string
}

// NewChangeRequest builds a pending request; it must change something and carry a reason.
func NewChangeRequest(r *Reservation, by uuid.UUID, size, dynamic *Amount, start, end *time.Time, reason string, now time.Time) (*ChangeRequest, error) {
	if reason == "" || (size == nil && dynamic == nil && start == nil && end == nil) || r.EventID == nil {
		return nil, ErrChangeRequestInvalid.Err()
	}
	if size != nil && (size.CPUMillicores < 0 || size.MemoryBytes < 0 || *size == (Amount{})) {
		return nil, ErrChangeRequestInvalid.Err()
	}
	if dynamic != nil && (dynamic.CPUMillicores < 0 || dynamic.MemoryBytes < 0) {
		return nil, ErrChangeRequestInvalid.Err()
	}
	if start != nil && end != nil && !end.After(*start) {
		return nil, ErrChangeRequestInvalid.Err()
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	return &ChangeRequest{
		ID: id, ReservationID: r.ID, EventID: *r.EventID, RequestedBy: by, RequestedAt: now,
		Size: size, Dynamic: dynamic, WindowStart: start, WindowEnd: end, Reason: reason, Status: ChangePending,
	}, nil
}

// Decide closes a pending request.
func (c *ChangeRequest) Decide(approved bool, by uuid.UUID, note string, now time.Time) error {
	if c.Status != ChangePending {
		return ErrChangeRequestDecided.Err()
	}
	c.Status = ChangeRejected
	if approved {
		c.Status = ChangeApproved
	}
	c.DecidedBy, c.DecidedAt, c.DecisionNote = &by, &now, note
	return nil
}

// AlarmKind says why a readiness alarm is raised.
type AlarmKind string

const (
	// AlarmNotPlaced: a reservation (or some of its teams) fits no agent.
	AlarmNotPlaced AlarmKind = "not_placed"
	// AlarmAgentLost: an agent a reservation is placed on is gone (deleted, disabled, below the requirements, or
	// without recorded capacity).
	AlarmAgentLost AlarmKind = "agent_lost"
	// AlarmAgentShrunk: an agent's capacity fell below what is placed on it.
	AlarmAgentShrunk AlarmKind = "agent_shrunk"
	// AlarmNotConnected: the connected capacity at the deploy lead is below the reservation.
	AlarmNotConnected AlarmKind = "not_connected"
)

// Alarm is a readiness alarm: a reservation that cannot be served as promised. It is a real entity: raised
// once per cause, visible to the admin, resolved when the cause goes away or acknowledged by an admin.
type Alarm struct {
	ID            uuid.UUID
	Kind          AlarmKind
	ReservationID uuid.UUID
	EventID       *uuid.UUID
	AgentID       *uuid.UUID
	// Units is how many teams are affected; Shortage what is missing (zero when not known). Stage is how far the
	// escalation went (0 first sight, then AlarmStage* as the deploy lead comes closer).
	Units    int
	Stage    int
	Shortage Amount
	RaisedAt time.Time
	// UpdatedAt is the last time the cause was seen.
	UpdatedAt  time.Time
	ResolvedAt *time.Time
	AckedBy    *uuid.UUID
	AckedAt    *time.Time
}

// NewAlarm raises an alarm.
func NewAlarm(kind AlarmKind, r *Reservation, agent *uuid.UUID, units int, shortage Amount, now time.Time) (*Alarm, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	return &Alarm{ID: id, Kind: kind, ReservationID: r.ID, EventID: r.EventID, AgentID: agent, Units: units, Shortage: shortage, RaisedAt: now, UpdatedAt: now}, nil
}

// Open is true until the cause goes away (an acknowledged alarm stays open while its cause lasts).
func (a *Alarm) Open() bool { return a.ResolvedAt == nil }

// Acknowledged is true once an admin saw the alarm.
func (a *Alarm) Acknowledged() bool { return a.AckedAt != nil }

// Resolve closes the alarm: its cause is gone.
func (a *Alarm) Resolve(now time.Time) {
	if a.ResolvedAt == nil {
		a.ResolvedAt, a.UpdatedAt = &now, now
	}
}

// Acknowledge records that an admin saw the alarm.
func (a *Alarm) Acknowledge(by uuid.UUID, now time.Time) {
	if a.AckedAt == nil {
		a.AckedBy, a.AckedAt, a.UpdatedAt = &by, &now, now
	}
}

// Settings are the calendar settings an admin edits.
type Settings struct {
	// TestPool is the guaranteed minimum for test laboratories, always on and never reserved by events.
	TestPool  Amount
	UpdatedAt time.Time
}

// Booking limits of a test laboratory window.
const (
	MinBooking      = SlotDuration
	MaxBooking      = 8 * time.Hour
	MaxBookingAhead = 14 * 24 * time.Hour
)

// NewBookingWindow validates the window an author books.
func NewBookingWindow(start time.Time, duration time.Duration, now time.Time) (Window, error) {
	if duration < MinBooking || duration > MaxBooking {
		return Window{}, ErrBookingInvalid.Err()
	}
	w, err := NewWindow(start, start.Add(duration))
	if err != nil {
		return Window{}, ErrBookingInvalid.Err()
	}
	if w.Start.Before(SlotFloor(now)) || w.Start.After(now.Add(MaxBookingAhead)) {
		return Window{}, ErrBookingInvalid.Err()
	}
	return w, nil
}
