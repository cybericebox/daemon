package resourceCalendar

import (
	"time"

	"github.com/gofrs/uuid"

	calUseCase "github.com/cybericebox/daemon/internal/useCase/resourceCalendar"
)

// amountDTO is CPU and memory.
type amountDTO struct {
	CPUMillicores int64 `json:"CPUMillicores"`
	MemoryBytes   int64 `json:"MemoryBytes"`
}

func amount(a calUseCase.Amount) amountDTO {
	return amountDTO{CPUMillicores: a.CPUMillicores, MemoryBytes: a.MemoryBytes}
}

func amountPtr(a *calUseCase.Amount) *amountDTO {
	if a == nil {
		return nil
	}
	v := amount(*a)
	return &v
}

func (a *amountDTO) model() *calUseCase.Amount {
	if a == nil {
		return nil
	}
	return &calUseCase.Amount{CPUMillicores: a.CPUMillicores, MemoryBytes: a.MemoryBytes}
}

type shareDTO struct {
	AgentID   uuid.UUID `json:"AgentID"`
	AgentName string    `json:"AgentName"`
	Units     int       `json:"Units"`
}

// reservationDTO is one reservation on the admin timeline.
type reservationDTO struct {
	ID        uuid.UUID  `json:"ID"`
	Kind      string     `json:"Kind" enums:"event,test_booking"`
	EventID   *uuid.UUID `json:"EventID"`
	EventName string     `json:"EventName"`
	EventTag  string     `json:"EventTag"`
	OwnerID   *uuid.UUID `json:"OwnerID"`
	From      time.Time  `json:"From"`
	To        time.Time  `json:"To"`

	Teams          int       `json:"Teams"`
	PerTeam        amountDTO `json:"PerTeam"`
	LargestDevice  amountDTO `json:"LargestDevice"`
	BufferPercent  int       `json:"BufferPercent"`
	Dynamic        amountDTO `json:"Dynamic"`
	TailGapMinutes int       `json:"TailGapMinutes"`
	Size           amountDTO `json:"Size"`

	Placement []shareDTO `json:"Placement"`
	Unplaced  int        `json:"Unplaced"`
	// Covered: every team has an agent and no slot of the window is over capacity.
	Covered bool `json:"Covered"`
	// Used is what the event's running objects request now.
	Used   amountDTO  `json:"Used"`
	Alarms []alarmDTO `json:"Alarms"`
}

func reservation(v calUseCase.ReservationView) reservationDTO {
	out := reservationDTO{
		ID: v.ID, Kind: string(v.Kind), EventID: v.EventID, EventName: v.EventName, EventTag: v.EventTag, OwnerID: v.OwnerID,
		From: v.From, To: v.To, Teams: v.Teams, PerTeam: amount(v.PerTeam), LargestDevice: amount(v.LargestDevice), BufferPercent: v.BufferPercent,
		Dynamic: amount(v.Dynamic), TailGapMinutes: int(v.TailGap / time.Minute), Size: amount(v.Size), Placement: make([]shareDTO, 0, len(v.Placement)),
		Unplaced: v.Unplaced, Covered: v.Covered, Used: amount(v.Used), Alarms: make([]alarmDTO, 0, len(v.Alarms)),
	}
	for _, s := range v.Placement {
		out.Placement = append(out.Placement, shareDTO{AgentID: s.AgentID, AgentName: s.AgentName, Units: s.Units})
	}
	for _, a := range v.Alarms {
		out.Alarms = append(out.Alarms, alarm(a))
	}
	return out
}

type agentCapacityDTO struct {
	ID       uuid.UUID `json:"ID"`
	Name     string    `json:"Name"`
	Priority int       `json:"Priority"`
	Used     bool      `json:"Used"`
	// Why says why the agent is not used: disabled, below_requirements, no_capacity.
	Why             string    `json:"Why"`
	Connected       bool      `json:"Connected"`
	Capacity        amountDTO `json:"Capacity"`
	CPUUnlimited    bool      `json:"CPUUnlimited"`
	MemoryUnlimited bool      `json:"MemoryUnlimited"`
	DeviceMax       amountDTO `json:"DeviceMax"`
	// Nodes is the allocatable room of each lab node the agent reported (empty when it reports none).
	Nodes []amountDTO `json:"Nodes"`
}

type capacityDTO struct {
	Total               amountDTO          `json:"Total"`
	CPUUnlimited        bool               `json:"CPUUnlimited"`
	MemoryUnlimited     bool               `json:"MemoryUnlimited"`
	TestPool            amountDTO          `json:"TestPool"`
	Agents              []agentCapacityDTO `json:"Agents"`
	PerNodeRoomReported bool               `json:"PerNodeRoomReported"`
}

func capacity(v calUseCase.CapacityView) capacityDTO {
	out := capacityDTO{
		Total: amount(v.Total), CPUUnlimited: v.CPUUnlimited, MemoryUnlimited: v.MemoryUnlimited, TestPool: amount(v.TestPool),
		Agents: make([]agentCapacityDTO, 0, len(v.Agents)), PerNodeRoomReported: v.PerNodeRoomReported,
	}
	for _, a := range v.Agents {
		dto := agentCapacityDTO{
			ID: a.ID, Name: a.Name, Priority: a.Priority, Used: a.Used, Why: a.Why, Connected: a.Connected, Capacity: amount(a.Capacity),
			CPUUnlimited: a.CPUUnlimited, MemoryUnlimited: a.MemoryUnlimited, DeviceMax: amount(a.DeviceMax), Nodes: make([]amountDTO, 0, len(a.Nodes)),
		}
		for _, n := range a.Nodes {
			dto.Nodes = append(dto.Nodes, amount(n))
		}
		out.Agents = append(out.Agents, dto)
	}
	return out
}

type segmentDTO struct {
	From     time.Time `json:"From"`
	To       time.Time `json:"To"`
	Reserved amountDTO `json:"Reserved"`
}

type conflictDTO struct {
	From           time.Time   `json:"From"`
	To             time.Time   `json:"To"`
	ReservationIDs []uuid.UUID `json:"ReservationIDs"`
	PoolShort      bool        `json:"PoolShort"`
	Unplaced       int         `json:"Unplaced"`
	Short          amountDTO   `json:"Short"`
}

func conflicts(in []calUseCase.ConflictView) []conflictDTO {
	out := make([]conflictDTO, 0, len(in))
	for _, c := range in {
		out = append(out, conflictDTO{From: c.From, To: c.To, ReservationIDs: c.ReservationIDs, PoolShort: c.PoolShort, Unplaced: c.Unplaced, Short: amount(c.Short)})
	}
	return out
}

// timelineDTO is the calendar over a range of slots.
type timelineDTO struct {
	From         time.Time        `json:"From"`
	To           time.Time        `json:"To"`
	SlotMinutes  int              `json:"SlotMinutes"`
	Reservations []reservationDTO `json:"Reservations"`
	Reserved     []segmentDTO     `json:"Reserved"`
	Conflicts    []conflictDTO    `json:"Conflicts"`
	Capacity     capacityDTO      `json:"Capacity"`
	// Maintenance are the windows the cluster operators announced on the agents that are used which touch the range,
	// soonest first. In one the agent gives the platform no capacity (or Left).
	Maintenance []maintenanceDTO `json:"Maintenance"`
	// MaintenanceReported is true when every agent that is used has reported its windows; false means some agent may
	// have windows the calendar does not know.
	MaintenanceReported bool `json:"MaintenanceReported"`
}

// maintenanceDTO is a maintenance window of an agent.
type maintenanceDTO struct {
	AgentID   uuid.UUID `json:"AgentID"`
	AgentName string    `json:"AgentName"`
	Name      string    `json:"Name"`
	Reason    string    `json:"Reason"`
	From      time.Time `json:"From"`
	// To is null for a window without an end.
	To *time.Time `json:"To"`
	// Left is the capacity the window leaves; zero unless the cluster operator named some.
	Left amountDTO `json:"Left"`
}

func timeline(v calUseCase.TimelineView) timelineDTO {
	out := timelineDTO{
		From: v.From, To: v.To, SlotMinutes: v.SlotMinutes, Reservations: make([]reservationDTO, 0, len(v.Reservations)),
		Reserved: make([]segmentDTO, 0, len(v.Reserved)), Conflicts: conflicts(v.Conflicts), Capacity: capacity(v.Capacity), MaintenanceReported: v.MaintenanceReported,
		Maintenance: make([]maintenanceDTO, 0, len(v.Maintenance)),
	}
	for _, m := range v.Maintenance {
		out.Maintenance = append(out.Maintenance, maintenanceDTO{AgentID: m.AgentID, AgentName: m.AgentName, Name: m.Name, Reason: m.Reason, From: m.From, To: m.To, Left: amount(m.Left)})
	}
	for _, r := range v.Reservations {
		out.Reservations = append(out.Reservations, reservation(r))
	}
	for _, s := range v.Reserved {
		out.Reserved = append(out.Reserved, segmentDTO{From: s.From, To: s.To, Reserved: amount(s.Reserved)})
	}
	return out
}

// reservationResultDTO is the outcome of setting or changing a reservation.
type reservationResultDTO struct {
	Reservation reservationDTO `json:"Reservation"`
	Conflicts   []conflictDTO  `json:"Conflicts"`
	// Saved is false for a dry run.
	Saved bool `json:"Saved"`
}

func reservationResult(v calUseCase.ReservationResult) reservationResultDTO {
	return reservationResultDTO{Reservation: reservation(v.Reservation), Conflicts: conflicts(v.Conflicts), Saved: v.Saved}
}

// setReservationRequest is what a platform admin sets for an event. Every field is optional: the plan of the
// event gives the teams and the size per team, the settings give the buffer and the gap, the event gives the
// window.
type setReservationRequest struct {
	Teams   *int       `json:"Teams"`
	PerTeam *amountDTO `json:"PerTeam"`
	// BufferPercent overrides the default buffer (15).
	BufferPercent *int `json:"BufferPercent"`
	// Dynamic is the estimate for tasks that appear later.
	Dynamic *amountDTO `json:"Dynamic"`
	// TailGapMinutes is the gap after the event end; never shorter than the default (60), may be more.
	TailGapMinutes *int `json:"TailGapMinutes"`
	// WindowStart and WindowEnd override the window; WindowEnd is the event end (the gap is added).
	WindowStart *time.Time `json:"WindowStart"`
	WindowEnd   *time.Time `json:"WindowEnd"`
	// AllowConflicts keeps a reservation that does not fit by packing (it is then not covered).
	AllowConflicts bool `json:"AllowConflicts"`
	// DryRun only reports what would happen.
	DryRun bool `json:"DryRun"`
}

func (r setReservationRequest) model() calUseCase.EventReservationInput {
	in := calUseCase.EventReservationInput{
		Teams: r.Teams, PerTeam: r.PerTeam.model(), BufferPercent: r.BufferPercent, Dynamic: r.Dynamic.model(),
		WindowStart: r.WindowStart, WindowEnd: r.WindowEnd, AllowConflicts: r.AllowConflicts, DryRun: r.DryRun,
	}
	if r.TailGapMinutes != nil {
		d := time.Duration(*r.TailGapMinutes) * time.Minute
		in.TailGap = &d
	}
	return in
}

type replanRequest struct {
	AllowConflicts bool `json:"AllowConflicts"`
}

type alarmDTO struct {
	ID            uuid.UUID  `json:"ID"`
	Kind          string     `json:"Kind" enums:"not_placed,agent_lost,agent_shrunk,not_connected"`
	ReservationID uuid.UUID  `json:"ReservationID"`
	EventID       *uuid.UUID `json:"EventID"`
	EventName     string     `json:"EventName"`
	EventTag      string     `json:"EventTag"`
	AgentID       *uuid.UUID `json:"AgentID"`
	AgentName     string     `json:"AgentName"`
	Units         int        `json:"Units"`
	// Stage is how far the escalation went (0 first sight, 1 a day before the deploy lead, 2 two hours before, 3 at the lead).
	Stage      int        `json:"Stage"`
	Shortage   amountDTO  `json:"Shortage"`
	RaisedAt   time.Time  `json:"RaisedAt"`
	UpdatedAt  time.Time  `json:"UpdatedAt"`
	ResolvedAt *time.Time `json:"ResolvedAt"`
	AckedBy    *uuid.UUID `json:"AckedBy"`
	AckedAt    *time.Time `json:"AckedAt"`
}

func alarm(a calUseCase.AlarmView) alarmDTO {
	return alarmDTO{
		ID: a.ID, Kind: string(a.Kind), ReservationID: a.ReservationID, EventID: a.EventID, EventName: a.EventName, EventTag: a.EventTag,
		AgentID: a.AgentID, AgentName: a.AgentName, Units: a.Units, Stage: a.Stage, Shortage: amount(a.Shortage), RaisedAt: a.RaisedAt,
		UpdatedAt: a.UpdatedAt, ResolvedAt: a.ResolvedAt, AckedBy: a.AckedBy, AckedAt: a.AckedAt,
	}
}

type changeRequestDTO struct {
	ID            uuid.UUID  `json:"ID"`
	ReservationID uuid.UUID  `json:"ReservationID"`
	EventID       uuid.UUID  `json:"EventID"`
	EventName     string     `json:"EventName"`
	EventTag      string     `json:"EventTag"`
	RequestedBy   uuid.UUID  `json:"RequestedBy"`
	RequestedAt   time.Time  `json:"RequestedAt"`
	Size          *amountDTO `json:"Size"`
	Dynamic       *amountDTO `json:"Dynamic"`
	WindowStart   *time.Time `json:"WindowStart"`
	WindowEnd     *time.Time `json:"WindowEnd"`
	Reason        string     `json:"Reason"`
	Status        string     `json:"Status" enums:"pending,approved,rejected"`
	DecidedBy     *uuid.UUID `json:"DecidedBy"`
	DecidedAt     *time.Time `json:"DecidedAt"`
	DecisionNote  string     `json:"DecisionNote"`
	// Current is what the reservation holds now; the admin decides against it.
	CurrentSize amountDTO `json:"CurrentSize"`
	CurrentFrom time.Time `json:"CurrentFrom"`
	CurrentTo   time.Time `json:"CurrentTo"`
}

func changeRequest(c calUseCase.ChangeRequestView) changeRequestDTO {
	return changeRequestDTO{
		ID: c.ID, ReservationID: c.ReservationID, EventID: c.EventID, EventName: c.EventName, EventTag: c.EventTag, RequestedBy: c.RequestedBy,
		RequestedAt: c.RequestedAt, Size: amountPtr(c.Size), Dynamic: amountPtr(c.Dynamic), WindowStart: c.WindowStart, WindowEnd: c.WindowEnd,
		Reason: c.Reason, Status: string(c.Status), DecidedBy: c.DecidedBy, DecidedAt: c.DecidedAt, DecisionNote: c.DecisionNote,
		CurrentSize: amount(c.CurrentSize), CurrentFrom: c.CurrentFrom, CurrentTo: c.CurrentTo,
	}
}

type decideRequest struct {
	Approve bool   `json:"Approve"`
	Note    string `json:"Note"`
	// AllowConflicts keeps an approved change that no longer fits by packing (the reservation is then not covered).
	AllowConflicts bool `json:"AllowConflicts"`
}

type agentStatDTO struct {
	ID        uuid.UUID `json:"ID"`
	Name      string    `json:"Name"`
	Priority  int       `json:"Priority"`
	Used      bool      `json:"Used"`
	Connected bool      `json:"Connected"`
	Capacity  amountDTO `json:"Capacity"`
	Allocated amountDTO `json:"Allocated"`
	InUse     amountDTO `json:"InUse"`
	Free      amountDTO `json:"Free"`
}

type eventStatDTO struct {
	ReservationID uuid.UUID `json:"ReservationID"`
	EventID       uuid.UUID `json:"EventID"`
	EventName     string    `json:"EventName"`
	EventTag      string    `json:"EventTag"`
	From          time.Time `json:"From"`
	To            time.Time `json:"To"`
	Allocated     amountDTO `json:"Allocated"`
	InUse         amountDTO `json:"InUse"`
	Free          amountDTO `json:"Free"`
	Covered       bool      `json:"Covered"`
}

type statsDTO struct {
	At                    time.Time      `json:"At"`
	Agents                []agentStatDTO `json:"Agents"`
	Events                []eventStatDTO `json:"Events"`
	TestPool              amountDTO      `json:"TestPool"`
	TestLabsHeld          amountDTO      `json:"TestLabsHeld"`
	PendingChangeRequests int64          `json:"PendingChangeRequests"`
	OpenAlarms            int            `json:"OpenAlarms"`
}

func stats(v calUseCase.StatsView) statsDTO {
	out := statsDTO{
		At: v.At, Agents: make([]agentStatDTO, 0, len(v.Agents)), Events: make([]eventStatDTO, 0, len(v.Events)), TestPool: amount(v.TestPool),
		TestLabsHeld: amount(v.TestLabsHeld), PendingChangeRequests: v.PendingChangeRequests, OpenAlarms: v.OpenAlarms,
	}
	for _, a := range v.Agents {
		out.Agents = append(out.Agents, agentStatDTO{
			ID: a.ID, Name: a.Name, Priority: a.Priority, Used: a.Used, Connected: a.Connected, Capacity: amount(a.Capacity),
			Allocated: amount(a.Allocated), InUse: amount(a.InUse), Free: amount(a.Free),
		})
	}
	for _, e := range v.Events {
		out.Events = append(out.Events, eventStatDTO{
			ReservationID: e.ReservationID, EventID: e.EventID, EventName: e.EventName, EventTag: e.EventTag, From: e.From, To: e.To,
			Allocated: amount(e.Allocated), InUse: amount(e.InUse), Free: amount(e.Free), Covered: e.Covered,
		})
	}
	return out
}

type settingsDTO struct {
	// TestPool is the guaranteed minimum for test laboratories: always on, never reserved by events.
	TestPool  amountDTO `json:"TestPool"`
	UpdatedAt time.Time `json:"UpdatedAt"`
}

type setSettingsRequest struct {
	TestPool amountDTO `json:"TestPool"`
	// AllowConflicts keeps a pool that the reservations of the next month no longer leave room for.
	AllowConflicts bool `json:"AllowConflicts"`
}

type setSettingsResponse struct {
	Settings  settingsDTO   `json:"Settings"`
	Conflicts []conflictDTO `json:"Conflicts"`
}

// organizerChangeDTO is a change request as the organizer sees it.
type organizerChangeDTO struct {
	ID           uuid.UUID  `json:"ID"`
	RequestedAt  time.Time  `json:"RequestedAt"`
	Size         *amountDTO `json:"Size"`
	Dynamic      *amountDTO `json:"Dynamic"`
	WindowStart  *time.Time `json:"WindowStart"`
	WindowEnd    *time.Time `json:"WindowEnd"`
	Reason       string     `json:"Reason"`
	Status       string     `json:"Status" enums:"pending,approved,rejected"`
	DecidedAt    *time.Time `json:"DecidedAt"`
	DecisionNote string     `json:"DecisionNote"`
}

func organizerChange(c calUseCase.OrganizerChangeView) organizerChangeDTO {
	return organizerChangeDTO{
		ID: c.ID, RequestedAt: c.RequestedAt, Size: amountPtr(c.Size), Dynamic: amountPtr(c.Dynamic), WindowStart: c.WindowStart, WindowEnd: c.WindowEnd,
		Reason: c.Reason, Status: string(c.Status), DecidedAt: c.DecidedAt, DecisionNote: c.DecisionNote,
	}
}

// eventResourcesDTO is what an organizer sees: allocated vs used and the change requests; never an agent.
type eventResourcesDTO struct {
	// Reserved is false when the event has no reservation (nothing else is set then).
	Reserved  bool      `json:"Reserved"`
	From      time.Time `json:"From"`
	To        time.Time `json:"To"`
	Teams     int       `json:"Teams"`
	Allocated amountDTO `json:"Allocated"`
	InUse     amountDTO `json:"InUse"`
	Free      amountDTO `json:"Free"`
	// BufferPercent and Dynamic are part of Allocated: the buffer and the estimate for future dynamic tasks.
	BufferPercent int       `json:"BufferPercent"`
	Dynamic       amountDTO `json:"Dynamic"`
	// Covered: the reservation is placed in full. False means the platform admin has been alerted.
	Covered bool                 `json:"Covered"`
	Changes []organizerChangeDTO `json:"Changes"`
}

func eventResources(v calUseCase.OrganizerReservationView) eventResourcesDTO {
	out := eventResourcesDTO{
		Reserved: v.Reserved, From: v.From, To: v.To, Teams: v.Teams, Allocated: amount(v.Allocated), InUse: amount(v.InUse), Free: amount(v.Free),
		BufferPercent: v.BufferPercent, Dynamic: amount(v.Dynamic), Covered: v.Covered, Changes: make([]organizerChangeDTO, 0, len(v.Changes)),
	}
	for _, c := range v.Changes {
		out.Changes = append(out.Changes, organizerChange(c))
	}
	return out
}

// requestChangeRequest is an organizer's change request: size, window and/or the estimate for future dynamic
// tasks, with a reason.
type requestChangeRequest struct {
	Size        *amountDTO `json:"Size"`
	Dynamic     *amountDTO `json:"Dynamic"`
	WindowStart *time.Time `json:"WindowStart"`
	WindowEnd   *time.Time `json:"WindowEnd"`
	Reason      string     `json:"Reason"`
}

// testLabRoomDTO answers whether a test laboratory fits now.
type testLabRoomDTO struct {
	Available bool `json:"Available"`
	// Via says how it is admitted: booking, pool or free.
	Via string `json:"Via" enums:"booking,pool,free,"`
	// NearestFrom is the start of the nearest free window when not available (null: none within the horizon).
	NearestFrom *time.Time `json:"NearestFrom"`
}

type bookingDTO struct {
	ID   uuid.UUID `json:"ID"`
	From time.Time `json:"From"`
	To   time.Time `json:"To"`
	Size amountDTO `json:"Size"`
}

func booking(b calUseCase.BookingView) bookingDTO {
	return bookingDTO{ID: b.ID, From: b.From, To: b.To, Size: amount(b.Size)}
}

type bookRequest struct {
	// Start is the wanted start (aligned down to a 15-minute slot).
	Start time.Time `json:"Start"`
	// DurationMinutes is 15 to 480.
	DurationMinutes int `json:"DurationMinutes"`
	// Size is the laboratory's total (the exercise totals the catalog shows); LargestDevice its largest device.
	Size          amountDTO `json:"Size"`
	LargestDevice amountDTO `json:"LargestDevice"`
}
