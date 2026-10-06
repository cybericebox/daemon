// Package exerciseRepo is the repository for the Exercise catalog aggregates:
// it accepts and returns whole domain entities and keeps all pgtype/sqlc/JSON
// mapping out of the business layer. The use case never touches postgres.* types
// — the list/count/id-projection query shapes are wrapped here too (ListCursor /
// Count / ListVersionIDs), returning domain models and primitives; the lifecycle
// CTEs (UpsertDraft/Publish/Discard/CreateDraftFrom) return whole version rows.
package exerciseRepo

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
//
// UpsertExerciseDraft/PublishExerciseDraft/CreateDraftFromVersion return
// query-specific Row types (sqlc emits these instead of the shared
// ExerciseVersion model whenever the final SELECT targets a CTE alias rather
// than the base table directly) — their field sets are identical to
// ExerciseVersion, so the repository converts with a plain struct conversion.
type Queries interface {
	CreateExercise(ctx context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error)
	GetExerciseByID(ctx context.Context, id uuid.UUID) (postgres.Exercise, error)
	UpdateExercise(ctx context.Context, arg postgres.UpdateExerciseParams) (int64, error)
	DeleteExercise(ctx context.Context, id uuid.UUID) (int64, error)
	GetDraftVersion(ctx context.Context, exerciseID uuid.UUID) (postgres.ExerciseVersion, error)
	GetExerciseVersionByID(ctx context.Context, id uuid.UUID) (postgres.ExerciseVersion, error)
	ListExerciseVersions(ctx context.Context, exerciseID uuid.UUID) ([]postgres.ExerciseVersion, error)
	UpsertExerciseDraft(ctx context.Context, arg postgres.UpsertExerciseDraftParams) (postgres.UpsertExerciseDraftRow, error)
	PublishExerciseDraft(ctx context.Context, arg postgres.PublishExerciseDraftParams) (postgres.PublishExerciseDraftRow, error)
	DiscardExerciseDraft(ctx context.Context, exerciseID uuid.UUID) (uuid.UUID, error)
	CreateDraftFromVersion(ctx context.Context, arg postgres.CreateDraftFromVersionParams) (postgres.CreateDraftFromVersionRow, error)
	CreateExerciseCheckpoint(ctx context.Context, arg postgres.CreateExerciseCheckpointParams) (postgres.ExerciseVersion, error)
	RestoreVersionPreservingDraft(ctx context.Context, arg postgres.RestoreVersionPreservingDraftParams) (postgres.RestoreVersionPreservingDraftRow, error)
	ListExercisesCursor(ctx context.Context, arg postgres.ListExercisesCursorParams) ([]postgres.ListExercisesCursorRow, error)
	ListExercisesPage(ctx context.Context, arg postgres.ListExercisesPageParams) ([]postgres.ListExercisesPageRow, error)
	ListExerciseTags(ctx context.Context, arg postgres.ListExerciseTagsParams) ([]postgres.ListExerciseTagsRow, error)
	CountExercises(ctx context.Context, arg postgres.CountExercisesParams) (int64, error)
	CountExercisesPage(ctx context.Context, arg postgres.CountExercisesPageParams) (int64, error)
	ListExerciseVersionIDs(ctx context.Context, exerciseID uuid.UUID) ([]uuid.UUID, error)
	ListExerciseUsageEvents(ctx context.Context, arg postgres.ListExerciseUsageEventsParams) ([]postgres.ListExerciseUsageEventsRow, error)
	InsertImportedExerciseVersion(ctx context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error)
	SetImportedExercisePointers(ctx context.Context, arg postgres.SetImportedExercisePointersParams) error
	ExerciseDraftDiffersFromPublished(ctx context.Context, exerciseID uuid.UUID) (bool, error)
	ListExercisesEventAccess(ctx context.Context, exerciseIds []uuid.UUID) ([]postgres.ListExercisesEventAccessRow, error)
	DeleteExerciseEventAccess(ctx context.Context, exerciseID uuid.UUID) error
	InsertExerciseEventAccess(ctx context.Context, arg postgres.InsertExerciseEventAccessParams) error
	IsExerciseReadableBy(ctx context.Context, arg postgres.IsExerciseReadableByParams) (bool, error)
	IsExerciseAvailableToEvent(ctx context.Context, arg postgres.IsExerciseAvailableToEventParams) (bool, error)
	GetExerciseInfrastructure(ctx context.Context, exerciseID uuid.UUID) (bool, error)
	GetEventInfrastructureAllowed(ctx context.Context, id uuid.UUID) (bool, error)
	FindEventFork(ctx context.Context, arg postgres.FindEventForkParams) (postgres.Exercise, error)
	ListExerciseCardExtras(ctx context.Context, ids []uuid.UUID) ([]postgres.ListExerciseCardExtrasRow, error)
	CreateExerciseResourceElevation(ctx context.Context, arg postgres.CreateExerciseResourceElevationParams) error
	GetExerciseResourceElevation(ctx context.Context, id uuid.UUID) (postgres.ExerciseResourceElevation, error)
	DecideExerciseResourceElevation(ctx context.Context, arg postgres.DecideExerciseResourceElevationParams) (int64, error)
	ListExerciseResourceElevations(ctx context.Context, arg postgres.ListExerciseResourceElevationsParams) ([]postgres.ListExerciseResourceElevationsRow, error)
	GetLatestExerciseResourceElevation(ctx context.Context, exerciseID uuid.UUID) (postgres.ExerciseResourceElevation, error)
	ListApprovedExerciseResourceElevations(ctx context.Context, exerciseIds []uuid.UUID) ([]postgres.ListApprovedExerciseResourceElevationsRow, error)
	ListPublishedVariantDevices(ctx context.Context, ids []uuid.UUID) ([]postgres.ListPublishedVariantDevicesRow, error)
	ListVersionVariantDevices(ctx context.Context, ids []uuid.UUID) ([]postgres.ListVersionVariantDevicesRow, error)
	ListFileExerciseIDs(ctx context.Context, arg postgres.ListFileExerciseIDsParams) ([]uuid.UUID, error)
	ListFileOwners(ctx context.Context, ids []uuid.UUID) ([]postgres.ListFileOwnersRow, error)
	ListUserNames(ctx context.Context, ids []uuid.UUID) ([]postgres.ListUserNamesRow, error)
	ListUserEventMemberships(ctx context.Context, userID uuid.UUID) ([]postgres.ListUserEventMembershipsRow, error)
	ListManagedExerciseAttachments(ctx context.Context, arg postgres.ListManagedExerciseAttachmentsParams) ([]postgres.ListManagedExerciseAttachmentsRow, error)
	ArchiveEventExercises(ctx context.Context, arg postgres.ArchiveEventExercisesParams) error
	CreateExerciseProposal(ctx context.Context, arg postgres.CreateExerciseProposalParams) (postgres.ExerciseProposal, error)
	GetExerciseProposal(ctx context.Context, id uuid.UUID) (postgres.ExerciseProposal, error)
	DecideExerciseProposal(ctx context.Context, arg postgres.DecideExerciseProposalParams) (int64, error)
	ListExerciseProposals(ctx context.Context, status pgtype.Int2) ([]postgres.ListExerciseProposalsRow, error)
}

// ListParams is the keyset-page query for the catalog list, in domain terms
// (the use case never builds the sqlc params).
type ListParams struct {
	Search          string
	Tags            []string
	Archived        string // "only" or "exclude"
	Visibility      Visibility
	CursorCreatedAt time.Time
	CursorID        uuid.UUID
	Limit           int32
}

// Visibility narrows catalog lists: Scope ” | catalog | event; EventIDs
// keeps exercises relevant to ANY of the events (owned by one, or available
// to one); Infrastructure ” | yes | no; ViewerID set = a non-admin who sees
// only what they may read.
type Visibility struct {
	Scope          string
	EventIDs       []uuid.UUID
	Infrastructure string
	ViewerID       uuid.NullUUID
}

// Listed is one catalog list row: the entity plus its derived catalog status
// (query-side shape).
type Listed struct {
	exerciseModel.Exercise
	Status exerciseModel.CatalogStatus
}

type PageParams struct {
	Search     string
	Tags       []string
	Status     string // ” or a CatalogStatus
	Archived   string // "only" or "exclude"
	Visibility Visibility
	SortBy     string
	SortDir    string
	Limit      int32
	Offset     int32
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// ListCursor returns a keyset page of catalog exercises as domain entities.
func (r *Repository) ListCursor(ctx context.Context, p ListParams) ([]Listed, error) {
	rows, err := r.q.ListExercisesCursor(ctx, postgres.ListExercisesCursorParams{
		Search:          p.Search,
		Tags:            p.Tags,
		Archived:        p.Archived,
		Scope:           p.Visibility.Scope,
		EventIds:        p.Visibility.EventIDs,
		Infrastructure:  p.Visibility.Infrastructure,
		ViewerID:        p.Visibility.ViewerID,
		CursorCreatedAt: p.CursorCreatedAt,
		CursorID:        p.CursorID,
		LimitVal:        p.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Listed, 0, len(rows))
	for _, row := range rows {
		out = append(out, Listed{Exercise: ToDomain(row.Exercise), Status: exerciseModel.CatalogStatus(row.Status)})
	}
	return out, nil
}

func (r *Repository) ListPage(ctx context.Context, p PageParams) ([]Listed, error) {
	rows, err := r.q.ListExercisesPage(ctx, postgres.ListExercisesPageParams{
		Search: p.Search, Tags: p.Tags, Status: p.Status, Archived: p.Archived, SortBy: p.SortBy,
		SortDir: p.SortDir, LimitVal: p.Limit, OffsetVal: p.Offset,
		Scope: p.Visibility.Scope, EventIds: p.Visibility.EventIDs, Infrastructure: p.Visibility.Infrastructure, ViewerID: p.Visibility.ViewerID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Listed, 0, len(rows))
	for _, row := range rows {
		out = append(out, Listed{Exercise: ToDomain(row.Exercise), Status: exerciseModel.CatalogStatus(row.Status)})
	}
	return out, nil
}

func (r *Repository) CountPage(ctx context.Context, search string, tags []string, status, archived string, v Visibility) (int64, error) {
	return r.q.CountExercisesPage(ctx, postgres.CountExercisesPageParams{Search: search, Tags: tags, Status: status, Archived: archived,
		Scope: v.Scope, EventIds: v.EventIDs, Infrastructure: v.Infrastructure, ViewerID: v.ViewerID})
}

type TagCount struct {
	Tag   string
	Count int64
}

// ListTags ranks tags by use; an empty prefix returns the most used. A valid
// viewerID counts only exercises that viewer may read.
func (r *Repository) ListTags(ctx context.Context, prefix string, viewerID uuid.NullUUID, limit int32) ([]TagCount, error) {
	rows, err := r.q.ListExerciseTags(ctx, postgres.ListExerciseTagsParams{Prefix: prefix, ViewerID: viewerID, LimitVal: limit})
	if err != nil {
		return nil, err
	}
	items := make([]TagCount, 0, len(rows))
	for _, row := range rows {
		items = append(items, TagCount{Tag: row.Tag, Count: row.ExerciseCount})
	}
	return items, nil
}

// Count returns the number of catalog exercises matching the search/tags/archived filter.
func (r *Repository) Count(ctx context.Context, search string, tags []string, archived string, v Visibility) (int64, error) {
	return r.q.CountExercises(ctx, postgres.CountExercisesParams{Search: search, Tags: tags, Archived: archived,
		Scope: v.Scope, EventIds: v.EventIDs, Infrastructure: v.Infrastructure, ViewerID: v.ViewerID})
}

// ListVersionIDs returns the ids of an exercise's versions (for media GC).
func (r *Repository) ListVersionIDs(ctx context.Context, exerciseID uuid.UUID) ([]uuid.UUID, error) {
	return r.q.ListExerciseVersionIDs(ctx, exerciseID)
}

// UsageEvent is one event referencing the exercise (query-side shape).
type UsageEvent struct {
	ID       uuid.UUID
	Name     string
	Archived bool
}

// ListUsage returns the events referencing any version of the exercise;
// archive state is evaluated at now.
func (r *Repository) ListUsage(ctx context.Context, exerciseID uuid.UUID, now time.Time) ([]UsageEvent, error) {
	rows, err := r.q.ListExerciseUsageEvents(ctx, postgres.ListExerciseUsageEventsParams{ExerciseID: exerciseID, Now: now})
	if err != nil {
		return nil, err
	}
	out := make([]UsageEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, UsageEvent{ID: row.ID, Name: row.Name, Archived: row.Archived})
	}
	return out, nil
}

func (r *Repository) Create(ctx context.Context, e exerciseModel.Exercise) (exerciseModel.Exercise, error) {
	row, err := r.q.CreateExercise(ctx, postgres.CreateExerciseParams{
		ID:          e.ID,
		Name:        e.Name,
		Description: e.Description,
		Tags:        e.Tags,
		CreatedAt:   e.CreatedAt,
		CreatedBy:   e.CreatedBy,
		UpdatedAt:   e.UpdatedAt,
		UpdatedBy:   e.UpdatedBy,

		Scope:                int16(e.Scope),
		OwnerEventID:         e.OwnerEventID,
		AccessLevel:          int16(e.AccessLevel),
		OriginEventID:        e.OriginEventID,
		ForkedFromExerciseID: e.ForkedFromExerciseID,
		ForkedFromVersionID:  e.ForkedFromVersionID,
	})
	if err != nil {
		return exerciseModel.Exercise{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (exerciseModel.Exercise, error) {
	row, err := r.q.GetExerciseByID(ctx, id)
	if err != nil {
		return exerciseModel.Exercise{}, err
	}
	return ToDomain(row), nil
}

// Update writes the identity columns (archive state included) in one
// statement guarded by the optimistic lock (see users). Version pointers are
// excluded — they belong to the lifecycle CTEs.
func (r *Repository) Update(ctx context.Context, e exerciseModel.Exercise, expectedUpdatedAt time.Time) (int64, error) {
	return r.q.UpdateExercise(ctx, postgres.UpdateExerciseParams{
		ID:                e.ID,
		Name:              e.Name,
		Description:       e.Description,
		Tags:              e.Tags,
		ArchivedAt:        timestamptzFromPtr(e.ArchivedAt),
		AccessLevel:       int16(e.AccessLevel),
		UpdatedAt:         e.UpdatedAt,
		UpdatedBy:         e.UpdatedBy,
		ExpectedUpdatedAt: expectedUpdatedAt,
	})
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) (int64, error) {
	return r.q.DeleteExercise(ctx, id)
}

func (r *Repository) GetDraft(ctx context.Context, exerciseID uuid.UUID) (exerciseModel.ExerciseVersion, error) {
	row, err := r.q.GetDraftVersion(ctx, exerciseID)
	if err != nil {
		return exerciseModel.ExerciseVersion{}, err
	}
	return VersionToDomain(row)
}

func (r *Repository) GetVersion(ctx context.Context, id uuid.UUID) (exerciseModel.ExerciseVersion, error) {
	row, err := r.q.GetExerciseVersionByID(ctx, id)
	if err != nil {
		return exerciseModel.ExerciseVersion{}, err
	}
	return VersionToDomain(row)
}

func (r *Repository) ListVersions(ctx context.Context, exerciseID uuid.UUID) ([]exerciseModel.ExerciseVersion, error) {
	rows, err := r.q.ListExerciseVersions(ctx, exerciseID)
	if err != nil {
		return nil, err
	}
	out := make([]exerciseModel.ExerciseVersion, 0, len(rows))
	for _, row := range rows {
		v, mErr := VersionToDomain(row)
		if mErr != nil {
			return nil, mErr
		}
		out = append(out, v)
	}
	return out, nil
}

func (r *Repository) InsertImportedVersion(ctx context.Context, v exerciseModel.ExerciseVersion) (exerciseModel.ExerciseVersion, error) {
	variants, err := marshalVariants(v.Variants)
	if err != nil {
		return exerciseModel.ExerciseVersion{}, err
	}
	row, err := r.q.InsertImportedExerciseVersion(ctx, postgres.InsertImportedExerciseVersionParams{ID: v.ID, ExerciseID: v.ExerciseID, Status: string(v.Status), AdminNote: v.AdminNote, Label: v.Label, Variants: variants, CreatedAt: v.CreatedAt, CreatedBy: v.CreatedBy, PublishedAt: timestamptzFromPtr(v.PublishedAt)})
	if err != nil {
		return exerciseModel.ExerciseVersion{}, err
	}
	return VersionToDomain(row)
}
func (r *Repository) SetImportedPointers(ctx context.Context, id uuid.UUID, draft, published uuid.NullUUID) error {
	return r.q.SetImportedExercisePointers(ctx, postgres.SetImportedExercisePointersParams{ID: id, DraftVersionID: draft, PublishedVersionID: published})
}

// DraftDiffersFromPublished compares the draft and published content
// (variants + admin note) in SQL; ErrNoRows when either pointer is NULL.
func (r *Repository) DraftDiffersFromPublished(ctx context.Context, exerciseID uuid.UUID) (bool, error) {
	return r.q.ExerciseDraftDiffersFromPublished(ctx, exerciseID)
}

func (r *Repository) UpsertDraft(ctx context.Context, exerciseID, newID uuid.UUID, v exerciseModel.ExerciseVersion, now time.Time, by uuid.NullUUID) (exerciseModel.ExerciseVersion, error) {
	blob, err := marshalVariants(v.Variants)
	if err != nil {
		return exerciseModel.ExerciseVersion{}, err
	}
	row, err := r.q.UpsertExerciseDraft(ctx, postgres.UpsertExerciseDraftParams{
		ExerciseID: exerciseID,
		NewID:      newID,
		AdminNote:  v.AdminNote,
		Variants:   blob,
		CreatedAt:  now,
		CreatedBy:  by,
	})
	if err != nil {
		return exerciseModel.ExerciseVersion{}, err
	}
	return VersionToDomain(postgres.ExerciseVersion(row))
}

func (r *Repository) Publish(ctx context.Context, exerciseID uuid.UUID, publishedAt time.Time) (exerciseModel.ExerciseVersion, error) {
	row, err := r.q.PublishExerciseDraft(ctx, postgres.PublishExerciseDraftParams{
		ExerciseID:  exerciseID,
		PublishedAt: timestamptzFromTime(publishedAt),
	})
	if err != nil {
		return exerciseModel.ExerciseVersion{}, err
	}
	return VersionToDomain(postgres.ExerciseVersion(row))
}

func (r *Repository) Discard(ctx context.Context, exerciseID uuid.UUID) (uuid.UUID, error) {
	return r.q.DiscardExerciseDraft(ctx, exerciseID)
}

func (r *Repository) CreateDraftFrom(ctx context.Context, exerciseID, sourceID, newID uuid.UUID, now time.Time, by uuid.NullUUID) (exerciseModel.ExerciseVersion, error) {
	row, err := r.q.CreateDraftFromVersion(ctx, postgres.CreateDraftFromVersionParams{
		ExerciseID: exerciseID,
		SourceID:   sourceID,
		NewID:      newID,
		CreatedAt:  now,
		CreatedBy:  by,
	})
	if err != nil {
		return exerciseModel.ExerciseVersion{}, err
	}
	return VersionToDomain(postgres.ExerciseVersion(row))
}

func (r *Repository) CreateCheckpoint(ctx context.Context, exerciseID, newID uuid.UUID, label string, now time.Time, by uuid.NullUUID) (exerciseModel.ExerciseVersion, error) {
	row, err := r.q.CreateExerciseCheckpoint(ctx, postgres.CreateExerciseCheckpointParams{
		ExerciseID: exerciseID, NewID: newID, Label: label, CreatedAt: now, CreatedBy: by,
	})
	if err != nil {
		return exerciseModel.ExerciseVersion{}, err
	}
	return VersionToDomain(row)
}

func (r *Repository) RestoreVersionPreservingDraft(ctx context.Context, exerciseID, sourceID, checkpointID uuid.UUID, now time.Time, by uuid.NullUUID) (exerciseModel.ExerciseVersion, error) {
	row, err := r.q.RestoreVersionPreservingDraft(ctx, postgres.RestoreVersionPreservingDraftParams{
		ExerciseID: exerciseID, SourceID: sourceID, CheckpointID: checkpointID, CreatedAt: now, CreatedBy: by,
	})
	if err != nil {
		return exerciseModel.ExerciseVersion{}, err
	}
	return VersionToDomain(postgres.ExerciseVersion(row))
}

// ToDomain maps a sqlc exercises row to the domain entity.
func ToDomain(row postgres.Exercise) exerciseModel.Exercise {
	return exerciseModel.Exercise{
		ID:                 row.ID,
		Name:               row.Name,
		Description:        row.Description,
		Tags:               row.Tags,
		DraftVersionID:     row.DraftVersionID,
		PublishedVersionID: row.PublishedVersionID,
		ArchivedAt:         timePtrFromTimestamptz(row.ArchivedAt),
		CreatedAt:          row.CreatedAt,
		CreatedBy:          row.CreatedBy,
		UpdatedAt:          row.UpdatedAt,
		UpdatedBy:          row.UpdatedBy,

		Scope:                exerciseModel.Scope(row.Scope),
		OwnerEventID:         row.OwnerEventID,
		AccessLevel:          exerciseModel.AccessLevel(row.AccessLevel),
		OriginEventID:        row.OriginEventID,
		ForkedFromExerciseID: row.ForkedFromExerciseID,
		ForkedFromVersionID:  row.ForkedFromVersionID,
	}
}

// VersionToDomain maps a version row; the JSONB blob is decoded here and only
// here. Reads forgive nothing structurally (json must parse) but validate no
// domain rules — historical rows load as-is.
func VersionToDomain(row postgres.ExerciseVersion) (exerciseModel.ExerciseVersion, error) {
	variants, err := unmarshalVariants(row.Variants)
	if err != nil {
		return exerciseModel.ExerciseVersion{}, err
	}
	var publishedAt *time.Time
	if row.PublishedAt.Valid {
		t := row.PublishedAt.Time
		publishedAt = &t
	}
	return exerciseModel.ExerciseVersion{
		ID:          row.ID,
		ExerciseID:  row.ExerciseID,
		Status:      exerciseModel.VersionStatus(row.Status),
		AdminNote:   row.AdminNote,
		Label:       row.Label,
		Variants:    variants,
		CreatedAt:   row.CreatedAt,
		CreatedBy:   row.CreatedBy,
		PublishedAt: publishedAt,
	}, nil
}

func marshalVariants(vs []exerciseModel.Variant) ([]byte, error) {
	if vs == nil {
		vs = []exerciseModel.Variant{}
	}
	b, err := json.Marshal(vs)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to marshal variants").Err()
	}
	return b, nil
}

func unmarshalVariants(b []byte) ([]exerciseModel.Variant, error) {
	var vs []exerciseModel.Variant
	if len(b) == 0 {
		return []exerciseModel.Variant{}, nil
	}
	if err := json.Unmarshal(b, &vs); err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to unmarshal variants").Err()
	}
	return vs, nil
}

func timestamptzFromTime(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func timestamptzFromPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return timestamptzFromTime(*t)
}

func timePtrFromTimestamptz(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// ── W4: access, visibility, proposals (query-side shapes) ──

// EventRef names an event (query-side shape).
type EventRef struct {
	ID   uuid.UUID
	Name string
}

// AccessEventsOf lists the selected events of a page of exercises in one
// query, keyed by exercise id (sorted by event name).
func (r *Repository) AccessEventsOf(ctx context.Context, exerciseIDs []uuid.UUID) (map[uuid.UUID][]EventRef, error) {
	out := make(map[uuid.UUID][]EventRef, len(exerciseIDs))
	if len(exerciseIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.ListExercisesEventAccess(ctx, exerciseIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ExerciseID] = append(out[row.ExerciseID], EventRef{ID: row.EventID, Name: row.EventName})
	}
	return out, nil
}

// ReplaceAccessEvents replaces the selected-events list (set operation; the
// caller runs it with the identity write in one unit of work).
func (r *Repository) ReplaceAccessEvents(ctx context.Context, exerciseID uuid.UUID, eventIDs []uuid.UUID) error {
	if err := r.q.DeleteExerciseEventAccess(ctx, exerciseID); err != nil {
		return err
	}
	if len(eventIDs) == 0 {
		return nil
	}
	return r.q.InsertExerciseEventAccess(ctx, postgres.InsertExerciseEventAccessParams{ExerciseID: exerciseID, EventIds: eventIDs})
}

func (r *Repository) ReadableBy(ctx context.Context, exerciseID, viewerID uuid.UUID) (bool, error) {
	return r.q.IsExerciseReadableBy(ctx, postgres.IsExerciseReadableByParams{ExerciseID: exerciseID, ViewerID: viewerID})
}

func (r *Repository) AvailableToEvent(ctx context.Context, exerciseID, eventID uuid.UUID) (bool, error) {
	return r.q.IsExerciseAvailableToEvent(ctx, postgres.IsExerciseAvailableToEventParams{ExerciseID: exerciseID, EventID: eventID})
}

func (r *Repository) HasInfrastructure(ctx context.Context, exerciseID uuid.UUID) (bool, error) {
	return r.q.GetExerciseInfrastructure(ctx, exerciseID)
}

// PublishedVariants reads, for each exercise that has a published version, its variants reduced to what the
// resource totals need (ids and the container devices' size fields); exercises without one are absent.
func (r *Repository) PublishedVariants(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]exerciseModel.Variant, error) {
	out := make(map[uuid.UUID][]exerciseModel.Variant, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.q.ListPublishedVariantDevices(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var variants []exerciseModel.Variant
		if err = json.Unmarshal(row.Variants, &variants); err != nil {
			return nil, err
		}
		out[row.ExerciseID] = variants
	}
	return out, nil
}

// VersionVariants reads the variants of specific versions reduced the same way (an event pins versions);
// the result is keyed by version id.
func (r *Repository) VersionVariants(ctx context.Context, versionIDs []uuid.UUID) (map[uuid.UUID][]exerciseModel.Variant, error) {
	out := make(map[uuid.UUID][]exerciseModel.Variant, len(versionIDs))
	if len(versionIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.ListVersionVariantDevices(ctx, versionIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var variants []exerciseModel.Variant
		if err = json.Unmarshal(row.Variants, &variants); err != nil {
			return nil, err
		}
		out[row.VersionID] = variants
	}
	return out, nil
}

// CardExtras decorates a page of exercises (owner event name, fork source
// name, infrastructure, pending proposal) in one query.
type CardExtras struct {
	OwnerEventName    string
	ForkedFromName    string
	Infrastructure    bool
	PendingProposalID uuid.NullUUID
}

func (r *Repository) CardExtras(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]CardExtras, error) {
	out := make(map[uuid.UUID]CardExtras, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.q.ListExerciseCardExtras(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = CardExtras{OwnerEventName: row.OwnerEventName, ForkedFromName: row.ForkedFromName, Infrastructure: row.Infrastructure, PendingProposalID: row.PendingProposalID}
	}
	return out, nil
}

// FileExerciseIDs lists the exercises whose versions reference a file.
func (r *Repository) FileExerciseIDs(ctx context.Context, fileID uuid.UUID, refType string) ([]uuid.UUID, error) {
	return r.q.ListFileExerciseIDs(ctx, postgres.ListFileExerciseIDsParams{FileID: fileID, RefType: refType})
}

// FileOwners returns who uploaded each of the files; a file that does not exist is absent from the map, and
// one with no recorded uploader maps to the zero value.
func (r *Repository) FileOwners(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]uuid.NullUUID, error) {
	rows, err := r.q.ListFileOwners(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]uuid.NullUUID, len(rows))
	for _, row := range rows {
		out[row.ID] = row.CreatedBy
	}
	return out, nil
}

// UserNames maps each user id to "First Last" (never the email); a user that does not exist, or has no name,
// is absent from the map.
func (r *Repository) UserNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := r.q.ListUserNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, row := range rows {
		if name := strings.TrimSpace(row.FirstName + " " + row.LastName); name != "" {
			out[row.ID] = name
		}
	}
	return out, nil
}

// Membership is one event the user is a member of (role: 0 owner, 1 manager,
// 2 viewer).
type Membership struct {
	EventID               uuid.UUID
	Role                  int16
	Name                  string
	Tag                   string
	InfrastructureAllowed bool
}

func (r *Repository) Memberships(ctx context.Context, userID uuid.UUID) ([]Membership, error) {
	rows, err := r.q.ListUserEventMemberships(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Membership, 0, len(rows))
	for _, row := range rows {
		out = append(out, Membership{EventID: row.EventID, Role: row.Role, Name: row.Name, Tag: row.Tag, InfrastructureAllowed: row.InfrastructureAllowed})
	}
	return out, nil
}

// Attachment is one active attachment of an exercise in an event the user belongs to.
type Attachment struct {
	VersionID         uuid.UUID
	Fixed             bool
	FixedVariantIndex int32
	Role              int16
}

// Attachments lists the active attachments of the exercise in the user's events.
func (r *Repository) Attachments(ctx context.Context, exerciseID, userID uuid.UUID) ([]Attachment, error) {
	rows, err := r.q.ListManagedExerciseAttachments(ctx, postgres.ListManagedExerciseAttachmentsParams{ExerciseID: exerciseID, UserID: userID})
	if err != nil {
		return nil, err
	}
	out := make([]Attachment, 0, len(rows))
	for _, row := range rows {
		out = append(out, Attachment{VersionID: row.ExerciseVersionID, Fixed: row.VariantMode == 1, FixedVariantIndex: row.FixedVariantIndex.Int32, Role: row.Role})
	}
	return out, nil
}

// ArchiveOwnedBy archives every exercise an event owns (event deletion).
func (r *Repository) ArchiveOwnedBy(ctx context.Context, eventID uuid.UUID, now time.Time) error {
	return r.q.ArchiveEventExercises(ctx, postgres.ArchiveEventExercisesParams{Now: now, EventID: uuid.NullUUID{UUID: eventID, Valid: true}})
}

// Proposal is one request to add an event exercise to the catalog.
type Proposal struct {
	ID                uuid.UUID
	ExerciseID        uuid.UUID
	EventID           uuid.NullUUID
	Status            int16 // 0 pending, 1 approved, 2 rejected
	Note              string
	ProposedBy        uuid.NullUUID
	ProposedAt        time.Time
	DecidedBy         uuid.NullUUID
	DecidedAt         *time.Time
	DecisionNote      string
	CatalogExerciseID uuid.NullUUID
	ExerciseName      string
	EventName         string
	ProposedByName    string
}

func proposalFromRow(row postgres.ExerciseProposal) Proposal {
	return Proposal{ID: row.ID, ExerciseID: row.ExerciseID, EventID: row.EventID, Status: row.Status, Note: row.Note,
		ProposedBy: row.ProposedBy, ProposedAt: row.ProposedAt, DecidedBy: row.DecidedBy, DecidedAt: timePtrFromTimestamptz(row.DecidedAt),
		DecisionNote: row.DecisionNote, CatalogExerciseID: row.CatalogExerciseID}
}

func (r *Repository) CreateProposal(ctx context.Context, p Proposal) (Proposal, error) {
	row, err := r.q.CreateExerciseProposal(ctx, postgres.CreateExerciseProposalParams{ID: p.ID, ExerciseID: p.ExerciseID, EventID: p.EventID, Note: p.Note, ProposedBy: p.ProposedBy, ProposedAt: p.ProposedAt})
	if err != nil {
		return Proposal{}, err
	}
	return proposalFromRow(row), nil
}

func (r *Repository) GetProposal(ctx context.Context, id uuid.UUID) (Proposal, error) {
	row, err := r.q.GetExerciseProposal(ctx, id)
	if err != nil {
		return Proposal{}, err
	}
	return proposalFromRow(row), nil
}

// DecideProposal moves a pending proposal to approved/rejected; 0 rows when
// it was already decided.
func (r *Repository) DecideProposal(ctx context.Context, p Proposal) (int64, error) {
	return r.q.DecideExerciseProposal(ctx, postgres.DecideExerciseProposalParams{ID: p.ID, Status: p.Status, DecidedBy: p.DecidedBy,
		DecidedAt: timestamptzFromPtr(p.DecidedAt), DecisionNote: p.DecisionNote, CatalogExerciseID: p.CatalogExerciseID})
}

// ListProposals lists proposals, optionally of one status.
func (r *Repository) ListProposals(ctx context.Context, status *int16) ([]Proposal, error) {
	arg := pgtype.Int2{}
	if status != nil {
		arg = pgtype.Int2{Int16: *status, Valid: true}
	}
	rows, err := r.q.ListExerciseProposals(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]Proposal, 0, len(rows))
	for _, row := range rows {
		p := proposalFromRow(postgres.ExerciseProposal{ID: row.ID, ExerciseID: row.ExerciseID, EventID: row.EventID, Status: row.Status, Note: row.Note,
			ProposedBy: row.ProposedBy, ProposedAt: row.ProposedAt, DecidedBy: row.DecidedBy, DecidedAt: row.DecidedAt, DecisionNote: row.DecisionNote, CatalogExerciseID: row.CatalogExerciseID})
		p.ExerciseName, p.EventName, p.ProposedByName = row.ExerciseName, row.EventName, row.ProposedByName
		out = append(out, p)
	}
	return out, nil
}

// EventInfrastructureAllowed reads the admin-owned infrastructure flag of an
// event (ErrNoRows when the event does not exist).
func (r *Repository) EventInfrastructureAllowed(ctx context.Context, eventID uuid.UUID) (bool, error) {
	return r.q.GetEventInfrastructureAllowed(ctx, eventID)
}

// FindEventFork returns the event's active, published fork of a source
// exercise (ErrNoRows when none).
func (r *Repository) FindEventFork(ctx context.Context, eventID, sourceID uuid.UUID) (exerciseModel.Exercise, error) {
	row, err := r.q.FindEventFork(ctx, postgres.FindEventForkParams{EventID: uuid.NullUUID{UUID: eventID, Valid: true}, SourceExerciseID: uuid.NullUUID{UUID: sourceID, Valid: true}})
	if err != nil {
		return exerciseModel.Exercise{}, err
	}
	return ToDomain(row), nil
}
