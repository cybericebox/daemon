package event

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventChallengeRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventExerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/flagpattern"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// AttachExercise pins a published version of an exercise available to the
// event (its own, or a catalog exercise whose access level admits it).
func (u *EventUseCase) AttachExercise(ctx context.Context, eventID uuid.UUID, in AttachExerciseInput, by uuid.UUID) (EventExerciseView, error) {
	version, catalogEntry, err := u.attachableVersion(ctx, eventID, in.ExerciseVersionID, true)
	if err != nil {
		return EventExerciseView{}, err
	}
	link, err := eventExerciseModel.New(eventID, version.ExerciseID, version.ID, in.VariantMode, in.FixedVariantIndex, time.Now(), by)
	if err != nil {
		return EventExerciseView{}, err
	}
	if link.FixedVariantIndex != nil && int(*link.FixedVariantIndex) >= len(version.Variants) {
		return EventExerciseView{}, eventExerciseModel.ErrEventExerciseVariantModeInvalid.Err()
	}
	family, err := u.eventExercises.HasActiveFamily(ctx, eventID, catalogEntry.ID)
	if err != nil {
		return EventExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to check event exercises").Err()
	}
	if family {
		return EventExerciseView{}, eventExerciseModel.ErrEventExerciseExists.Err()
	}
	if u.uow == nil {
		return EventExerciseView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return EventExerciseView{}, err
	}
	defer unit.Restore()
	created, err := eventExerciseRepo.New(txRepo).Create(txCtx, link)
	if err != nil {
		if creator, ok := repositoryTools.UniqueViolationError(err, eventExerciseModel.ErrEventExerciseExists); ok {
			return EventExerciseView{}, creator.Err()
		}
		return EventExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to attach event exercise").Err()
	}
	if err = materializeChallenges(txCtx, txRepo, created, version); err != nil {
		return EventExerciseView{}, err
	}
	if err = unit.Save(); err != nil {
		return EventExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to attach event exercise").Err()
	}
	view := toEventExerciseView(created)
	view.ExerciseName = catalogEntry.Name
	return view, nil
}

// attachableVersion loads a version an event may pin: published (or, for
// reverting, once published), of an active exercise available to the event,
// with infrastructure only when the event allows it.
func (u *EventUseCase) attachableVersion(ctx context.Context, eventID, versionID uuid.UUID, currentOnly bool) (exerciseModel.ExerciseVersion, exerciseModel.Exercise, error) {
	version, err := u.exercises.GetVersion(ctx, versionID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return exerciseModel.ExerciseVersion{}, exerciseModel.Exercise{}, eventExerciseModel.ErrEventExerciseNotFound.Err()
		}
		return exerciseModel.ExerciseVersion{}, exerciseModel.Exercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise version").Err()
	}
	if (currentOnly && version.Status != exerciseModel.VersionStatusPublished) || version.PublishedAt == nil {
		return exerciseModel.ExerciseVersion{}, exerciseModel.Exercise{}, eventExerciseModel.ErrEventExerciseVersionNotPublished.Err()
	}
	if len(version.Variants) == 0 {
		return exerciseModel.ExerciseVersion{}, exerciseModel.Exercise{}, model.ErrPlatform.WithMessage("Published exercise has no variants").Err()
	}
	entry, err := u.exercises.GetByID(ctx, version.ExerciseID)
	if err != nil {
		return exerciseModel.ExerciseVersion{}, exerciseModel.Exercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}
	// Check-then-write: a concurrent archive between this read and the write
	// may let one in-flight attach through. Accepted as low impact.
	if err = entry.EnsureNotArchived(); err != nil {
		return exerciseModel.ExerciseVersion{}, exerciseModel.Exercise{}, err
	}
	available, err := u.exercises.AvailableToEvent(ctx, entry.ID, eventID)
	if err != nil {
		return exerciseModel.ExerciseVersion{}, exerciseModel.Exercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to check exercise access").Err()
	}
	if !available {
		return exerciseModel.ExerciseVersion{}, exerciseModel.Exercise{}, eventExerciseModel.ErrEventExerciseNotAvailable.Err()
	}
	if err = u.requireInfrastructureForVersion(ctx, eventID, version); err != nil {
		return exerciseModel.ExerciseVersion{}, exerciseModel.Exercise{}, err
	}
	return version, entry, nil
}

func toEventExerciseView(value eventExerciseModel.EventExercise) EventExerciseView {
	return EventExerciseView{ID: value.ID, ExerciseID: value.ExerciseID, ExerciseVersionID: value.ExerciseVersionID, VariantMode: value.VariantMode, FixedVariantIndex: value.FixedVariantIndex, Revision: value.Revision, Status: value.Status, ReplacesID: value.ReplacesID, SupersededAt: value.SupersededAt, DetachedAt: value.DetachedAt, CreatedAt: value.CreatedAt}
}

// ListEventExercises returns every attachment with names, catalog version
// numbers, newer versions and fork sources in one query.
func (u *EventUseCase) ListEventExercises(ctx context.Context, eventID uuid.UUID) ([]EventExerciseView, error) {
	rows, err := u.eventExercises.Details(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event exercises").Err()
	}
	items := make([]EventExerciseView, 0, len(rows))
	for _, row := range rows {
		items = append(items, toEventExerciseDetailView(row))
	}
	return items, nil
}

func toEventExerciseDetailView(row eventExerciseRepo.Detail) EventExerciseView {
	view := toEventExerciseView(row.EventExercise)
	view.ExerciseName = row.ExerciseName
	view.Scope = "catalog"
	if exerciseModel.Scope(row.Scope) == exerciseModel.ScopeEvent {
		view.Scope = "event"
	}
	view.VersionNumber, view.LatestVersionNumber = row.VersionNumber, row.LatestVersionNumber
	if row.LatestVersionID.Valid {
		latest := row.LatestVersionID.UUID
		view.LatestVersionID = &latest
		view.UpdateAvailable = latest != row.EventExercise.ExerciseVersionID
	}
	if row.ForkedFromExerciseID.Valid {
		fork := &EventExerciseForkView{SourceExerciseID: row.ForkedFromExerciseID.UUID, SourceExerciseName: row.SourceName,
			SourceVersionNumber: row.SourceVersionNumber, SourceLatestVersionNumber: row.SourceLatestVersionNumber}
		if row.ForkedFromVersionID.Valid {
			id := row.ForkedFromVersionID.UUID
			fork.SourceVersionID = &id
		}
		if row.SourceLatestVersionID.Valid {
			id := row.SourceLatestVersionID.UUID
			fork.SourceLatestVersionID = &id
			fork.SourceUpdateAvailable = fork.SourceVersionID == nil || *fork.SourceVersionID != id
		}
		view.Fork = fork
	}
	view.Infrastructure, view.VariantCount = row.Infrastructure, row.VariantCount
	view.ChallengeCount, view.PublishedCount, view.HasAttempts = row.ChallengeCount, row.PublishedCount, row.HasAttempts
	return view
}

// PublishedExerciseChoice exposes only attachable catalog identities to an
// event manager. Draft content and other catalog administration stay private.
type PublishedExerciseChoice = EventCatalogItem

// PublishedExercisePreview deliberately exposes only catalog metadata and task
// labels of one variant. Flags, topology, placeholders and notes stay private.
type PublishedExercisePreview struct {
	ID           uuid.UUID
	Name         string
	Description  string
	VersionID    uuid.UUID
	VariantCount int
	Variant      int
	Tasks        []PublishedExerciseTaskPreview
}

type PublishedExerciseTaskPreview struct {
	Name       string
	Difficulty exerciseModel.Difficulty
	HintCount  int
}

// GetPublishedExercisePreviewForEvent shows the tasks of one variant of a
// version the event may attach (E5: not only variant 0).
func (u *EventUseCase) GetPublishedExercisePreviewForEvent(ctx context.Context, eventID, versionID uuid.UUID, variant int) (PublishedExercisePreview, error) {
	version, entry, err := u.attachableVersion(ctx, eventID, versionID, false)
	if err != nil {
		return PublishedExercisePreview{}, err
	}
	if variant < 0 || variant >= len(version.Variants) {
		return PublishedExercisePreview{}, eventExerciseModel.ErrEventExerciseVariantModeInvalid.Err()
	}
	preview := PublishedExercisePreview{ID: entry.ID, Name: entry.Name, Description: entry.Description, VersionID: version.ID, VariantCount: len(version.Variants), Variant: variant, Tasks: []PublishedExerciseTaskPreview{}}
	for _, task := range version.Variants[variant].Tasks {
		preview.Tasks = append(preview.Tasks, PublishedExerciseTaskPreview{Name: task.Name, Difficulty: task.Difficulty, HintCount: len(task.Hints)})
	}
	return preview, nil
}

// Event catalog tag suggestions: the default and the largest page.
const (
	defaultCatalogTagLimit = 50
	maxCatalogTagLimit     = 200
)

// normalizeCatalogTags lowercases, trims and de-duplicates the tag filter the
// way exercise tags are stored; blanks are dropped.
func normalizeCatalogTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag != "" && !slices.Contains(out, tag) {
			out = append(out, tag)
		}
	}
	return out
}

// ListPublishedExercisesForEvent lists what the event may attach: its own
// published exercises and the catalog exercises available to it. tags keep
// the exercises carrying any of them (union); empty means no tag filter.
func (u *EventUseCase) ListPublishedExercisesForEvent(ctx context.Context, eventID uuid.UUID, search, infrastructure string, tags []string) ([]PublishedExerciseChoice, error) {
	if infrastructure != "yes" && infrastructure != "no" {
		infrastructure = ""
	}
	rows, err := u.eventExercises.Catalog(ctx, eventID, strings.TrimSpace(search), infrastructure, normalizeCatalogTags(tags))
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list published exercises").Err()
	}
	items := make([]PublishedExerciseChoice, 0, len(rows))
	for _, row := range rows {
		scope := "catalog"
		if exerciseModel.Scope(row.Scope) == exerciseModel.ScopeEvent {
			scope = "event"
		}
		items = append(items, EventCatalogItem{ID: row.ID, Name: row.Name, Description: row.Description, Tags: row.Tags, Scope: scope,
			PublishedVersionID: row.PublishedVersionID, Infrastructure: row.Infrastructure, Attached: row.Attached})
	}
	return items, nil
}

// ListEventCatalogTags suggests the tags of the exercises the event may attach
// (the same set ListPublishedExercisesForEvent searches), most used first.
// limit <= 0 means the default (50); it is capped at 200.
func (u *EventUseCase) ListEventCatalogTags(ctx context.Context, eventID uuid.UUID, prefix string, limit int) ([]EventCatalogTag, error) {
	if limit <= 0 {
		limit = defaultCatalogTagLimit
	}
	limit = min(limit, maxCatalogTagLimit)
	rows, err := u.eventExercises.CatalogTags(ctx, eventID, strings.ToLower(strings.TrimSpace(prefix)), int32(limit))
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event catalog tags").Err()
	}
	out := make([]EventCatalogTag, 0, len(rows))
	for _, row := range rows {
		out = append(out, EventCatalogTag{Tag: row.Tag, ExerciseCount: row.Count})
	}
	return out, nil
}

// ReplaceEventExercise switches the attachment to another published version
// of the same exercise; kept as the older route of UpdateEventExercise.
func (u *EventUseCase) ReplaceEventExercise(ctx context.Context, eventID, eventExerciseID uuid.UUID, in ReplaceEventExerciseInput, by uuid.UUID) (EventExerciseView, error) {
	versionID := in.ExerciseVersionID
	return u.UpdateEventExercise(ctx, eventID, eventExerciseID, &versionID, by)
}

// UpdateEventExercise («Оновити») switches the attachment in place to the
// exercise's latest published version (or the given one of the same
// exercise), keeping every event override (E4).
func (u *EventUseCase) UpdateEventExercise(ctx context.Context, eventID, eventExerciseID uuid.UUID, versionID *uuid.UUID, by uuid.UUID) (EventExerciseView, error) {
	link, err := u.activeAttachment(ctx, eventID, eventExerciseID)
	if err != nil {
		return EventExerciseView{}, err
	}
	target := uuid.Nil
	if versionID != nil {
		target = *versionID
	} else {
		entry, getErr := u.exercises.GetByID(ctx, link.ExerciseID)
		if getErr != nil {
			return EventExerciseView{}, model.ErrPlatform.WithError(getErr).WithMessage("Failed to get exercise").Err()
		}
		if !entry.PublishedVersionID.Valid {
			return EventExerciseView{}, eventExerciseModel.ErrEventExerciseVersionNotPublished.Err()
		}
		target = entry.PublishedVersionID.UUID
	}
	version, entry, err := u.attachableVersion(ctx, eventID, target, true)
	if err != nil {
		return EventExerciseView{}, err
	}
	if version.ExerciseID != link.ExerciseID {
		return EventExerciseView{}, eventExerciseModel.ErrEventExerciseVersionMismatch.Err()
	}
	return u.switchInTransaction(ctx, eventID, eventExerciseID, entry, version, nil)
}

// ForkEventExercise («Налаштувати під захід») switches the attachment to the
// event's own copy of its catalog exercise: an existing fork is reused, else
// one is created with the pinned content as its first published version.
func (u *EventUseCase) ForkEventExercise(ctx context.Context, eventID, eventExerciseID, by uuid.UUID) (EventExerciseView, error) {
	link, err := u.activeAttachment(ctx, eventID, eventExerciseID)
	if err != nil {
		return EventExerciseView{}, err
	}
	source, err := u.exercises.GetByID(ctx, link.ExerciseID)
	if err != nil {
		return EventExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}
	if source.IsEventScoped() {
		return EventExerciseView{}, eventExerciseModel.ErrEventExerciseNoForkSource.Err()
	}
	if existing, findErr := u.exercises.FindEventFork(ctx, eventID, source.ID); findErr == nil {
		version, entry, versionErr := u.attachableVersion(ctx, eventID, existing.PublishedVersionID.UUID, true)
		if versionErr != nil {
			return EventExerciseView{}, versionErr
		}
		return u.switchInTransaction(ctx, eventID, eventExerciseID, entry, version, nil)
	} else if !repositoryTools.IsObjectNotFoundError(findErr) {
		return EventExerciseView{}, model.ErrPlatform.WithError(findErr).WithMessage("Failed to find event fork").Err()
	}
	pinned, err := u.exercises.GetVersion(ctx, link.ExerciseVersionID)
	if err != nil {
		return EventExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get pinned version").Err()
	}
	// The copy carries the pinned lab topology: it needs infrastructure too.
	if err = u.requireInfrastructureForVersion(ctx, eventID, pinned); err != nil {
		return EventExerciseView{}, err
	}
	now := time.Now()
	fork, err := exerciseModel.NewFork(source, pinned.ID, eventID, by, now)
	if err != nil {
		return EventExerciseView{}, err
	}
	version := exerciseModel.ExerciseVersion{ID: uuid.Must(uuid.NewV7()), ExerciseID: fork.ID, Status: exerciseModel.VersionStatusPublished,
		AdminNote: pinned.AdminNote, Variants: pinned.Variants, CreatedAt: now, CreatedBy: uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}, PublishedAt: &now}
	create := func(ctx context.Context, repo IRepository) error {
		exercises := exerciseRepo.New(repo)
		created, createErr := exercises.Create(ctx, fork)
		if createErr != nil {
			if _, exists := repositoryTools.UniqueViolationError(createErr, exerciseModel.ErrExerciseExists); !exists {
				return model.ErrPlatform.WithError(createErr).WithMessage("Failed to create event fork").Err()
			}
			// The event already has an exercise with that name: name the
			// copy apart instead of failing the action.
			fork.Name = forkName(source.Name)
			if created, createErr = exercises.Create(ctx, fork); createErr != nil {
				return model.ErrPlatform.WithError(createErr).WithMessage("Failed to create event fork").Err()
			}
		}
		if _, createErr = exercises.InsertImportedVersion(ctx, version); createErr != nil {
			return model.ErrPlatform.WithError(createErr).WithMessage("Failed to copy exercise version").Err()
		}
		fork = created
		return exercises.SetImportedPointers(ctx, created.ID, uuid.NullUUID{}, uuid.NullUUID{UUID: version.ID, Valid: true})
	}
	view, err := u.switchInTransaction(ctx, eventID, eventExerciseID, fork, version, create)
	if err != nil {
		return EventExerciseView{}, err
	}
	view.ExerciseName = fork.Name
	if u.brandMedia != nil {
		if refErr := u.brandMedia.ReplaceReferences(ctx, mediaModel.RefTypeExerciseVersion, version.ID, exerciseModel.CollectFileIDs(version.Variants)); refErr != nil {
			log.Error().Err(refErr).Str("version_id", version.ID.String()).Msg("Failed to reference fork attachments")
		}
	}
	return view, nil
}

// forkName names an event copy apart from an existing event exercise,
// within the 50-character name limit.
func forkName(name string) string {
	const suffix = " (копія)"
	runes := []rune(name)
	if limit := 50 - len([]rune(suffix)); len(runes) > limit {
		runes = runes[:limit]
	}
	return strings.TrimSpace(string(runes)) + suffix
}

// RevertEventExercise («Повернути оригінал») switches a fork attachment back
// to the catalog version it was forked from (the fork itself stays).
func (u *EventUseCase) RevertEventExercise(ctx context.Context, eventID, eventExerciseID uuid.UUID) (EventExerciseView, error) {
	link, err := u.activeAttachment(ctx, eventID, eventExerciseID)
	if err != nil {
		return EventExerciseView{}, err
	}
	fork, err := u.exercises.GetByID(ctx, link.ExerciseID)
	if err != nil {
		return EventExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}
	if !fork.IsEventScoped() || !fork.ForkedFromExerciseID.Valid {
		return EventExerciseView{}, eventExerciseModel.ErrEventExerciseNoForkSource.Err()
	}
	target := fork.ForkedFromVersionID
	if !target.Valid {
		source, getErr := u.exercises.GetByID(ctx, fork.ForkedFromExerciseID.UUID)
		if getErr != nil {
			return EventExerciseView{}, model.ErrPlatform.WithError(getErr).WithMessage("Failed to get fork source").Err()
		}
		target = source.PublishedVersionID
	}
	if !target.Valid {
		return EventExerciseView{}, eventExerciseModel.ErrEventExerciseVersionNotPublished.Err()
	}
	version, entry, err := u.attachableVersion(ctx, eventID, target.UUID, false)
	if err != nil {
		return EventExerciseView{}, err
	}
	return u.switchInTransaction(ctx, eventID, eventExerciseID, entry, version, nil)
}

func (u *EventUseCase) activeAttachment(ctx context.Context, eventID, eventExerciseID uuid.UUID) (eventExerciseModel.EventExercise, error) {
	link, err := u.eventExercises.GetByID(ctx, eventID, eventExerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventExerciseModel.EventExercise{}, eventExerciseModel.ErrEventExerciseNotFound.Err()
		}
		return eventExerciseModel.EventExercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event exercise").Err()
	}
	return link, link.EnsureActive()
}

// switchInTransaction runs an optional preparation (creating a fork) and the
// in-place source switch in one transaction.
func (u *EventUseCase) switchInTransaction(ctx context.Context, eventID, eventExerciseID uuid.UUID, entry exerciseModel.Exercise, version exerciseModel.ExerciseVersion, prepare func(context.Context, IRepository) error) (EventExerciseView, error) {
	if u.uow == nil {
		return EventExerciseView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return EventExerciseView{}, err
	}
	defer unit.Restore()
	if prepare != nil {
		if err = prepare(txCtx, txRepo); err != nil {
			return EventExerciseView{}, err
		}
		entry.ID = version.ExerciseID
	}
	switched, err := u.switchSource(txCtx, txRepo, eventID, eventExerciseID, version, time.Now())
	if err != nil {
		return EventExerciseView{}, err
	}
	if err = unit.Save(); err != nil {
		return EventExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to switch event exercise").Err()
	}
	view := toEventExerciseView(switched)
	view.ExerciseName = entry.Name
	return view, nil
}

// switchSource moves an active attachment to another version in place. Board
// challenges are matched by task ID so every event override stays; new tasks
// get unpublished challenges; removed tasks are deleted unless attempted
// (then the switch is refused). Team assignments are refreshed for their
// pinned variant and marked «Оновлено» when their visible content changed.
func (u *EventUseCase) switchSource(ctx context.Context, repo IRepository, eventID, eventExerciseID uuid.UUID, version exerciseModel.ExerciseVersion, now time.Time) (eventExerciseModel.EventExercise, error) {
	attachments := eventExerciseRepo.New(repo)
	link, err := attachments.GetByID(ctx, eventID, eventExerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventExerciseModel.EventExercise{}, eventExerciseModel.ErrEventExerciseNotFound.Err()
		}
		return eventExerciseModel.EventExercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event exercise").Err()
	}
	if err = link.EnsureActive(); err != nil {
		return eventExerciseModel.EventExercise{}, err
	}
	if link.FixedVariantIndex != nil && int(*link.FixedVariantIndex) >= len(version.Variants) {
		return eventExerciseModel.EventExercise{}, eventExerciseModel.ErrEventExerciseVariantModeInvalid.Err()
	}
	previous, err := exerciseRepo.New(repo).GetVersion(ctx, link.ExerciseVersionID)
	if err != nil {
		return eventExerciseModel.EventExercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get pinned version").Err()
	}
	tasks := version.Variants[link.CanonicalVariant(len(version.Variants))].Tasks
	taskByID := make(map[uuid.UUID]exerciseModel.Task, len(tasks))
	for _, task := range tasks {
		taskByID[task.ID] = task
	}
	challenges := eventChallengeRepo.New(repo)
	board, err := challenges.List(ctx, link.ID)
	if err != nil {
		return eventExerciseModel.EventExercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	setShown := false
	for _, challenge := range board {
		setShown = setShown || challenge.Published
	}
	existing := make(map[uuid.UUID]bool, len(board))
	removed, kept := make([]uuid.UUID, 0), make([]uuid.UUID, 0, len(board))
	for i := range board {
		existing[board[i].TaskID] = true
		task, found := taskByID[board[i].TaskID]
		if !found {
			removed = append(removed, board[i].ID)
			continue
		}
		kept = append(kept, board[i].ID)
		if err = board[i].RefreshContent(task); err != nil {
			return eventExerciseModel.EventExercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to snapshot event challenge").Err()
		}
		if _, err = challenges.UpdateContent(ctx, board[i]); err != nil {
			return eventExerciseModel.EventExercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to refresh event challenge").Err()
		}
	}
	attempted, err := challenges.WithAttempts(ctx, removed)
	if err != nil {
		return eventExerciseModel.EventExercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to check challenge attempts").Err()
	}
	if len(attempted) > 0 {
		return eventExerciseModel.EventExercise{}, eventExerciseModel.ErrEventExerciseTaskHasAttempts.Err()
	}
	if err = challenges.DeleteWithAssignments(ctx, link.ID, removed); err != nil {
		return eventExerciseModel.EventExercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to remove event challenges").Err()
	}
	order, err := challenges.MaxOrder(ctx, link.ID)
	if err != nil {
		return eventExerciseModel.EventExercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read challenge order").Err()
	}
	for _, task := range tasks {
		if existing[task.ID] {
			continue
		}
		order++
		challenge, newErr := eventChallengeModel.New(link.ID, task, order, now)
		if newErr != nil {
			return eventExerciseModel.EventExercise{}, newErr
		}
		// A new task follows its set's visibility (a set is all-or-nothing).
		challenge.SetPublished(setShown)
		if _, err = challenges.Create(ctx, challenge); err != nil {
			return eventExerciseModel.EventExercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to materialize event challenge").Err()
		}
	}
	if err = u.refreshTeamAssignments(ctx, repo, link, previous, version, board, now); err != nil {
		return eventExerciseModel.EventExercise{}, err
	}
	switched, err := attachments.UpdateSource(ctx, eventID, link.ID, version.ExerciseID, version.ID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventExerciseModel.EventExercise{}, eventExerciseModel.ErrEventExerciseNotActive.Err()
		}
		return eventExerciseModel.EventExercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to switch event exercise").Err()
	}
	return switched, nil
}

// refreshTeamAssignments rewrites every team assignment of the kept board
// challenges from the new version: same variant (re-selected only when it no
// longer exists), new snapshot and hint texts; the flag of an unsolved static
// task is re-resolved only when its flag source changed (lab-injected flags
// stay with the running Lab).
func (u *EventUseCase) refreshTeamAssignments(ctx context.Context, repo IRepository, link eventExerciseModel.EventExercise, previous, next exerciseModel.ExerciseVersion, board []eventChallengeModel.EventChallenge, now time.Time) error {
	taskOf := make(map[uuid.UUID]uuid.UUID, len(board))
	ids := make([]uuid.UUID, 0, len(board))
	for _, challenge := range board {
		taskOf[challenge.ID] = challenge.TaskID
		ids = append(ids, challenge.ID)
	}
	teamChallenges := teamChallengeRepo.New(repo)
	rows, err := teamChallenges.ForRefresh(ctx, ids)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list team challenges").Err()
	}
	for _, row := range rows {
		taskID, known := taskOf[row.EventChallengeID]
		if !known {
			continue
		}
		variantIndex := row.VariantIndex
		if link.VariantMode == eventExerciseModel.VariantModeFixed && link.FixedVariantIndex != nil {
			variantIndex = *link.FixedVariantIndex
		} else if int(variantIndex) >= len(next.Variants) {
			if variantIndex, err = teamChallengeModel.SelectVariant(row.EventTeamID, link.ID, len(next.Variants)); err != nil {
				return err
			}
		}
		task, found := findTask(next, variantIndex, taskID)
		if !found {
			continue
		}
		snapshot, snapErr := eventChallengeModel.SnapshotForTask(task)
		if snapErr != nil {
			return model.ErrPlatform.WithError(snapErr).WithMessage("Failed to snapshot team challenge").Err()
		}
		hints := teamHints(task)
		changed := !jsonEqual(snapshot, row.Snapshot) || !slices.Equal(hints, row.Hints)
		if !row.Solved && !task.LinkedDeviceID.Valid {
			oldTask, hadTask := findTask(previous, row.VariantIndex, taskID)
			if !hadTask || variantIndex != row.VariantIndex || !slices.Equal(oldTask.Flag, task.Flag) {
				if row.ExpectedFlag, err = flagpattern.Resolve(task.Flag, u.flagRandomBytes, rand.Reader); err != nil {
					return model.ErrPlatform.WithError(err).WithMessage("Failed to resolve team challenge flag").Err()
				}
			}
		}
		row.VariantIndex, row.Snapshot, row.Hints = variantIndex, snapshot, hints
		if _, err = teamChallenges.UpdateContent(ctx, row, changed, now); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to refresh team challenge").Err()
		}
	}
	return nil
}

func findTask(version exerciseModel.ExerciseVersion, variant int32, taskID uuid.UUID) (exerciseModel.Task, bool) {
	if variant < 0 || int(variant) >= len(version.Variants) {
		return exerciseModel.Task{}, false
	}
	for _, task := range version.Variants[variant].Tasks {
		if task.ID == taskID {
			return task, true
		}
	}
	return exerciseModel.Task{}, false
}

// teamHints copies a task variant's hint texts for one team assignment.
func teamHints(task exerciseModel.Task) []teamChallengeModel.Hint {
	out := make([]teamChallengeModel.Hint, 0, len(task.Hints))
	for _, hint := range task.Hints {
		out = append(out, teamChallengeModel.Hint{ID: hint.ID, Text: hint.Text})
	}
	return out
}

// jsonEqual compares two JSON documents semantically (key order and spacing
// ignored).
func jsonEqual(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

// DetachEventExercise removes an exercise from the event. Without attempts
// its board challenges and team assignments are deleted; with attempts it
// needs confirmation and stays as detached evidence (off boards and scores).
func (u *EventUseCase) DetachEventExercise(ctx context.Context, eventID, eventExerciseID uuid.UUID, confirmed bool, by uuid.UUID) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	attachments := eventExerciseRepo.New(txRepo)
	link, err := attachments.GetByID(txCtx, eventID, eventExerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventExerciseModel.ErrEventExerciseNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event exercise").Err()
	}
	if err = link.EnsureActive(); err != nil {
		return err
	}
	attempted, err := attachments.HasAttempts(txCtx, link.ID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to check exercise attempts").Err()
	}
	keep, err := eventExerciseModel.DetachDecision(attempted, confirmed)
	if err != nil {
		return err
	}
	challenges := eventChallengeRepo.New(txRepo)
	if keep {
		if _, err = attachments.Detach(txCtx, eventID, link.ID, by, time.Now()); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to detach event exercise").Err()
		}
		if err = challenges.UnpublishAll(txCtx, link.ID); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to unpublish detached challenges").Err()
		}
		if err = recordScoreboardRecalculation(txCtx, txRepo, eventID); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to record result change").Err()
		}
	} else {
		board, listErr := challenges.List(txCtx, link.ID)
		if listErr != nil {
			return model.ErrPlatform.WithError(listErr).WithMessage("Failed to list event challenges").Err()
		}
		ids := make([]uuid.UUID, 0, len(board))
		for _, challenge := range board {
			ids = append(ids, challenge.ID)
		}
		if err = challenges.DeleteWithAssignments(txCtx, link.ID, ids); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to remove event challenges").Err()
		}
		if _, err = attachments.Delete(txCtx, eventID, link.ID); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to detach event exercise").Err()
		}
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to detach event exercise").Err()
	}
	if u.supportsLabAccessPolicy() {
		return u.RequestEventLabAccessSyncs(ctx, eventID)
	}
	return nil
}

// materializeChallenges creates the board challenges of a new attachment from
// its canonical variant (the fixed one when pinned, E5).
func materializeChallenges(ctx context.Context, repo IRepository, link eventExerciseModel.EventExercise, version exerciseModel.ExerciseVersion) error {
	if len(version.Variants) == 0 {
		return model.ErrPlatform.WithMessage("Published exercise has no variants").Err()
	}
	challenges := eventChallengeRepo.New(repo)
	for order, task := range version.Variants[link.CanonicalVariant(len(version.Variants))].Tasks {
		challenge, err := eventChallengeModel.New(link.ID, task, int32(order), time.Now())
		if err != nil {
			return err
		}
		if _, err = challenges.Create(ctx, challenge); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to materialize event challenge").Err()
		}
	}
	return nil
}

// requireInfrastructureForVersion keeps exercises with a lab topology off
// events whose administrator did not allow infrastructure challenges.
func (u *EventUseCase) requireInfrastructureForVersion(ctx context.Context, eventID uuid.UUID, version exerciseModel.ExerciseVersion) error {
	if !exerciseModel.HasInfrastructure(version.Variants) {
		return nil
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.ErrEventNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if !e.InfrastructureAllowed {
		return eventExerciseModel.ErrEventExerciseInfrastructureNotAllowed.Err()
	}
	return nil
}
