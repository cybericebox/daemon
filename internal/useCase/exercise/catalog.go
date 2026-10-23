package exercise

import (
	"context"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/pkg/pagination"
)

const defaultTagLimit = 50

// Keyset sentinels — see auth.ListUsers.
var maxUUID = uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))
var cursorSentinelTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

// CreateExercise persists a new catalog entry, or an event exercise when
// OwnerEventID is set. Authorization: CreateExerciseFor.
func (u *ExerciseUseCase) CreateExercise(ctx context.Context, in CreateExerciseInput) (ExerciseView, error) {
	var (
		e   exerciseModel.Exercise
		err error
	)
	if in.OwnerEventID != nil {
		e, err = exerciseModel.NewEventExercise(in.Name, in.Description, in.Tags, *in.OwnerEventID, in.CreatedBy, time.Now())
	} else {
		e, err = exerciseModel.NewExercise(in.Name, in.Description, in.Tags, in.CreatedBy, time.Now())
	}
	if err != nil {
		return ExerciseView{}, err
	}
	created, err := u.exercises.Create(ctx, e)
	if err != nil {
		return ExerciseView{}, classifyExerciseWriteError(err, "create")
	}
	return u.exerciseView(ctx, created)
}

// CreateExerciseFor checks where the caller may create (catalog: admins;
// event: its managers) and creates.
func (u *ExerciseUseCase) CreateExerciseFor(ctx context.Context, actor Actor, in CreateExerciseInput) (ExerciseView, error) {
	if err := u.requireCreateScope(ctx, actor, in.OwnerEventID); err != nil {
		return ExerciseView{}, err
	}
	in.CreatedBy = actor.UserID
	view, err := u.CreateExercise(ctx, in)
	if err != nil {
		return ExerciseView{}, err
	}
	return u.decorateView(ctx, actor, view)
}

// GetExerciseFor returns the card with the W4 ownership block and the
// caller's permissions (authorization happened in the route).
func (u *ExerciseUseCase) GetExerciseFor(ctx context.Context, actor Actor, id uuid.UUID) (ExerciseView, error) {
	view, err := u.GetExercise(ctx, id)
	if err != nil {
		return ExerciseView{}, err
	}
	view, err = u.decorateView(ctx, actor, view)
	if err != nil {
		return ExerciseView{}, err
	}
	// The identity columns are the public projection. Only a full working-copy
	// reader receives the draft metadata, just like that reader's version content.
	if view.Permissions.CanRead && view.DraftVersionID != nil {
		draft, readErr := u.exercises.GetDraft(ctx, id)
		if readErr == nil && draft.Name != "" {
			view.Name, view.Description, view.Tags = draft.Name, draft.Description, draft.Tags
		} else if readErr != nil && !repositoryTools.IsObjectNotFoundError(readErr) {
			return ExerciseView{}, model.ErrPlatform.WithError(readErr).WithMessage("Failed to read working metadata").Err()
		}
	}
	return view, nil
}

// decorateView fills names, infrastructure, the access list, a pending
// proposal and the caller's permissions.
func (u *ExerciseUseCase) decorateView(ctx context.Context, actor Actor, view ExerciseView) (ExerciseView, error) {
	e, err := u.exercises.GetByID(ctx, view.ID)
	if err != nil {
		return ExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}
	extras, err := u.exercises.CardExtras(ctx, []uuid.UUID{view.ID})
	if err != nil {
		return ExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to decorate exercise").Err()
	}
	memberships, err := u.membershipMap(ctx, actor)
	if err != nil {
		return ExerciseView{}, err
	}
	applyExtras(&view.ExerciseScopeView, extras[view.ID])
	view.Permissions = permissionsFor(actor, e, memberships)
	if err = u.applyAccessEvents(ctx, actor, []*ExerciseScopeView{&view.ExerciseScopeView}, []uuid.UUID{e.ID}); err != nil {
		return ExerciseView{}, err
	}
	if err = u.applyResources(ctx, actor, []*ExerciseScopeView{&view.ExerciseScopeView}, []uuid.UUID{e.ID}); err != nil {
		return ExerciseView{}, err
	}
	return view, nil
}

// applyResources fills totals, infrastructure and the resource-heavy mark from
// the same actor-visible snapshots as the catalog metadata (one query for the page).
func (u *ExerciseUseCase) applyResources(ctx context.Context, actor Actor, views []*ExerciseScopeView, ids []uuid.UUID) error {
	visible, err := u.visibleResources(ctx, actor, ids)
	if err != nil {
		return err
	}
	for i, v := range views {
		if r, ok := visible[ids[i]]; ok {
			v.Resources, v.ResourceHeavy, v.Infrastructure = r.Range, r.Heavy, r.Infrastructure
		}
	}
	return nil
}

// applyAccessEvents fills the "selected events" of the views (one query for
// all of them). Only platform readers learn which events those are.
func (u *ExerciseUseCase) applyAccessEvents(ctx context.Context, actor Actor, views []*ExerciseScopeView, ids []uuid.UUID) error {
	if !actor.has(rbac.PermExercisesRead) {
		return nil
	}
	selected := make([]uuid.UUID, 0, len(ids))
	for i, v := range views {
		if v.Scope == "catalog" && v.AccessLevel == "selected" {
			selected = append(selected, ids[i])
		}
	}
	if len(selected) == 0 {
		return nil
	}
	access, err := u.exercises.AccessEventsOf(ctx, selected)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list exercise access").Err()
	}
	for i, v := range views {
		for _, ref := range access[ids[i]] {
			v.AccessEventIDs = append(v.AccessEventIDs, ref.ID)
			v.AccessEvents = append(v.AccessEvents, EventRef{ID: ref.ID, Name: ref.Name})
		}
	}
	return nil
}

func applyExtras(v *ExerciseScopeView, extras exerciseRepo.CardExtras) {
	v.OwnerEventName = extras.OwnerEventName
	if v.OwnerEventID != nil {
		v.OwnerEvent = &EventRef{ID: *v.OwnerEventID, Name: extras.OwnerEventName}
	}
	v.Infrastructure = extras.Infrastructure
	v.PendingProposalID = uuidPtr(extras.PendingProposalID)
	if v.ForkedFrom != nil {
		v.ForkedFrom.ExerciseName = extras.ForkedFromName
	}
}

// ListExercisesFor lists what the caller may read (admins: everything) with
// the ownership block and per-item permissions.
func (u *ExerciseUseCase) ListExercisesFor(ctx context.Context, actor Actor, f ExercisesFilter) (ExercisesListResult, error) {
	result, err := u.listExercises(ctx, f, visibilityFor(actor, f))
	if err != nil {
		return ExercisesListResult{}, err
	}
	ids := make([]uuid.UUID, 0, len(result.Exercises))
	for _, item := range result.Exercises {
		ids = append(ids, item.ID)
	}
	extras, err := u.exercises.CardExtras(ctx, ids)
	if err != nil {
		return ExercisesListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to decorate exercises").Err()
	}
	memberships, err := u.membershipMap(ctx, actor)
	if err != nil {
		return ExercisesListResult{}, err
	}
	views := make([]*ExerciseScopeView, 0, len(result.Exercises))
	for i := range result.Exercises {
		item := &result.Exercises[i]
		applyExtras(&item.ExerciseScopeView, extras[item.ID])
		item.Permissions = permissionsFor(actor, listItemEntity(*item), memberships)
		if !item.Permissions.CanRead {
			// A published-only reader never learns about the working copy
			// (Status already ignores it in SQL).
			item.HasDraft = false
		}
		views = append(views, &item.ExerciseScopeView)
	}
	if err = u.applyAccessEvents(ctx, actor, views, ids); err != nil {
		return ExercisesListResult{}, err
	}
	if err = u.applyResources(ctx, actor, views, ids); err != nil {
		return ExercisesListResult{}, err
	}
	return result, nil
}

// listItemEntity rebuilds the fields permissionsFor reads from a list item.
func listItemEntity(item ExerciseListItem) exerciseModel.Exercise {
	e := exerciseModel.Exercise{ID: item.ID, ArchivedAt: item.ArchivedAt, PublishedVersionID: uuid.NullUUID{Valid: item.HasPublished}}
	if item.Scope == "event" {
		e.Scope = exerciseModel.ScopeEvent
	}
	if item.OwnerEventID != nil {
		e.OwnerEventID = uuid.NullUUID{UUID: *item.OwnerEventID, Valid: true}
	}
	return e
}

// GetExercise returns the identity card. Route gate: exercises.read.
func (u *ExerciseUseCase) GetExercise(ctx context.Context, id uuid.UUID) (ExerciseView, error) {
	e, err := u.exercises.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ExerciseView{}, exerciseModel.ErrExerciseNotFound.Err()
		}
		return ExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}
	return u.exerciseView(ctx, e)
}

// ListExercises returns a keyset or offset page filtered by search, tags and
// archive state (default: active only). Route gate: exercises.read.
func (u *ExerciseUseCase) ListExercises(ctx context.Context, f ExercisesFilter) (ExercisesListResult, error) {
	return u.listExercises(ctx, f, visibilityFor(Actor{Role: rbac.RoleSuperAdmin}, f))
}

func (u *ExerciseUseCase) listExercises(ctx context.Context, f ExercisesFilter, visibility exerciseRepo.Visibility) (ExercisesListResult, error) {
	limit := f.PageSize
	if limit <= 0 || limit > pagination.MaxPageSize {
		limit = pagination.DefaultPageSize
	}
	tags := f.Tags
	if tags == nil {
		tags = []string{}
	}
	archived := "exclude"
	if f.Archived == "only" || f.Status == string(exerciseModel.StatusArchived) {
		archived = "only"
	}
	if f.Page > 0 {
		sortBy := f.SortBy
		switch sortBy {
		case "name", "tags", "status", "updated", "author", "difficulty", "subtasks", "variants":
		default:
			sortBy = "updated"
		}
		sortDir := f.SortDir
		if sortDir != "asc" {
			sortDir = "desc"
		}
		statuses, pageArchived := statusFilter(f)
		offset := (int64(f.Page) - 1) * int64(limit)
		if offset < 0 || offset > 2147483647 {
			return ExercisesListResult{}, model.ErrPlatform.WithMessage("Exercise page is out of range").Err()
		}
		params := exerciseRepo.PageParams{
			Search: f.Search, Tags: tags, TagsAll: normalizeTagFilter(f.TagsAll), TagsNone: normalizeTagFilter(f.TagsNone),
			AuthorIDs: f.AuthorIDs, Statuses: statuses, Archived: pageArchived,
			DifficultyMin: difficultyBound(f.DifficultyMin), DifficultyMax: difficultyBound(f.DifficultyMax),
			SortBy: sortBy, SortDir: sortDir, Limit: int32(limit), Offset: int32(offset), Visibility: visibility,
		}
		exercises, err := u.exercises.ListPage(ctx, params)
		if err != nil {
			return ExercisesListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list exercises").Err()
		}
		items := make([]ExerciseListItem, 0, len(exercises))
		for _, e := range exercises {
			item := toExerciseListItem(e.Exercise, e.Status)
			item.AuthorName, item.AvgDifficulty, item.TaskCount, item.TaskNames, item.VariantCount = e.AuthorName, e.AvgDifficulty, e.TaskCount, e.TaskNames, e.VariantCount
			items = append(items, item)
		}
		total, err := u.exercises.CountPage(ctx, params)
		if err != nil {
			return ExercisesListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count exercises").Err()
		}
		return ExercisesListResult{Exercises: items, Total: total}, nil
	}
	curTs, curID := cursorSentinelTime, maxUUID
	if f.Cursor != uuid.Nil {
		if e, err := u.exercises.GetByID(ctx, f.Cursor); err == nil {
			curTs, curID = e.CreatedAt, e.ID
		}
	}
	exercises, err := u.exercises.ListCursor(ctx, exerciseRepo.ListParams{
		Search:          f.Search,
		Tags:            tags,
		Archived:        archived,
		Visibility:      visibility,
		CursorCreatedAt: curTs,
		CursorID:        curID,
		Limit:           int32(limit + 1),
	})
	if err != nil {
		return ExercisesListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list exercises").Err()
	}
	hasMore := false
	if len(exercises) > limit {
		hasMore = true
		exercises = exercises[:limit]
	}
	items := make([]ExerciseListItem, 0, len(exercises))
	for _, e := range exercises {
		items = append(items, toExerciseListItem(e.Exercise, e.Status))
	}
	next := uuid.Nil
	if hasMore && len(exercises) > 0 {
		next = exercises[len(exercises)-1].ID
	}
	total, err := u.exercises.Count(ctx, f.Search, tags, archived, visibility)
	if err != nil {
		return ExercisesListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count exercises").Err()
	}
	return ExercisesListResult{Exercises: items, NextCursor: next, HasMore: hasMore, Total: total}, nil
}

// statusFilter turns the status filter (Status plus Statuses, unknown values dropped) into the statuses to keep
// and the archive filter of a page list: archived alone lists only archived tasks, archived with other statuses
// lists both, no archived keeps archived tasks out (unless Archived is "only").
func statusFilter(f ExercisesFilter) ([]string, string) {
	seen := map[string]bool{}
	statuses := make([]string, 0, len(f.Statuses)+1)
	archived := false
	for _, status := range append([]string{f.Status}, f.Statuses...) {
		if !exerciseModel.CatalogStatus(status).Valid() || seen[status] {
			continue
		}
		seen[status] = true
		statuses = append(statuses, status)
		archived = archived || status == string(exerciseModel.StatusArchived)
	}
	switch {
	case f.Archived == "only" || (archived && len(statuses) == 1):
		return statuses, "only"
	case archived:
		return statuses, "any"
	default:
		return statuses, "exclude"
	}
}

// normalizeTagFilter lower-cases and trims the tags of a filter (tags are stored lower-case), dropping empty ones.
func normalizeTagFilter(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag = strings.ToLower(strings.TrimSpace(tag)); tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

// difficultyBound keeps a difficulty level bound within 1..6; anything else is no bound (0).
func difficultyBound(level int) int32 {
	if level < 1 || level > 6 {
		return 0
	}
	return int32(level)
}

// ListExerciseAuthors suggests the authors of the tasks the caller may read, by use; search matches the name, ids
// (non-empty) asks for those authors only. limit <= 0 means the default (50). Names only, never emails.
func (u *ExerciseUseCase) ListExerciseAuthors(ctx context.Context, actor Actor, search string, ids []uuid.UUID, limit int) ([]ExerciseAuthorSuggestion, error) {
	if limit <= 0 {
		limit = defaultTagLimit
	}
	limit = min(limit, pagination.MaxPageSize)
	viewer := uuid.NullUUID{}
	if !actor.has(rbac.PermExercisesRead) {
		viewer = uuid.NullUUID{UUID: actor.UserID, Valid: true}
	}
	items, err := u.exercises.ListAuthors(ctx, strings.TrimSpace(search), ids, viewer, int32(limit))
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list exercise authors").Err()
	}
	out := make([]ExerciseAuthorSuggestion, 0, len(items))
	for _, item := range items {
		out = append(out, ExerciseAuthorSuggestion{ID: item.ID, Name: item.Name, Count: item.Count})
	}
	return out, nil
}

// ListExerciseTags suggests existing tags by use: those starting with prefix,
// or the most used when it is empty. limit <= 0 means the default (50).
// Non-admins count only the exercises they may read.
func (u *ExerciseUseCase) ListExerciseTags(ctx context.Context, actor Actor, prefix string, limit int) ([]ExerciseTagSuggestion, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if limit <= 0 {
		limit = defaultTagLimit
	}
	limit = min(limit, pagination.MaxPageSize)
	viewer := uuid.NullUUID{}
	if !actor.has(rbac.PermExercisesRead) {
		viewer = uuid.NullUUID{UUID: actor.UserID, Valid: true}
	}
	items, err := u.exercises.ListTags(ctx, prefix, viewer, int32(limit))
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list exercise tags").Err()
	}
	out := make([]ExerciseTagSuggestion, 0, len(items))
	for _, item := range items {
		out = append(out, ExerciseTagSuggestion{Tag: item.Tag, Count: item.Count})
	}
	return out, nil
}

// UpdateExerciseIdentity preserves the PATCH DTO while editing the full draft
// snapshot. The public identity projection changes only in Publish's SQL CTE.
func (u *ExerciseUseCase) UpdateExerciseIdentity(ctx context.Context, in UpdateExerciseInput) (ExerciseView, error) {
	e, err := u.loadEditable(ctx, in.ID)
	if err != nil {
		return ExerciseView{}, err
	}
	expected := e.UpdatedAt
	if err = e.UpdateIdentity(in.Name, in.Description, in.Tags, in.UpdatedBy, time.Now()); err != nil {
		return ExerciseView{}, err
	}
	draft, err := u.exercises.SaveDraftIdentity(ctx, e, expected, freshID())
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			if _, readErr := u.exercises.GetByID(ctx, in.ID); repositoryTools.IsObjectNotFoundError(readErr) {
				return ExerciseView{}, exerciseModel.ErrExerciseNotFound.Err()
			} else if readErr != nil {
				return ExerciseView{}, model.ErrPlatform.WithError(readErr).Err()
			}
			return ExerciseView{}, exerciseModel.ErrExerciseModified.Err()
		}
		return ExerciseView{}, classifyExerciseWriteError(err, "save draft metadata")
	}
	e.DraftVersionID = uuid.NullUUID{UUID: draft.ID, Valid: true}
	return u.exerciseView(ctx, e)
}

// ArchiveExercise hides the exercise from the catalog and freezes it; event
// attachments keep working. Idempotent. Route gate: exercises.write.
func (u *ExerciseUseCase) ArchiveExercise(ctx context.Context, id, by uuid.UUID) (ExerciseView, error) {
	e, err := u.mutateExercise(ctx, id, func(e *exerciseModel.Exercise) error {
		e.Archive(by, time.Now())
		return nil
	})
	if err != nil {
		return ExerciseView{}, err
	}
	return u.exerciseView(ctx, e)
}

// UnarchiveExercise returns the exercise to the catalog. Idempotent. Route
// gate: exercises.write.
func (u *ExerciseUseCase) UnarchiveExercise(ctx context.Context, id, by uuid.UUID) (ExerciseView, error) {
	e, err := u.mutateExercise(ctx, id, func(e *exerciseModel.Exercise) error {
		e.Unarchive(by, time.Now())
		return nil
	})
	if err != nil {
		return ExerciseView{}, err
	}
	return u.exerciseView(ctx, e)
}

// GetExerciseUsage lists the events using the exercise. Route gate:
// exercises.read.
func (u *ExerciseUseCase) GetExerciseUsage(ctx context.Context, id uuid.UUID) (ExerciseUsage, error) {
	if _, err := u.exercises.GetByID(ctx, id); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ExerciseUsage{}, exerciseModel.ErrExerciseNotFound.Err()
		}
		return ExerciseUsage{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}
	events, err := u.exercises.ListUsage(ctx, id, time.Now())
	if err != nil {
		return ExerciseUsage{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list exercise usage").Err()
	}
	out := ExerciseUsage{Events: make([]ExerciseUsageEvent, 0, len(events))}
	for _, e := range events {
		out.Events = append(out.Events, ExerciseUsageEvent{ID: e.ID, Name: e.Name, Archived: e.Archived})
	}
	return out, nil
}

// DeleteExercise removes the entry (versions cascade in SQL) unless an event
// uses it, then closes the admins' requests of its pending resource elevations
// and clears the versions' media references. Reference cleanup runs
// AFTER the delete: if it fails the files simply stay referenced until an
// operator retries — never the other way around (premature GC). Route gate:
// exercises.delete.
func (u *ExerciseUseCase) DeleteExercise(ctx context.Context, id uuid.UUID) error {
	if err := u.ensureUnused(ctx, id); err != nil {
		return err
	}
	versionIDs, err := u.exercises.ListVersionIDs(ctx, id)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list exercise versions").Err()
	}
	// The elevation rows cascade with the exercise; their admin inbox requests are closed after the delete.
	pendingElevations := u.pendingElevations(ctx, id)
	affected, err := u.exercises.Delete(ctx, id)
	if err != nil {
		if repositoryTools.IsForeignKeyViolation(err) {
			// event_exercises.exercise_id is ON DELETE RESTRICT: an event
			// attached the exercise between the usage check and the DELETE.
			if usedErr := u.ensureUnused(ctx, id); usedErr != nil {
				return usedErr
			}
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete exercise").Err()
	}
	if affected == 0 {
		return exerciseModel.ErrExerciseNotFound.Err()
	}
	u.elevationsWithdrawn(ctx, pendingElevations)
	return u.media.RemoveReferencesBatch(ctx, mediaModel.RefTypeExerciseVersion, versionIDs)
}

func (u *ExerciseUseCase) ensureUnused(ctx context.Context, id uuid.UUID) error {
	events, err := u.exercises.ListUsage(ctx, id, time.Now())
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to check exercise usage").Err()
	}
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, e.Name)
	}
	return exerciseModel.EnsureNotInUse(names)
}
