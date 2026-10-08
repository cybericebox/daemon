package resourceCalendarUseCase

import (
	"time"

	"github.com/gofrs/uuid"

	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// ShareView is the part of a reservation placed on one agent (admin only).
type ShareView struct {
	AgentID   uuid.UUID
	AgentName string
	Units     int
}

// ReservationView is one reservation for the admin timeline.
type ReservationView struct {
	PerTeamSnapshotQuotaBytes, DynamicSnapshotQuotaBytes, SizeSnapshotQuotaBytes int64
	ID                                                                           uuid.UUID
	Kind                                                                         calModel.Kind
	EventID                                                                      *uuid.UUID
	EventName                                                                    string
	EventTag                                                                     string
	OwnerID                                                                      *uuid.UUID
	From, To                                                                     time.Time

	Teams         int
	PerTeam       Amount
	LargestDevice Amount
	BufferPercent int
	Dynamic       Amount
	TailGap       time.Duration
	Size          Amount

	Placement []ShareView
	Unplaced  int
	// Covered: every team has an agent and no slot of the window is over capacity.
	Covered bool
	// Used is what the event's running objects request now (events only; zero when unknown).
	Used Amount
	// Alarms are the open readiness alarms of the reservation.
	Alarms []AlarmView
}

// AgentCapacityView is the capacity of one agent as the calendar uses it.
type AgentCapacityView struct {
	ID       uuid.UUID
	Name     string
	Priority int
	Used     bool
	// Why says why the agent is not used: disabled, below_requirements, no_capacity.
	Why       string
	Connected bool
	Capacity  Amount
	// CPUUnlimited and MemoryUnlimited: the agent puts no limit on that resource (no tenant quota).
	CPUUnlimited    bool
	MemoryUnlimited bool
	// DeviceMax is the largest device the agent can place (its limit and what it says its nodes can hold); zero is no limit.
	DeviceMax Amount
}

// CapacityView is the capacity of the agents that are used.
type CapacityView struct {
	// Total sums the limited resources of the used agents; the unlimited flags say a resource has no limit on
	// some agent (the total is then only a part).
	Total           Amount
	CPUUnlimited    bool
	MemoryUnlimited bool
	// TestPool is the guaranteed minimum for test laboratories.
	TestPool Amount
	Agents   []AgentCapacityView
}

// MaintenanceView is a maintenance window the cluster operator announced on an agent: in it the agent gives the
// platform no capacity (or Left).
type MaintenanceView struct {
	AgentID   uuid.UUID
	AgentName string
	Name      string
	Reason    string
	From      time.Time
	// To is nil for a window without an end.
	To *time.Time
	// Left is the capacity the window leaves; zero unless the operator named some.
	Left Amount
}

// SegmentView is a range of slots with one total reserved.
type SegmentView struct {
	From, To time.Time
	Reserved Amount
}

// ConflictView is a range of slots where the reservations do not fit by packing.
type ConflictView struct {
	From, To       time.Time
	ReservationIDs []uuid.UUID
	PoolShort      bool
	Unplaced       int
	Short          Amount
}

// TimelineView is the calendar over a range of slots.
type TimelineView struct {
	From, To     time.Time
	SlotMinutes  int
	Reservations []ReservationView
	Reserved     []SegmentView
	Conflicts    []ConflictView
	Capacity     CapacityView
	// Maintenance are the windows of the agents that are used which touch the range, soonest first.
	Maintenance []MaintenanceView
	// MaintenanceReported is true when every agent that is used has reported its maintenance windows (and there is
	// one); an agent that has not (an older one) may have windows the calendar does not know.
	MaintenanceReported bool
}

// ReservationResult is the outcome of setting or changing a reservation: the reservation as it would be (or is,
// when saved), where it does not fit, and whether it was kept.
type ReservationResult struct {
	Reservation ReservationView
	Conflicts   []ConflictView
	Saved       bool
}

// AlarmView is a readiness alarm.
type AlarmView struct {
	ID            uuid.UUID
	Kind          calModel.AlarmKind
	ReservationID uuid.UUID
	EventID       *uuid.UUID
	EventName     string
	EventTag      string
	AgentID       *uuid.UUID
	AgentName     string
	Units         int
	Stage         int
	Shortage      Amount
	RaisedAt      time.Time
	UpdatedAt     time.Time
	ResolvedAt    *time.Time
	AckedBy       *uuid.UUID
	AckedAt       *time.Time
}

// ChangeRequestView is a change request with its event.
type ChangeRequestView struct {
	ID            uuid.UUID
	ReservationID uuid.UUID
	EventID       uuid.UUID
	EventName     string
	EventTag      string
	RequestedBy   uuid.UUID
	RequestedAt   time.Time
	Size          *Amount
	Dynamic       *Amount
	WindowStart   *time.Time
	WindowEnd     *time.Time
	Reason        string
	Status        calModel.ChangeStatus
	DecidedBy     *uuid.UUID
	DecidedAt     *time.Time
	DecisionNote  string
	// Current is what the reservation holds now (the admin decides against it).
	CurrentSize Amount
	CurrentFrom time.Time
	CurrentTo   time.Time
}

// AgentStatView is the allocated, used and free room of one agent.
type AgentStatView struct {
	ID        uuid.UUID
	Name      string
	Priority  int
	Used      bool
	Connected bool
	Capacity  Amount
	// Allocated is what the reservations active now place on the agent; Used what the running objects request;
	// Free the capacity not allocated.
	Allocated Amount
	InUse     Amount
	Free      Amount
}

// EventStatView is the allocated, used and free room of one event.
type EventStatView struct {
	ReservationID uuid.UUID
	EventID       uuid.UUID
	EventName     string
	EventTag      string
	From, To      time.Time
	Allocated     Amount
	InUse         Amount
	Free          Amount
	Covered       bool
}

// StatsView is the calendar statistics now.
type StatsView struct {
	Observation ResourceObservation
	At          time.Time
	Agents      []AgentStatView
	Events      []EventStatView
	TestPool    Amount
	// TestLabsHeld is what the running test laboratories were admitted with (pool and free room, bookings aside).
	TestLabsHeld Amount
	// PendingChangeRequests waits for a decision; OpenAlarms for attention.
	PendingChangeRequests int64
	OpenAlarms            int
}

// OrganizerReservationView is what an organizer sees of the event's reservation: allocated and used, the
// window, whether it holds, the change requests. It never names an agent.
type OrganizerReservationView struct {
	Observation ResourceObservation
	// Reserved is false when the event has no reservation (nothing else is set then).
	Reserved  bool
	From, To  time.Time
	Teams     int
	Allocated Amount
	InUse     Amount
	Free      Amount
	// Buffer and the estimate for future dynamic tasks, which are part of Allocated.
	BufferPercent int
	Dynamic       Amount
	// Covered: the reservation is placed in full. False means the platform admin has been alerted.
	Covered bool
	// Changes are the event's change requests, newest first.
	Changes []OrganizerChangeView
}

// OrganizerChangeView is a change request as the organizer sees it.
type OrganizerChangeView struct {
	ID           uuid.UUID
	RequestedAt  time.Time
	Size         *Amount
	Dynamic      *Amount
	WindowStart  *time.Time
	WindowEnd    *time.Time
	Reason       string
	Status       calModel.ChangeStatus
	DecidedAt    *time.Time
	DecisionNote string
}

// BookingView is a test laboratory booking.
type BookingView struct {
	ID       uuid.UUID
	From, To time.Time
	Size     Amount
}

// TestLabRoom answers whether a test laboratory fits now.
type TestLabRoom struct {
	// Available is true when the laboratory can start now; Via says how (pool, free, booking).
	Available bool
	Via       string
	// NearestFrom is the start of the nearest free window when not available (nil: none within the horizon).
	NearestFrom *time.Time
}

// TestLabRequest is what a test laboratory asks for.
type TestLabRequest struct {
	ID            uuid.UUID
	Owner         uuid.UUID
	Size          Amount
	LargestDevice Amount
	// Lease is how long it will run; zero takes the default.
	Lease time.Duration
}
