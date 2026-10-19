package resourceCalendarUseCase

import (
	"context"
	"sort"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// The cluster reservation parts of the in-memory store.

func (m *memStore) partsMap() map[uuid.UUID]calModel.Part {
	if m.parts == nil {
		m.parts = map[uuid.UUID]calModel.Part{}
	}
	return m.parts
}

func (m *memStore) unitsMap(calendar uuid.UUID) map[string]uuid.UUID {
	if m.units == nil {
		m.units = map[uuid.UUID]map[string]uuid.UUID{}
	}
	if m.units[calendar] == nil {
		m.units[calendar] = map[string]uuid.UUID{}
	}
	return m.units[calendar]
}

func (m *memStore) SavePart(_ context.Context, p calModel.Part) error {
	parts := m.partsMap()
	if cur, ok := parts[p.ID]; ok {
		// The desired write keeps the agent's answer and the observed status.
		p.SyncedRev, p.SyncedAt, p.SyncError, p.SyncReason, p.SyncFailedSince, p.ShrinkHeld = cur.SyncedRev, cur.SyncedAt, cur.SyncError, cur.SyncReason, cur.SyncFailedSince, cur.ShrinkHeld
		p.Status = cur.Status
		p.DesiredRev = max(p.DesiredRev, cur.DesiredRev)
		p.CreatedAt = cur.CreatedAt
	}
	parts[p.ID] = p
	return nil
}

func (m *memStore) MarkSynced(_ context.Context, id uuid.UUID, a calModel.SyncAnswer) error {
	parts := m.partsMap()
	p, ok := parts[id]
	if !ok {
		return nil
	}
	p.Answer(a)
	parts[id] = p
	return nil
}

func (m *memStore) Observe(_ context.Context, id uuid.UUID, s calModel.PartStatus) error {
	parts := m.partsMap()
	if p, ok := parts[id]; ok {
		p.Status = s
		parts[id] = p
	}
	return nil
}

func (m *memStore) DeletePart(_ context.Context, id uuid.UUID) error {
	delete(m.partsMap(), id)
	return nil
}

func (m *memStore) GetPart(_ context.Context, id uuid.UUID) (calModel.Part, error) {
	if p, ok := m.partsMap()[id]; ok {
		return p, nil
	}
	return calModel.Part{}, pgx.ErrNoRows
}

func (m *memStore) partsWhere(keep func(calModel.Part) bool) []calModel.Part {
	var out []calModel.Part
	for _, p := range m.partsMap() {
		if keep(p) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	return out
}

func (m *memStore) PartsOfAgent(_ context.Context, agent uuid.UUID) ([]calModel.Part, error) {
	return m.partsWhere(func(p calModel.Part) bool { return p.AgentID == agent }), nil
}

func (m *memStore) PartsOfCalendar(_ context.Context, id uuid.UUID) ([]calModel.Part, error) {
	return m.partsWhere(func(p calModel.Part) bool {
		return p.Source == calModel.PartCalendar && p.CalendarID != nil && *p.CalendarID == id && !p.Retired()
	}), nil
}

func (m *memStore) PartsOfCalendars(_ context.Context, ids []uuid.UUID) ([]calModel.Part, error) {
	want := map[uuid.UUID]bool{}
	for _, id := range ids {
		want[id] = true
	}
	return m.partsWhere(func(p calModel.Part) bool {
		return p.Source == calModel.PartCalendar && p.CalendarID != nil && want[*p.CalendarID]
	}), nil
}

func (m *memStore) RunPart(_ context.Context, deploy uuid.UUID) (calModel.Part, error) {
	for _, p := range m.partsWhere(func(p calModel.Part) bool {
		return p.Source == calModel.PartTestRun && p.TestDeployID != nil && *p.TestDeployID == deploy && !p.Retired()
	}) {
		return p, nil
	}
	return calModel.Part{}, pgx.ErrNoRows
}

func (m *memStore) RunPartsOf(_ context.Context, owner, agent uuid.UUID) ([]calModel.Part, error) {
	return m.partsWhere(func(p calModel.Part) bool {
		return p.Source == calModel.PartTestRun && p.OwnerID != nil && *p.OwnerID == owner && p.AgentID == agent && !p.Retired()
	}), nil
}

func (m *memStore) GroupPart(_ context.Context, owner uuid.UUID) (calModel.Part, error) {
	for _, p := range m.partsWhere(func(p calModel.Part) bool {
		return p.Source == calModel.PartTestGroup && p.OwnerID != nil && *p.OwnerID == owner && !p.Retired()
	}) {
		return p, nil
	}
	return calModel.Part{}, pgx.ErrNoRows
}

func (m *memStore) agentsWhere(keep func(calModel.Part) bool) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, p := range m.partsWhere(keep) {
		if !seen[p.AgentID] {
			seen[p.AgentID] = true
			out = append(out, p.AgentID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func (m *memStore) PartAgents(context.Context) ([]uuid.UUID, error) {
	return m.agentsWhere(func(calModel.Part) bool { return true }), nil
}

func (m *memStore) UnsyncedAgents(context.Context) ([]uuid.UUID, error) {
	return m.agentsWhere(func(p calModel.Part) bool { return !p.Synced() || p.Retired() || p.ShrinkHeld }), nil
}

func (m *memStore) Units(_ context.Context, calendar uuid.UUID) (map[string]uuid.UUID, error) {
	out := map[string]uuid.UUID{}
	for k, v := range m.unitsMap(calendar) {
		out[k] = v
	}
	return out, nil
}

func (m *memStore) AssignUnit(_ context.Context, calendar uuid.UUID, unit string, agent uuid.UUID, _ time.Time) (uuid.UUID, error) {
	units := m.unitsMap(calendar)
	if a, ok := units[unit]; ok {
		return a, nil
	}
	units[unit] = agent
	return agent, nil
}

func (m *memStore) ReleaseUnit(_ context.Context, calendar uuid.UUID, unit string) error {
	delete(m.unitsMap(calendar), unit)
	return nil
}

// fakePort is an agent's reservation API in memory: every agent holds its reservations by id, and every call is
// counted.
type fakePort struct {
	held  map[uuid.UUID]map[uuid.UUID]infraModel.ClusterReservation
	calls []string
	// down makes every call of an agent fail; refuse makes create/update refuse an id with a reason.
	down   map[uuid.UUID]error
	refuse map[uuid.UUID]string
	// afterList runs once a listing is taken (another replica acting in between).
	afterList func()
}

func newFakePort() *fakePort {
	return &fakePort{held: map[uuid.UUID]map[uuid.UUID]infraModel.ClusterReservation{}, down: map[uuid.UUID]error{}, refuse: map[uuid.UUID]string{}}
}

func (f *fakePort) agent(id uuid.UUID) map[uuid.UUID]infraModel.ClusterReservation {
	if f.held[id] == nil {
		f.held[id] = map[uuid.UUID]infraModel.ClusterReservation{}
	}
	return f.held[id]
}

func (f *fakePort) ListReservations(_ context.Context, agent uuid.UUID) ([]infraModel.ClusterReservation, error) {
	if err := f.down[agent]; err != nil {
		return nil, err
	}
	var out []infraModel.ClusterReservation
	for _, r := range f.agent(agent) {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	if f.afterList != nil {
		f.afterList()
	}
	return out, nil
}

func (f *fakePort) apply(agent uuid.UUID, op string, rs []infraModel.ClusterReservation) ([]infraModel.ItemOutcome, error) {
	if err := f.down[agent]; err != nil {
		return nil, err
	}
	var out []infraModel.ItemOutcome
	for _, r := range rs {
		f.calls = append(f.calls, op+" "+r.ID.String())
		if reason := f.refuse[r.ID]; reason != "" {
			out = append(out, infraModel.ItemOutcome{ID: r.ID, Reason: reason, Err: infraModel.ReasonError(reason)})
			continue
		}
		held := f.agent(agent)
		if cur, ok := held[r.ID]; ok {
			r.Status = cur.Status
		}
		held[r.ID] = r
		out = append(out, infraModel.ItemOutcome{ID: r.ID, OK: true})
	}
	return out, nil
}

func (f *fakePort) CreateReservations(_ context.Context, agent uuid.UUID, rs []infraModel.ClusterReservation) ([]infraModel.ItemOutcome, error) {
	return f.apply(agent, "create", rs)
}

func (f *fakePort) UpdateReservations(_ context.Context, agent uuid.UUID, rs []infraModel.ClusterReservation) ([]infraModel.ItemOutcome, error) {
	return f.apply(agent, "update", rs)
}

func (f *fakePort) DeleteReservations(_ context.Context, agent uuid.UUID, ids []uuid.UUID) ([]infraModel.ItemOutcome, error) {
	if err := f.down[agent]; err != nil {
		return nil, err
	}
	var out []infraModel.ItemOutcome
	for _, id := range ids {
		f.calls = append(f.calls, "delete "+id.String())
		delete(f.agent(agent), id)
		out = append(out, infraModel.ItemOutcome{ID: id, OK: true})
	}
	return out, nil
}

// running sets what the agent reports for a reservation: groups and usage.
func (f *fakePort) running(agent, id uuid.UUID, groups int, used Amount, phase string) {
	r := f.agent(agent)[id]
	r.Status = &infraModel.ClusterReservationStatus{Phase: phase, Groups: groups, Used: used}
	f.agent(agent)[id] = r
}

// The notifier also tells about ending and reduced reservations.
func (f *fakeNotifier) ReservationEnding(_ context.Context, n ReservationNotice) error {
	f.ending = append(f.ending, n)
	return nil
}

func (f *fakeNotifier) ReservationReduced(_ context.Context, n ReservationNotice) error {
	f.reduced = append(f.reduced, n)
	return nil
}

// fakeListener records the parts it hears about.
type fakeListener struct {
	ending, ended []PartNotice
}

func (f *fakeListener) PartEnding(_ context.Context, n PartNotice) { f.ending = append(f.ending, n) }
func (f *fakeListener) PartEnded(_ context.Context, n PartNotice)  { f.ended = append(f.ended, n) }
