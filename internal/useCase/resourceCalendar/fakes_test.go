package resourceCalendarUseCase

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
)

// memStore is the calendar's data port in memory.
type memStore struct {
	mu           sync.Mutex
	reservations map[uuid.UUID]*calModel.Reservation
	changes      map[uuid.UUID]*calModel.ChangeRequest
	alarms       map[uuid.UUID]*calModel.Alarm
	holds        map[uuid.UUID]calModel.TestLabHold
	settings     calModel.Settings
	labels       map[uuid.UUID]calModel.Label
}

func newMemStore() *memStore {
	return &memStore{
		reservations: map[uuid.UUID]*calModel.Reservation{}, changes: map[uuid.UUID]*calModel.ChangeRequest{},
		alarms: map[uuid.UUID]*calModel.Alarm{}, holds: map[uuid.UUID]calModel.TestLabHold{}, labels: map[uuid.UUID]calModel.Label{},
	}
}

func copyOf(r *calModel.Reservation) *calModel.Reservation {
	c := *r
	c.Placement = append([]calModel.Share(nil), r.Placement...)
	return &c
}

func (m *memStore) Lock(context.Context) error { return nil }
func (m *memStore) CreateReservation(_ context.Context, v *calModel.Reservation) error {
	m.reservations[v.ID] = copyOf(v)
	if v.EventID != nil {
		m.labels[v.ID] = calModel.Label{EventName: "Event", EventTag: "ev"}
	}
	return nil
}
func (m *memStore) UpdateReservation(_ context.Context, v *calModel.Reservation) (bool, error) {
	if _, ok := m.reservations[v.ID]; !ok {
		return false, nil
	}
	m.reservations[v.ID] = copyOf(v)
	return true, nil
}
func (m *memStore) GetReservation(_ context.Context, id uuid.UUID) (*calModel.Reservation, error) {
	if r, ok := m.reservations[id]; ok {
		return copyOf(r), nil
	}
	return nil, pgx.ErrNoRows
}
func (m *memStore) GetEventReservation(_ context.Context, eventID uuid.UUID) (*calModel.Reservation, error) {
	for _, r := range m.reservations {
		if r.Kind == calModel.KindEvent && r.EventID != nil && *r.EventID == eventID && r.Active() {
			return copyOf(r), nil
		}
	}
	return nil, pgx.ErrNoRows
}
func (m *memStore) list(keep func(*calModel.Reservation) bool) []*calModel.Reservation {
	var out []*calModel.Reservation
	for _, r := range m.reservations {
		if r.Active() && keep(r) {
			out = append(out, copyOf(r))
		}
	}
	sortReservations(out)
	return out
}
func (m *memStore) ListInWindow(_ context.Context, w calModel.Window) ([]*calModel.Reservation, error) {
	return m.list(func(r *calModel.Reservation) bool { return r.Window.Overlaps(w) }), nil
}
func (m *memStore) ListEndingAfter(_ context.Context, after time.Time) ([]*calModel.Reservation, error) {
	return m.list(func(r *calModel.Reservation) bool { return r.Window.End.After(after) }), nil
}
func (m *memStore) ListOwnedBookings(_ context.Context, owner uuid.UUID, after time.Time) ([]*calModel.Reservation, error) {
	return m.list(func(r *calModel.Reservation) bool {
		return r.Kind == calModel.KindBooking && r.OwnerID != nil && *r.OwnerID == owner && r.Window.End.After(after)
	}), nil
}
func (m *memStore) Labels(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]calModel.Label, error) {
	out := map[uuid.UUID]calModel.Label{}
	for _, id := range ids {
		if l, ok := m.labels[id]; ok {
			out[id] = l
		}
	}
	return out, nil
}
func (m *memStore) CreateChangeRequest(_ context.Context, c *calModel.ChangeRequest) error {
	cp := *c
	m.changes[c.ID] = &cp
	return nil
}
func (m *memStore) GetChangeRequest(_ context.Context, id uuid.UUID) (*calModel.ChangeRequest, error) {
	if c, ok := m.changes[id]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, pgx.ErrNoRows
}
func (m *memStore) DecideChangeRequest(_ context.Context, c *calModel.ChangeRequest) (bool, error) {
	cur, ok := m.changes[c.ID]
	if !ok || cur.Status != calModel.ChangePending {
		return false, nil
	}
	cp := *c
	m.changes[c.ID] = &cp
	return true, nil
}
func (m *memStore) ListChangeRequests(_ context.Context, status *calModel.ChangeStatus, eventID *uuid.UUID) ([]calModel.NamedChangeRequest, error) {
	var out []calModel.NamedChangeRequest
	for _, c := range m.changes {
		if (status == nil || c.Status == *status) && (eventID == nil || c.EventID == *eventID) {
			out = append(out, calModel.NamedChangeRequest{ChangeRequest: *c, EventName: "Event", EventTag: "ev"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequestedAt.After(out[j].RequestedAt) })
	return out, nil
}
func (m *memStore) CountPendingChangeRequests(context.Context) (int64, error) {
	var n int64
	for _, c := range m.changes {
		if c.Status == calModel.ChangePending {
			n++
		}
	}
	return n, nil
}
func (m *memStore) GetOpenAlarm(_ context.Context, kind calModel.AlarmKind, reservationID uuid.UUID, agentID *uuid.UUID) (*calModel.Alarm, error) {
	for _, a := range m.alarms {
		if a.Open() && a.Kind == kind && a.ReservationID == reservationID && sameAgent(a.AgentID, agentID) {
			cp := *a
			return &cp, nil
		}
	}
	return nil, pgx.ErrNoRows
}
func (m *memStore) CreateAlarm(_ context.Context, a *calModel.Alarm) error {
	cp := *a
	m.alarms[a.ID] = &cp
	return nil
}
func (m *memStore) UpdateAlarm(_ context.Context, a *calModel.Alarm) (bool, error) {
	cp := *a
	m.alarms[a.ID] = &cp
	return true, nil
}
func (m *memStore) GetAlarm(_ context.Context, id uuid.UUID) (*calModel.Alarm, error) {
	if a, ok := m.alarms[id]; ok {
		cp := *a
		return &cp, nil
	}
	return nil, pgx.ErrNoRows
}
func (m *memStore) ListAlarms(_ context.Context, onlyOpen bool, _ int32) ([]calModel.NamedAlarm, error) {
	var out []calModel.NamedAlarm
	for _, a := range m.alarms {
		if !onlyOpen || a.Open() {
			out = append(out, calModel.NamedAlarm{Alarm: *a, EventName: "Event", EventTag: "ev"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RaisedAt.Before(out[j].RaisedAt) })
	return out, nil
}
func (m *memStore) ListOpenAlarmsOf(_ context.Context, id uuid.UUID) ([]*calModel.Alarm, error) {
	var out []*calModel.Alarm
	for _, a := range m.alarms {
		if a.Open() && a.ReservationID == id {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (m *memStore) Settings(context.Context) (calModel.Settings, error) { return m.settings, nil }
func (m *memStore) SetSettings(_ context.Context, s calModel.Settings) error {
	m.settings = s
	return nil
}
func (m *memStore) SaveHold(_ context.Context, h calModel.TestLabHold) error {
	m.holds[h.ID] = h
	return nil
}
func (m *memStore) DeleteHold(_ context.Context, id uuid.UUID) error {
	delete(m.holds, id)
	return nil
}
func (m *memStore) ActiveHolds(_ context.Context, now time.Time) ([]calModel.TestLabHold, error) {
	var out []calModel.TestLabHold
	for _, h := range m.holds {
		if h.ExpiresAt.After(now) {
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out, nil
}
func (m *memStore) PurgeHolds(_ context.Context, now time.Time) (int64, error) {
	var n int64
	for id, h := range m.holds {
		if !h.ExpiresAt.After(now) {
			delete(m.holds, id)
			n++
		}
	}
	return n, nil
}

type passTx struct{ s Store }

func (p passTx) Do(ctx context.Context, fn func(context.Context, Store) error) error {
	return fn(ctx, p.s)
}

type fakeAgents struct{ records []infraModel.AgentRecord }

func (f *fakeAgents) ListRecords(context.Context) ([]infraModel.AgentRecord, error) {
	return f.records, nil
}

type fakeEvents struct{ e eventModel.Event }

func (f fakeEvents) GetByID(_ context.Context, id uuid.UUID) (eventModel.Event, error) {
	if id != f.e.ID {
		return eventModel.Event{}, pgx.ErrNoRows
	}
	return f.e, nil
}

type fakeConfigs struct{}

func (fakeConfigs) Get(context.Context, uuid.UUID) (eventConfigModel.EventConfig, error) {
	return eventConfigModel.EventConfig{StandTiming: eventStandModel.Timing{DeployLeadMinutes: 30}}, nil
}

type fakePlanner struct{ need Need }

func (f *fakePlanner) ReservationNeed(context.Context, uuid.UUID) (Need, error) { return f.need, nil }

type fakeUsage struct{ u Usage }

func (f fakeUsage) Usage(context.Context, time.Time) (Usage, error) { return f.u, nil }

type fakeNotifier struct {
	requested []inboxUseCase.ResourceChange
	decided   []bool
	raised    []inboxUseCase.ResourceAlarm
	closed    []uuid.UUID
}

func (f *fakeNotifier) ResourceChangeRequested(_ context.Context, c inboxUseCase.ResourceChange) error {
	f.requested = append(f.requested, c)
	return nil
}
func (f *fakeNotifier) ResourceChangeDecided(_ context.Context, _ inboxUseCase.ResourceChange, approved bool, _ uuid.UUID) error {
	f.decided = append(f.decided, approved)
	return nil
}
func (f *fakeNotifier) ResourceAlarmRaised(_ context.Context, a inboxUseCase.ResourceAlarm) error {
	f.raised = append(f.raised, a)
	return nil
}
func (f *fakeNotifier) ResourceAlarmClosed(_ context.Context, id uuid.UUID, _ uuid.UUID) error {
	f.closed = append(f.closed, id)
	return nil
}
