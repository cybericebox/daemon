// Package resourceCalendarRepo persists the resource calendar: reservations (one whole-row aggregate with the
// placement as JSON), change requests, readiness alarms, the settings and the test laboratory holds.
package resourceCalendarRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

type Queries interface {
	LockResourceCalendar(context.Context) error
	CreateResourceReservation(context.Context, postgres.CreateResourceReservationParams) error
	UpdateResourceReservation(context.Context, postgres.UpdateResourceReservationParams) (int64, error)
	GetResourceReservation(context.Context, uuid.UUID) (postgres.ResourceReservation, error)
	GetEventResourceReservation(context.Context, uuid.NullUUID) (postgres.ResourceReservation, error)
	ListResourceReservationsInWindow(context.Context, postgres.ListResourceReservationsInWindowParams) ([]postgres.ResourceReservation, error)
	ListResourceReservationsEndingAfter(context.Context, time.Time) ([]postgres.ResourceReservation, error)
	ListOwnedResourceBookings(context.Context, postgres.ListOwnedResourceBookingsParams) ([]postgres.ResourceReservation, error)
	ListResourceReservationLabels(context.Context, []uuid.UUID) ([]postgres.ListResourceReservationLabelsRow, error)
	CreateResourceChangeRequest(context.Context, postgres.CreateResourceChangeRequestParams) error
	GetResourceChangeRequest(context.Context, uuid.UUID) (postgres.ResourceChangeRequest, error)
	DecideResourceChangeRequest(context.Context, postgres.DecideResourceChangeRequestParams) (int64, error)
	ListResourceChangeRequests(context.Context, postgres.ListResourceChangeRequestsParams) ([]postgres.ListResourceChangeRequestsRow, error)
	CountPendingResourceChangeRequests(context.Context) (int64, error)
	GetOpenResourceAlarm(context.Context, postgres.GetOpenResourceAlarmParams) (postgres.ResourceAlarm, error)
	CreateResourceAlarm(context.Context, postgres.CreateResourceAlarmParams) error
	UpdateResourceAlarm(context.Context, postgres.UpdateResourceAlarmParams) (int64, error)
	GetResourceAlarm(context.Context, uuid.UUID) (postgres.ResourceAlarm, error)
	ListResourceAlarms(context.Context, postgres.ListResourceAlarmsParams) ([]postgres.ListResourceAlarmsRow, error)
	ListOpenResourceAlarmsOfReservation(context.Context, uuid.UUID) ([]postgres.ResourceAlarm, error)
	GetResourceCalendarSettings(context.Context) (postgres.ResourceCalendarSetting, error)
	SetResourceCalendarSettings(context.Context, postgres.SetResourceCalendarSettingsParams) error
	CreateResourceTestLabHold(context.Context, postgres.CreateResourceTestLabHoldParams) error
	DeleteResourceTestLabHold(context.Context, uuid.UUID) error
	ListActiveResourceTestLabHolds(context.Context, time.Time) ([]postgres.ResourceTestLabHold, error)
	DeleteExpiredResourceTestLabHolds(context.Context, time.Time) (int64, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

// Lock serializes everything that decides on the calendar until the transaction ends.
func (r *Repository) Lock(ctx context.Context) error { return r.q.LockResourceCalendar(ctx) }

type shareJSON struct {
	AgentID uuid.UUID `json:"agent_id"`
	Units   int       `json:"units"`
}

func timePtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time.UTC()
	return &t
}

func tstz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func uuidPtr(v uuid.NullUUID) *uuid.UUID {
	if !v.Valid {
		return nil
	}
	id := v.UUID
	return &id
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil || *id == uuid.Nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func amountPtr(cpu, mem pgtype.Int8) *calModel.Amount {
	if !cpu.Valid || !mem.Valid {
		return nil
	}
	return &calModel.Amount{CPUMillicores: cpu.Int64, MemoryBytes: mem.Int64}
}

func int8Of(a *calModel.Amount, cpu bool) pgtype.Int8 {
	if a == nil {
		return pgtype.Int8{}
	}
	if cpu {
		return pgtype.Int8{Int64: a.CPUMillicores, Valid: true}
	}
	return pgtype.Int8{Int64: a.MemoryBytes, Valid: true}
}

// reservationFrom converts a row; a placement that cannot be read counts as none placed (never validate on read).
func reservationFrom(row postgres.ResourceReservation) calModel.Reservation {
	var shares []shareJSON
	_ = json.Unmarshal(row.Placement, &shares)
	placement := make([]calModel.Share, 0, len(shares))
	for _, s := range shares {
		placement = append(placement, calModel.Share{AgentID: s.AgentID, Units: s.Units})
	}
	return calModel.Reservation{
		ID: row.ID, Kind: calModel.Kind(row.Kind), EventID: uuidPtr(row.EventID), OwnerID: uuidPtr(row.OwnerID),
		Window: calModel.Window{Start: row.StartsAt.UTC(), End: row.EndsAt.UTC()},
		Teams:  int(row.Teams), PerTeam: calModel.Amount{CPUMillicores: row.PerTeamCpuMillicores, MemoryBytes: row.PerTeamMemoryBytes},
		LargestDevice: calModel.Amount{CPUMillicores: row.LargestDeviceCpuMillicores, MemoryBytes: row.LargestDeviceMemoryBytes},
		BufferPercent: int(row.BufferPercent), Dynamic: calModel.Amount{CPUMillicores: row.DynamicCpuMillicores, MemoryBytes: row.DynamicMemoryBytes},
		TailGap:   time.Duration(row.TailGapSeconds) * time.Second,
		Size:      calModel.Amount{CPUMillicores: row.SizeCpuMillicores, MemoryBytes: row.SizeMemoryBytes},
		Placement: placement, Unplaced: int(row.Unplaced),
		CreatedBy: row.CreatedBy.UUID, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(), CanceledAt: timePtr(row.CanceledAt),
	}
}

func placementJSON(shares []calModel.Share) []byte {
	out := make([]shareJSON, 0, len(shares))
	for _, s := range shares {
		out = append(out, shareJSON{AgentID: s.AgentID, Units: s.Units})
	}
	raw, _ := json.Marshal(out)
	return raw
}

// CreateReservation inserts a reservation.
func (r *Repository) CreateReservation(ctx context.Context, v *calModel.Reservation) error {
	return r.q.CreateResourceReservation(ctx, postgres.CreateResourceReservationParams{
		ID: v.ID, Kind: string(v.Kind), EventID: nullUUID(v.EventID), OwnerID: nullUUID(v.OwnerID),
		StartsAt: v.Window.Start, EndsAt: v.Window.End, Teams: int32(v.Teams),
		PerTeamCpuMillicores: v.PerTeam.CPUMillicores, PerTeamMemoryBytes: v.PerTeam.MemoryBytes,
		LargestDeviceCpuMillicores: v.LargestDevice.CPUMillicores, LargestDeviceMemoryBytes: v.LargestDevice.MemoryBytes,
		BufferPercent: int32(v.BufferPercent), DynamicCpuMillicores: v.Dynamic.CPUMillicores, DynamicMemoryBytes: v.Dynamic.MemoryBytes,
		TailGapSeconds: int32(v.TailGap / time.Second), SizeCpuMillicores: v.Size.CPUMillicores, SizeMemoryBytes: v.Size.MemoryBytes,
		Placement: placementJSON(v.Placement), Unplaced: int32(v.Unplaced),
		CreatedBy: nullUUID(&v.CreatedBy), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, CanceledAt: tstz(v.CanceledAt),
	})
}

// UpdateReservation writes the whole reservation; false when it is gone.
func (r *Repository) UpdateReservation(ctx context.Context, v *calModel.Reservation) (bool, error) {
	n, err := r.q.UpdateResourceReservation(ctx, postgres.UpdateResourceReservationParams{
		ID: v.ID, StartsAt: v.Window.Start, EndsAt: v.Window.End, Teams: int32(v.Teams),
		PerTeamCpuMillicores: v.PerTeam.CPUMillicores, PerTeamMemoryBytes: v.PerTeam.MemoryBytes,
		LargestDeviceCpuMillicores: v.LargestDevice.CPUMillicores, LargestDeviceMemoryBytes: v.LargestDevice.MemoryBytes,
		BufferPercent: int32(v.BufferPercent), DynamicCpuMillicores: v.Dynamic.CPUMillicores, DynamicMemoryBytes: v.Dynamic.MemoryBytes,
		TailGapSeconds: int32(v.TailGap / time.Second), SizeCpuMillicores: v.Size.CPUMillicores, SizeMemoryBytes: v.Size.MemoryBytes,
		Placement: placementJSON(v.Placement), Unplaced: int32(v.Unplaced), UpdatedAt: v.UpdatedAt, CanceledAt: tstz(v.CanceledAt),
	})
	return n > 0, err
}

// GetReservation loads one reservation.
func (r *Repository) GetReservation(ctx context.Context, id uuid.UUID) (*calModel.Reservation, error) {
	row, err := r.q.GetResourceReservation(ctx, id)
	if err != nil {
		return nil, err
	}
	v := reservationFrom(row)
	return &v, nil
}

// GetEventReservation loads the active reservation of an event.
func (r *Repository) GetEventReservation(ctx context.Context, eventID uuid.UUID) (*calModel.Reservation, error) {
	row, err := r.q.GetEventResourceReservation(ctx, nullUUID(&eventID))
	if err != nil {
		return nil, err
	}
	v := reservationFrom(row)
	return &v, nil
}

func reservationsFrom(rows []postgres.ResourceReservation) []*calModel.Reservation {
	out := make([]*calModel.Reservation, 0, len(rows))
	for _, row := range rows {
		v := reservationFrom(row)
		out = append(out, &v)
	}
	return out
}

// ListInWindow lists the active reservations that share a slot with the window.
func (r *Repository) ListInWindow(ctx context.Context, w calModel.Window) ([]*calModel.Reservation, error) {
	rows, err := r.q.ListResourceReservationsInWindow(ctx, postgres.ListResourceReservationsInWindowParams{FromAt: w.Start, ToAt: w.End})
	return reservationsFrom(rows), err
}

// ListEndingAfter lists the active reservations that have not ended by the given time.
func (r *Repository) ListEndingAfter(ctx context.Context, after time.Time) ([]*calModel.Reservation, error) {
	rows, err := r.q.ListResourceReservationsEndingAfter(ctx, after)
	return reservationsFrom(rows), err
}

// ListOwnedBookings lists the author's bookings that have not ended by the given time.
func (r *Repository) ListOwnedBookings(ctx context.Context, owner uuid.UUID, after time.Time) ([]*calModel.Reservation, error) {
	rows, err := r.q.ListOwnedResourceBookings(ctx, postgres.ListOwnedResourceBookingsParams{OwnerID: nullUUID(&owner), AfterAt: after})
	return reservationsFrom(rows), err
}

// Labels names the events of event reservations.
func (r *Repository) Labels(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]calModel.Label, error) {
	rows, err := r.q.ListResourceReservationLabels(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]calModel.Label, len(rows))
	for _, row := range rows {
		out[row.ID] = calModel.Label{EventName: row.EventName, EventTag: row.EventTag}
	}
	return out, nil
}

func changeFrom(row postgres.ResourceChangeRequest) calModel.ChangeRequest {
	return calModel.ChangeRequest{
		ID: row.ID, ReservationID: row.ReservationID, EventID: row.EventID, RequestedBy: row.RequestedBy.UUID, RequestedAt: row.RequestedAt.UTC(),
		Size: amountPtr(row.SizeCpuMillicores, row.SizeMemoryBytes), Dynamic: amountPtr(row.DynamicCpuMillicores, row.DynamicMemoryBytes),
		WindowStart: timePtr(row.WindowStart), WindowEnd: timePtr(row.WindowEnd), Reason: row.Reason, Status: changeStatus(row.Status),
		DecidedBy: uuidPtr(row.DecidedBy), DecidedAt: timePtr(row.DecidedAt), DecisionNote: row.DecisionNote,
	}
}

func changeStatus(v int16) calModel.ChangeStatus {
	switch v {
	case 1:
		return calModel.ChangeApproved
	case 2:
		return calModel.ChangeRejected
	}
	return calModel.ChangePending
}

func changeStatusCode(s calModel.ChangeStatus) int16 {
	switch s {
	case calModel.ChangeApproved:
		return 1
	case calModel.ChangeRejected:
		return 2
	}
	return 0
}

// CreateChangeRequest inserts a pending request.
func (r *Repository) CreateChangeRequest(ctx context.Context, c *calModel.ChangeRequest) error {
	return r.q.CreateResourceChangeRequest(ctx, postgres.CreateResourceChangeRequestParams{
		ID: c.ID, ReservationID: c.ReservationID, EventID: c.EventID, RequestedBy: nullUUID(&c.RequestedBy), RequestedAt: c.RequestedAt,
		SizeCpuMillicores: int8Of(c.Size, true), SizeMemoryBytes: int8Of(c.Size, false),
		DynamicCpuMillicores: int8Of(c.Dynamic, true), DynamicMemoryBytes: int8Of(c.Dynamic, false),
		WindowStart: tstz(c.WindowStart), WindowEnd: tstz(c.WindowEnd), Reason: c.Reason,
	})
}

// GetChangeRequest loads one request.
func (r *Repository) GetChangeRequest(ctx context.Context, id uuid.UUID) (*calModel.ChangeRequest, error) {
	row, err := r.q.GetResourceChangeRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	v := changeFrom(row)
	return &v, nil
}

// DecideChangeRequest writes the decision of a pending request; false when it was decided meanwhile.
func (r *Repository) DecideChangeRequest(ctx context.Context, c *calModel.ChangeRequest) (bool, error) {
	n, err := r.q.DecideResourceChangeRequest(ctx, postgres.DecideResourceChangeRequestParams{
		ID: c.ID, Status: changeStatusCode(c.Status), DecidedBy: nullUUID(c.DecidedBy), DecidedAt: tstz(c.DecidedAt), DecisionNote: c.DecisionNote,
	})
	return n > 0, err
}

// ListChangeRequests lists requests newest first; status and event narrow it.
func (r *Repository) ListChangeRequests(ctx context.Context, status *calModel.ChangeStatus, eventID *uuid.UUID) ([]calModel.NamedChangeRequest, error) {
	arg := postgres.ListResourceChangeRequestsParams{EventID: nullUUID(eventID)}
	if status != nil {
		arg.Status = pgtype.Int2{Int16: changeStatusCode(*status), Valid: true}
	}
	rows, err := r.q.ListResourceChangeRequests(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]calModel.NamedChangeRequest, 0, len(rows))
	for _, row := range rows {
		c := changeFrom(postgres.ResourceChangeRequest{
			ID: row.ID, ReservationID: row.ReservationID, EventID: row.EventID, RequestedBy: row.RequestedBy, RequestedAt: row.RequestedAt,
			SizeCpuMillicores: row.SizeCpuMillicores, SizeMemoryBytes: row.SizeMemoryBytes, DynamicCpuMillicores: row.DynamicCpuMillicores,
			DynamicMemoryBytes: row.DynamicMemoryBytes, WindowStart: row.WindowStart, WindowEnd: row.WindowEnd, Reason: row.Reason,
			Status: row.Status, DecidedBy: row.DecidedBy, DecidedAt: row.DecidedAt, DecisionNote: row.DecisionNote,
		})
		out = append(out, calModel.NamedChangeRequest{ChangeRequest: c, EventName: row.EventName, EventTag: row.EventTag})
	}
	return out, nil
}

// CountPendingChangeRequests counts the requests that wait for a decision.
func (r *Repository) CountPendingChangeRequests(ctx context.Context) (int64, error) {
	return r.q.CountPendingResourceChangeRequests(ctx)
}

func alarmFrom(row postgres.ResourceAlarm) calModel.Alarm {
	return calModel.Alarm{
		ID: row.ID, Kind: calModel.AlarmKind(row.Kind), ReservationID: row.ReservationID, EventID: uuidPtr(row.EventID), AgentID: uuidPtr(row.AgentID),
		Units: int(row.Units), Stage: int(row.Stage), Shortage: calModel.Amount{CPUMillicores: row.ShortageCpuMillicores, MemoryBytes: row.ShortageMemoryBytes},
		RaisedAt: row.RaisedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(), ResolvedAt: timePtr(row.ResolvedAt), AckedBy: uuidPtr(row.AckedBy), AckedAt: timePtr(row.AckedAt),
	}
}

// GetOpenAlarm finds the open alarm of a cause (agent nil: the cause names no agent).
func (r *Repository) GetOpenAlarm(ctx context.Context, kind calModel.AlarmKind, reservationID uuid.UUID, agentID *uuid.UUID) (*calModel.Alarm, error) {
	row, err := r.q.GetOpenResourceAlarm(ctx, postgres.GetOpenResourceAlarmParams{Kind: string(kind), ReservationID: reservationID, AgentID: nullUUID(agentID)})
	if err != nil {
		return nil, err
	}
	v := alarmFrom(row)
	return &v, nil
}

// CreateAlarm inserts a new alarm.
func (r *Repository) CreateAlarm(ctx context.Context, a *calModel.Alarm) error {
	return r.q.CreateResourceAlarm(ctx, postgres.CreateResourceAlarmParams{
		ID: a.ID, Kind: string(a.Kind), ReservationID: a.ReservationID, EventID: nullUUID(a.EventID), AgentID: nullUUID(a.AgentID),
		Units: int32(a.Units), Stage: int16(a.Stage), ShortageCpuMillicores: a.Shortage.CPUMillicores, ShortageMemoryBytes: a.Shortage.MemoryBytes,
		RaisedAt: a.RaisedAt, UpdatedAt: a.UpdatedAt,
	})
}

// UpdateAlarm writes the mutable part of an alarm (the figures, the resolution, the acknowledgment).
func (r *Repository) UpdateAlarm(ctx context.Context, a *calModel.Alarm) (bool, error) {
	n, err := r.q.UpdateResourceAlarm(ctx, postgres.UpdateResourceAlarmParams{
		ID: a.ID, Units: int32(a.Units), Stage: int16(a.Stage), ShortageCpuMillicores: a.Shortage.CPUMillicores, ShortageMemoryBytes: a.Shortage.MemoryBytes,
		UpdatedAt: a.UpdatedAt, ResolvedAt: tstz(a.ResolvedAt), AckedBy: nullUUID(a.AckedBy), AckedAt: tstz(a.AckedAt),
	})
	return n > 0, err
}

// GetAlarm loads one alarm.
func (r *Repository) GetAlarm(ctx context.Context, id uuid.UUID) (*calModel.Alarm, error) {
	row, err := r.q.GetResourceAlarm(ctx, id)
	if err != nil {
		return nil, err
	}
	v := alarmFrom(row)
	return &v, nil
}

// ListAlarms lists alarms, open ones first.
func (r *Repository) ListAlarms(ctx context.Context, onlyOpen bool, limit int32) ([]calModel.NamedAlarm, error) {
	rows, err := r.q.ListResourceAlarms(ctx, postgres.ListResourceAlarmsParams{OnlyOpen: onlyOpen, LimitVal: limit})
	if err != nil {
		return nil, err
	}
	out := make([]calModel.NamedAlarm, 0, len(rows))
	for _, row := range rows {
		a := alarmFrom(postgres.ResourceAlarm{
			ID: row.ID, Kind: row.Kind, ReservationID: row.ReservationID, EventID: row.EventID, AgentID: row.AgentID, Units: row.Units, Stage: row.Stage,
			ShortageCpuMillicores: row.ShortageCpuMillicores, ShortageMemoryBytes: row.ShortageMemoryBytes, RaisedAt: row.RaisedAt,
			UpdatedAt: row.UpdatedAt, ResolvedAt: row.ResolvedAt, AckedBy: row.AckedBy, AckedAt: row.AckedAt,
		})
		out = append(out, calModel.NamedAlarm{Alarm: a, EventName: row.EventName.String, EventTag: row.EventTag.String})
	}
	return out, nil
}

// ListOpenAlarmsOf lists the open alarms of one reservation.
func (r *Repository) ListOpenAlarmsOf(ctx context.Context, reservationID uuid.UUID) ([]*calModel.Alarm, error) {
	rows, err := r.q.ListOpenResourceAlarmsOfReservation(ctx, reservationID)
	if err != nil {
		return nil, err
	}
	out := make([]*calModel.Alarm, 0, len(rows))
	for _, row := range rows {
		a := alarmFrom(row)
		out = append(out, &a)
	}
	return out, nil
}

// Settings reads the calendar settings.
func (r *Repository) Settings(ctx context.Context) (calModel.Settings, error) {
	row, err := r.q.GetResourceCalendarSettings(ctx)
	if err != nil {
		return calModel.Settings{}, err
	}
	return calModel.Settings{TestPool: calModel.Amount{CPUMillicores: row.TestPoolCpuMillicores, MemoryBytes: row.TestPoolMemoryBytes}, UpdatedAt: row.UpdatedAt}, nil
}

// SetSettings stores the calendar settings.
func (r *Repository) SetSettings(ctx context.Context, s calModel.Settings) error {
	return r.q.SetResourceCalendarSettings(ctx, postgres.SetResourceCalendarSettingsParams{
		TestPoolCpuMillicores: s.TestPool.CPUMillicores, TestPoolMemoryBytes: s.TestPool.MemoryBytes, UpdatedAt: s.UpdatedAt,
	})
}

// SaveHold records the room a test laboratory was admitted with (again: the lease may be extended).
func (r *Repository) SaveHold(ctx context.Context, h calModel.TestLabHold) error {
	return r.q.CreateResourceTestLabHold(ctx, postgres.CreateResourceTestLabHoldParams{
		ID: h.ID, OwnerID: h.OwnerID, Via: h.Via, ReservationID: nullUUID(h.ReservationID),
		CpuMillicores: h.Size.CPUMillicores, MemoryBytes: h.Size.MemoryBytes, StartsAt: h.StartsAt, ExpiresAt: h.ExpiresAt,
	})
}

// DeleteHold releases a hold.
func (r *Repository) DeleteHold(ctx context.Context, id uuid.UUID) error {
	return r.q.DeleteResourceTestLabHold(ctx, id)
}

// ActiveHolds lists the holds whose lease has not ended.
func (r *Repository) ActiveHolds(ctx context.Context, now time.Time) ([]calModel.TestLabHold, error) {
	rows, err := r.q.ListActiveResourceTestLabHolds(ctx, now)
	if err != nil {
		return nil, err
	}
	out := make([]calModel.TestLabHold, 0, len(rows))
	for _, row := range rows {
		out = append(out, calModel.TestLabHold{
			ID: row.ID, OwnerID: row.OwnerID, Via: row.Via, ReservationID: uuidPtr(row.ReservationID),
			Size: calModel.Amount{CPUMillicores: row.CpuMillicores, MemoryBytes: row.MemoryBytes}, StartsAt: row.StartsAt.UTC(), ExpiresAt: row.ExpiresAt.UTC(),
		})
	}
	return out, nil
}

// PurgeHolds removes the holds whose lease ended.
func (r *Repository) PurgeHolds(ctx context.Context, now time.Time) (int64, error) {
	return r.q.DeleteExpiredResourceTestLabHolds(ctx, now)
}
