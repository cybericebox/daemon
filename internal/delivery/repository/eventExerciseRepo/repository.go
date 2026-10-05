// Package eventExerciseRepo maps immutable event exercise links to PostgreSQL.
package eventExerciseRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
)

type Queries interface {
	CreateEventExercise(ctx context.Context, arg postgres.CreateEventExerciseParams) (postgres.EventExercise, error)
	GetEventExerciseByID(ctx context.Context, arg postgres.GetEventExerciseByIDParams) (postgres.EventExercise, error)
	SupersedeEventExercise(ctx context.Context, arg postgres.SupersedeEventExerciseParams) (int64, error)
	ListEventExercises(ctx context.Context, eventID uuid.UUID) ([]postgres.EventExercise, error)
	UpdateEventExerciseSource(ctx context.Context, arg postgres.UpdateEventExerciseSourceParams) (postgres.EventExercise, error)
	DetachEventExercise(ctx context.Context, arg postgres.DetachEventExerciseParams) (int64, error)
	SetEventExerciseStage(ctx context.Context, arg postgres.SetEventExerciseStageParams) (postgres.EventExercise, error)
	DeleteEventExercise(ctx context.Context, arg postgres.DeleteEventExerciseParams) (int64, error)
	EventExerciseHasAttempts(ctx context.Context, eventExerciseID uuid.UUID) (bool, error)
	EventHasActiveExerciseFamily(ctx context.Context, arg postgres.EventHasActiveExerciseFamilyParams) (bool, error)
	CountEventInfrastructureExercises(ctx context.Context, eventID uuid.UUID) (int32, error)
	ListEventExerciseDetails(ctx context.Context, eventID uuid.UUID) ([]postgres.ListEventExerciseDetailsRow, error)
	ListEventCatalog(ctx context.Context, arg postgres.ListEventCatalogParams) ([]postgres.ListEventCatalogRow, error)
	ListEventCatalogTags(ctx context.Context, arg postgres.ListEventCatalogTagsParams) ([]postgres.ListEventCatalogTagsRow, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) Create(ctx context.Context, value eventExerciseModel.EventExercise) (eventExerciseModel.EventExercise, error) {
	row, err := r.q.CreateEventExercise(ctx, postgres.CreateEventExerciseParams{
		ID: value.ID, EventID: value.EventID, ExerciseID: value.ExerciseID, ExerciseVersionID: value.ExerciseVersionID,
		VariantMode: int16(value.VariantMode), FixedVariantIndex: nullableInt32(value.FixedVariantIndex), Revision: value.Revision,
		Status: int16(value.Status), ReplacesEventExerciseID: nullableUUID(value.ReplacesID), CreatedAt: value.CreatedAt, CreatedBy: value.CreatedBy,
	})
	if err != nil {
		return eventExerciseModel.EventExercise{}, err
	}
	return ToDomain(row), nil
}

// CountInfrastructure counts the active attachments whose pinned set has a lab topology.
func (r *Repository) CountInfrastructure(ctx context.Context, eventID uuid.UUID) (int, error) {
	n, err := r.q.CountEventInfrastructureExercises(ctx, eventID)
	return int(n), err
}

func (r *Repository) List(ctx context.Context, eventID uuid.UUID) ([]eventExerciseModel.EventExercise, error) {
	rows, err := r.q.ListEventExercises(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]eventExerciseModel.EventExercise, 0, len(rows))
	for _, row := range rows {
		out = append(out, ToDomain(row))
	}
	return out, nil
}

func (r *Repository) GetByID(ctx context.Context, eventID, id uuid.UUID) (eventExerciseModel.EventExercise, error) {
	row, err := r.q.GetEventExerciseByID(ctx, postgres.GetEventExerciseByIDParams{ID: id, EventID: eventID})
	if err != nil {
		return eventExerciseModel.EventExercise{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) Supersede(ctx context.Context, eventID, id uuid.UUID, now time.Time) (int64, error) {
	return r.q.SupersedeEventExercise(ctx, postgres.SupersedeEventExerciseParams{ID: id, EventID: eventID, SupersededAt: pgtype.Timestamptz{Time: now, Valid: true}})
}

func nullableInt32(value *int32) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *value, Valid: true}
}

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func ToDomain(row postgres.EventExercise) eventExerciseModel.EventExercise {
	var fixed *int32
	if row.FixedVariantIndex.Valid {
		value := row.FixedVariantIndex.Int32
		fixed = &value
	}
	var replaces *uuid.UUID
	if row.ReplacesEventExerciseID.Valid {
		value := row.ReplacesEventExerciseID.UUID
		replaces = &value
	}
	var supersededAt *time.Time
	if row.SupersededAt.Valid {
		value := row.SupersededAt.Time
		supersededAt = &value
	}
	var detachedAt *time.Time
	if row.DetachedAt.Valid {
		value := row.DetachedAt.Time
		detachedAt = &value
	}
	return eventExerciseModel.EventExercise{ID: row.ID, EventID: row.EventID, ExerciseID: row.ExerciseID, ExerciseVersionID: row.ExerciseVersionID, VariantMode: eventExerciseModel.VariantMode(row.VariantMode), FixedVariantIndex: fixed, Revision: row.Revision, Status: eventExerciseModel.Status(row.Status), ReplacesID: replaces, SupersededAt: supersededAt, DetachedAt: detachedAt, CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, StageID: stageIDFromDB(row.StageID)}
}

func stageIDFromDB(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := value.UUID
	return &id
}

// SetStage attaches the set to a stage (nil: the whole event); ErrNoRows when it is not active.
func (r *Repository) SetStage(ctx context.Context, eventID, id uuid.UUID, stageID *uuid.UUID) (eventExerciseModel.EventExercise, error) {
	row, err := r.q.SetEventExerciseStage(ctx, postgres.SetEventExerciseStageParams{ID: id, EventID: eventID, StageID: nullableUUID(stageID)})
	if err != nil {
		return eventExerciseModel.EventExercise{}, err
	}
	return ToDomain(row), nil
}

// UpdateSource switches an active attachment to another exercise version in
// place (revision + 1); ErrNoRows when it is not active.
func (r *Repository) UpdateSource(ctx context.Context, eventID, id, exerciseID, versionID uuid.UUID) (eventExerciseModel.EventExercise, error) {
	row, err := r.q.UpdateEventExerciseSource(ctx, postgres.UpdateEventExerciseSourceParams{ID: id, EventID: eventID, ExerciseID: exerciseID, ExerciseVersionID: versionID})
	if err != nil {
		return eventExerciseModel.EventExercise{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) Detach(ctx context.Context, eventID, id, by uuid.UUID, now time.Time) (int64, error) {
	return r.q.DetachEventExercise(ctx, postgres.DetachEventExerciseParams{ID: id, EventID: eventID, DetachedAt: pgtype.Timestamptz{Time: now, Valid: true}, DetachedBy: uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}})
}

func (r *Repository) Delete(ctx context.Context, eventID, id uuid.UUID) (int64, error) {
	return r.q.DeleteEventExercise(ctx, postgres.DeleteEventExerciseParams{ID: id, EventID: eventID})
}

func (r *Repository) HasAttempts(ctx context.Context, id uuid.UUID) (bool, error) {
	return r.q.EventExerciseHasAttempts(ctx, id)
}

// HasActiveFamily: an active attachment already uses the exercise, a fork of
// it or its fork source.
func (r *Repository) HasActiveFamily(ctx context.Context, eventID, exerciseID uuid.UUID) (bool, error) {
	return r.q.EventHasActiveExerciseFamily(ctx, postgres.EventHasActiveExerciseFamilyParams{EventID: eventID, ExerciseID: exerciseID})
}

// Detail is one attachment with what the manage list shows (query shape).
type Detail struct {
	EventExercise             eventExerciseModel.EventExercise
	ExerciseName              string
	Scope                     int16
	LatestVersionID           uuid.NullUUID
	VersionNumber             int32
	LatestVersionNumber       int32
	ForkedFromExerciseID      uuid.NullUUID
	ForkedFromVersionID       uuid.NullUUID
	SourceName                string
	SourceLatestVersionID     uuid.NullUUID
	SourceVersionNumber       int32
	SourceLatestVersionNumber int32
	Infrastructure            bool
	VariantCount              int32
	ChallengeCount            int32
	PublishedCount            int32
	HasAttempts               bool
}

func (r *Repository) Details(ctx context.Context, eventID uuid.UUID) ([]Detail, error) {
	rows, err := r.q.ListEventExerciseDetails(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]Detail, 0, len(rows))
	for _, row := range rows {
		link := ToDomain(postgres.EventExercise{ID: row.ID, EventID: row.EventID, ExerciseID: row.ExerciseID, ExerciseVersionID: row.ExerciseVersionID, VariantMode: row.VariantMode,
			FixedVariantIndex: row.FixedVariantIndex, Revision: row.Revision, Status: row.Status, ReplacesEventExerciseID: row.ReplacesEventExerciseID,
			SupersededAt: row.SupersededAt, DetachedAt: row.DetachedAt, CreatedAt: row.CreatedAt, StageID: row.StageID})
		out = append(out, Detail{EventExercise: link, ExerciseName: row.ExerciseName, Scope: row.Scope, LatestVersionID: row.LatestVersionID,
			VersionNumber: row.VersionNumber, LatestVersionNumber: row.LatestVersionNumber, ForkedFromExerciseID: row.ForkedFromExerciseID,
			ForkedFromVersionID: row.ForkedFromVersionID, SourceName: row.SourceName, SourceLatestVersionID: row.SourceLatestVersionID,
			SourceVersionNumber: row.SourceVersionNumber, SourceLatestVersionNumber: row.SourceLatestVersionNumber, Infrastructure: row.Infrastructure,
			VariantCount: row.VariantCount, ChallengeCount: row.ChallengeCount, PublishedCount: row.PublishedCount, HasAttempts: row.HasAttempts})
	}
	return out, nil
}

// CatalogItem is one published exercise the event may attach.
type CatalogItem struct {
	ID                 uuid.UUID
	Name               string
	Description        string
	Tags               []string
	Scope              int16
	PublishedVersionID uuid.UUID
	Infrastructure     bool
	Attached           bool
}

// Catalog lists what the event may attach; tags match any (overlap), nil or
// empty means no tag filter.
func (r *Repository) Catalog(ctx context.Context, eventID uuid.UUID, search, infrastructure string, tags []string) ([]CatalogItem, error) {
	if tags == nil {
		tags = []string{}
	}
	rows, err := r.q.ListEventCatalog(ctx, postgres.ListEventCatalogParams{EventID: eventID, Search: search, Infrastructure: infrastructure, Tags: tags})
	if err != nil {
		return nil, err
	}
	out := make([]CatalogItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, CatalogItem(row))
	}
	return out, nil
}

// TagCount is a tag and how many catalog exercises of the event carry it.
type TagCount struct {
	Tag   string
	Count int64
}

// CatalogTags ranks the tags of the exercises Catalog offers the event; an
// empty prefix returns the most used.
func (r *Repository) CatalogTags(ctx context.Context, eventID uuid.UUID, prefix string, limit int32) ([]TagCount, error) {
	rows, err := r.q.ListEventCatalogTags(ctx, postgres.ListEventCatalogTagsParams{EventID: eventID, Prefix: prefix, LimitVal: limit})
	if err != nil {
		return nil, err
	}
	out := make([]TagCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, TagCount{Tag: row.Tag, Count: row.ExerciseCount})
	}
	return out, nil
}
