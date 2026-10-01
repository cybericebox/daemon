// Package eventRepo is the repository for the platform Event aggregate: it
// accepts/returns whole domain entities and keeps all pgtype/sqlc mapping out
// of the business layer.
//
// Narrow queries deliberately NOT wrapped here:
//   - ListEventsCursor / CountEvents — query-side list shapes.
package eventRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	CreateEvent(ctx context.Context, arg postgres.CreateEventParams) (postgres.Event, error)
	GetEventByID(ctx context.Context, id uuid.UUID) (postgres.Event, error)
	LockEventForTeamChange(ctx context.Context, id uuid.UUID) (uuid.UUID, error)
	UpdateEvent(ctx context.Context, arg postgres.UpdateEventParams) (int64, error)
	ArchiveEvent(ctx context.Context, arg postgres.ArchiveEventParams) (int64, error)
	UpdateEventInfrastructure(ctx context.Context, arg postgres.UpdateEventInfrastructureParams) (int64, error)
	UpdateEventLifecycle(ctx context.Context, arg postgres.UpdateEventLifecycleParams) (int64, error)
	UpdateEventPublicName(ctx context.Context, arg postgres.UpdateEventPublicNameParams) (int64, error)
	UpdateEventScoringProfile(ctx context.Context, arg postgres.UpdateEventScoringProfileParams) (int64, error)
	DeleteEvent(ctx context.Context, id uuid.UUID) (int64, error)
	GetLiveEventByTag(ctx context.Context, arg postgres.GetLiveEventByTagParams) (postgres.Event, error)
	ListEventsDueForScoringPopulation(ctx context.Context, now time.Time) ([]postgres.ListEventsDueForScoringPopulationRow, error)
	InsertEventScoringPopulation(ctx context.Context, arg postgres.InsertEventScoringPopulationParams) (postgres.EventScoringPopulation, error)
	CountLiveEventsWithTag(ctx context.Context, arg postgres.CountLiveEventsWithTagParams) (int64, error)
	ListEventsCursor(ctx context.Context, arg postgres.ListEventsCursorParams) ([]postgres.Event, error)
	ListEventsPage(ctx context.Context, arg postgres.ListEventsPageParams) ([]postgres.Event, error)
	CountEventsPage(ctx context.Context, arg postgres.CountEventsPageParams) (int64, error)
	CountEvents(ctx context.Context, search string) (int64, error)
}

func (r *Repository) ListDueForScoringPopulation(ctx context.Context, now time.Time) ([]postgres.ListEventsDueForScoringPopulationRow, error) {
	return r.q.ListEventsDueForScoringPopulation(ctx, now)
}

func (r *Repository) CaptureScoringPopulation(ctx context.Context, eventID uuid.UUID, unitsCount int32, capturedAt time.Time) (postgres.EventScoringPopulation, error) {
	return r.q.InsertEventScoringPopulation(ctx, postgres.InsertEventScoringPopulationParams{EventID: eventID, UnitsCount: unitsCount, CapturedAt: capturedAt})
}

// ListParams is the keyset-page query for the events list, in domain terms.
type ListParams struct {
	Search          string
	CursorCreatedAt time.Time
	CursorID        uuid.UUID
	Limit           int32
}

type PageParams struct {
	Search  string
	Status  string
	SortBy  string
	SortDir string
	Now     time.Time
	Limit   int32
	Offset  int32
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// CountOverlappingWithTag checks half-open platform availability windows,
// optionally excluding one id (self, on update).
func (r *Repository) CountOverlappingWithTag(ctx context.Context, tag string, availableFrom, archiveAt time.Time, excludeID uuid.UUID) (int64, error) {
	return r.q.CountLiveEventsWithTag(ctx, postgres.CountLiveEventsWithTagParams{Tag: tag, AvailableFrom: availableFrom, ArchiveAt: optionalPGTime(archiveAt), ExcludeID: excludeID})
}

// ListCursor returns a keyset page of events as domain entities.
func (r *Repository) ListCursor(ctx context.Context, p ListParams) ([]eventModel.Event, error) {
	rows, err := r.q.ListEventsCursor(ctx, postgres.ListEventsCursorParams{
		Search:          p.Search,
		CursorCreatedAt: p.CursorCreatedAt,
		CursorID:        p.CursorID,
		LimitVal:        p.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]eventModel.Event, 0, len(rows))
	for _, row := range rows {
		out = append(out, ToDomain(row))
	}
	return out, nil
}

func (r *Repository) ListPage(ctx context.Context, p PageParams) ([]eventModel.Event, error) {
	rows, err := r.q.ListEventsPage(ctx, postgres.ListEventsPageParams{
		Search: p.Search, Status: p.Status, SortBy: p.SortBy, SortDir: p.SortDir,
		Now: p.Now, LimitVal: p.Limit, OffsetVal: p.Offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]eventModel.Event, 0, len(rows))
	for _, row := range rows {
		out = append(out, ToDomain(row))
	}
	return out, nil
}

func (r *Repository) CountPage(ctx context.Context, search, status string, now time.Time) (int64, error) {
	return r.q.CountEventsPage(ctx, postgres.CountEventsPageParams{Search: search, Status: status, Now: now})
}

// Count returns the number of events matching the search filter.
func (r *Repository) Count(ctx context.Context, search string) (int64, error) {
	return r.q.CountEvents(ctx, search)
}

func (r *Repository) Create(ctx context.Context, e eventModel.Event) (eventModel.Event, error) {
	row, err := r.q.CreateEvent(ctx, postgres.CreateEventParams{
		ID:            e.ID,
		Tag:           e.Tag,
		Name:          e.Name,
		InternalName:  e.InternalName,
		AvailableFrom: e.AvailableFrom,
		ArchiveAt:     optionalPGTime(e.ArchiveAt),
		CreatedAt:     e.CreatedAt,
		CreatedBy:     e.CreatedBy,
		UpdatedAt:     pgtype.Timestamptz{Time: e.UpdatedAt, Valid: true},
		UpdatedBy:     e.UpdatedBy,
		// Set here and by UpdateInfrastructure: UpdateEvent never sets the column.
		InfrastructureAllowed: e.InfrastructureAllowed,
	})
	if err != nil {
		return eventModel.Event{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (eventModel.Event, error) {
	row, err := r.q.GetEventByID(ctx, id)
	if err != nil {
		return eventModel.Event{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) LockForTeamChange(ctx context.Context, id uuid.UUID) error {
	_, err := r.q.LockEventForTeamChange(ctx, id)
	return err
}

// Update writes the mutable columns in one statement guarded by the optimistic
// lock (created_at/created_by are immutable and excluded).
func (r *Repository) Update(ctx context.Context, e eventModel.Event, expectedUpdatedAt time.Time) (int64, error) {
	return r.q.UpdateEvent(ctx, postgres.UpdateEventParams{
		ID:                e.ID,
		Tag:               e.Tag,
		InternalName:      e.InternalName,
		AvailableFrom:     e.AvailableFrom,
		ArchiveAt:         optionalPGTime(e.ArchiveAt),
		UpdatedAt:         pgtype.Timestamptz{Time: e.UpdatedAt, Valid: true},
		UpdatedBy:         e.UpdatedBy,
		ExpectedUpdatedAt: pgtype.Timestamptz{Time: expectedUpdatedAt, Valid: true},
	})
}

func (r *Repository) UpdatePublicName(ctx context.Context, e eventModel.Event, expectedUpdatedAt time.Time) (int64, error) {
	return r.q.UpdateEventPublicName(ctx, postgres.UpdateEventPublicNameParams{
		ID: e.ID, Name: e.Name,
		UpdatedAt: pgtype.Timestamptz{Time: e.UpdatedAt, Valid: true}, UpdatedBy: e.UpdatedBy,
		ExpectedUpdatedAt: pgtype.Timestamptz{Time: expectedUpdatedAt, Valid: true},
	})
}

// Archive persists the legacy archive time and the canonical withdrawn
// lifecycle as one optimistic-lock update, retaining the original start date.
func (r *Repository) Archive(ctx context.Context, e eventModel.Event, expectedUpdatedAt time.Time) (int64, error) {
	return r.q.ArchiveEvent(ctx, postgres.ArchiveEventParams{
		ID: e.ID, ArchiveAt: e.ArchiveAt,
		PublishAt: e.Lifecycle.PublishAt, StartAt: e.Lifecycle.StartAt,
		FinishAt: *e.Lifecycle.FinishAt, WithdrawAt: *e.Lifecycle.WithdrawAt,
		UpdatedAt: pgtype.Timestamptz{Time: e.UpdatedAt, Valid: true}, UpdatedBy: e.UpdatedBy,
		ExpectedUpdatedAt: pgtype.Timestamptz{Time: expectedUpdatedAt, Valid: true},
	})
}

// UpdateInfrastructure writes only the administrator's infrastructure flag.
func (r *Repository) UpdateInfrastructure(ctx context.Context, e eventModel.Event, expectedUpdatedAt time.Time) (int64, error) {
	return r.q.UpdateEventInfrastructure(ctx, postgres.UpdateEventInfrastructureParams{
		ID: e.ID, InfrastructureAllowed: e.InfrastructureAllowed,
		UpdatedAt: pgtype.Timestamptz{Time: e.UpdatedAt, Valid: true}, UpdatedBy: e.UpdatedBy,
		ExpectedUpdatedAt: pgtype.Timestamptz{Time: expectedUpdatedAt, Valid: true},
	})
}

// UpdateLifecycle writes only the canonical runtime fields. It is intentionally
// separate from Update so that the temporary legacy window cannot overwrite a
// manager's lifecycle decision.
func (r *Repository) UpdateLifecycle(ctx context.Context, e eventModel.Event, expectedUpdatedAt time.Time) (int64, error) {
	lifecycle := e.Lifecycle
	return r.q.UpdateEventLifecycle(ctx, postgres.UpdateEventLifecycleParams{
		JoinPolicy:        int16(lifecycle.JoinPolicy),
		PublishAt:         lifecycle.PublishAt,
		StartAt:           lifecycle.StartAt,
		FinishAt:          nullablePGTime(lifecycle.FinishAt),
		WithdrawAt:        nullablePGTime(lifecycle.WithdrawAt),
		ManualFinishedAt:  nullablePGTime(lifecycle.ManualFinishAt),
		UpdatedAt:         pgtype.Timestamptz{Time: e.UpdatedAt, Valid: true},
		UpdatedBy:         e.UpdatedBy,
		ID:                e.ID,
		ExpectedUpdatedAt: pgtype.Timestamptz{Time: expectedUpdatedAt, Valid: true},
	})
}

func (r *Repository) UpdateScoringProfile(ctx context.Context, e eventModel.Event, expectedUpdatedAt time.Time) (int64, error) {
	p := e.ScoringProfile
	return r.q.UpdateEventScoringProfile(ctx, postgres.UpdateEventScoringProfileParams{
		ScoringMode: int16(p.Mode), DynamicAlgorithm: dynamicAlgorithm(p.Mode), DynamicMinPoints: p.MinPoints,
		DynamicMaxPoints: p.MaxPoints, DynamicFloorAtPercent: p.FloorAtPercent,
		ForceEventScoring: e.ForceEventScoring, StaticPoints: staticPoints(e.StaticPoints),
		UpdatedAt: pgtype.Timestamptz{Time: e.UpdatedAt, Valid: true},
		UpdatedBy: e.UpdatedBy, ID: e.ID, ExpectedUpdatedAt: pgtype.Timestamptz{Time: expectedUpdatedAt, Valid: true},
	})
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) (int64, error) {
	return r.q.DeleteEvent(ctx, id)
}

// GetLiveByTag resolves the tenant event from a subdomain tag: the single
// non-archived event sharing the tag as of now.
func (r *Repository) GetLiveByTag(ctx context.Context, tag string, now time.Time) (eventModel.Event, error) {
	row, err := r.q.GetLiveEventByTag(ctx, postgres.GetLiveEventByTagParams{Tag: tag, Now: now})
	if err != nil {
		return eventModel.Event{}, err
	}
	return ToDomain(row), nil
}

// ToDomain maps a sqlc row to the domain entity. updated_at is nullable at
// the column level (pgtype.Timestamptz) but the domain field is a plain
// time.Time — every row this repository ever reads was written by Create/
// Update above, which always set it valid.
func staticPoints(value *int32) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *value, Valid: true}
}

func ToDomain(row postgres.Event) eventModel.Event {
	var static *int32
	if row.StaticPoints.Valid {
		value := row.StaticPoints.Int32
		static = &value
	}
	archiveAt := time.Time{}
	if row.ArchiveAt.Valid {
		archiveAt = row.ArchiveAt.Time
	}
	lifecycle, lifecycleErr := eventModel.NewLifecycle(
		eventModel.JoinPolicy(row.JoinPolicy), row.PublishAt,
		row.StartAt,
		nullableTime(row.FinishAt),
		nullableTime(row.WithdrawAt),
		nullableTime(row.ManualFinishedAt),
	)
	// Mocks and rows created before migration 0021 have no canonical columns.
	// Map those consistently to the compatibility scheduled lifecycle until
	// every writer has moved to the event-local management API.
	if lifecycleErr != nil {
		finishAt := archiveAt
		if finishAt.IsZero() || !finishAt.After(row.AvailableFrom) {
			finishAt = row.AvailableFrom.Add(time.Hour)
		}
		withdrawAt := finishAt.Add(time.Microsecond)
		publishAt := row.CreatedAt
		if publishAt.IsZero() || publishAt.After(row.AvailableFrom) {
			publishAt = row.AvailableFrom
		}
		lifecycle, _ = eventModel.NewLifecycle(eventModel.JoinPolicyLockedAtStart,
			publishAt, row.AvailableFrom, &finishAt, &withdrawAt, nil)
	}
	lifecycle.Configured = row.LifecycleConfigured
	return eventModel.Event{
		ID:                    row.ID,
		Tag:                   row.Tag,
		Name:                  row.Name,
		InternalName:          row.InternalName,
		Lifecycle:             lifecycle,
		ScoringProfile:        eventModel.ScoringProfile{Mode: eventModel.ScoringMode(row.ScoringMode), MinPoints: row.DynamicMinPoints, MaxPoints: row.DynamicMaxPoints, FloorAtPercent: row.DynamicFloorAtPercent},
		ForceEventScoring:     row.ForceEventScoring,
		StaticPoints:          static,
		InfrastructureAllowed: row.InfrastructureAllowed,
		AvailableFrom:         row.AvailableFrom,
		ArchiveAt:             archiveAt,
		CreatedAt:             row.CreatedAt,
		CreatedBy:             row.CreatedBy,
		UpdatedAt:             row.UpdatedAt.Time,
		UpdatedBy:             row.UpdatedBy,
	}
}

func optionalPGTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: !value.IsZero()}
}

func dynamicAlgorithm(mode eventModel.ScoringMode) int16 {
	if mode == eventModel.ScoringStatic {
		return 0
	}
	return int16(mode - 1)
}

func nullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	copy := value.Time
	return &copy
}

func nullablePGTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}
