package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventExerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventManagerRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

// eventCopyQueries are the statements that copy an event's content into another event.
type eventCopyQueries interface {
	CopyEventSettings(ctx context.Context, arg postgres.CopyEventSettingsParams) error
	CopyEventConfig(ctx context.Context, arg postgres.CopyEventConfigParams) error
	CopyEventPages(ctx context.Context, arg postgres.CopyEventPagesParams) error
	CopyEventChallenge(ctx context.Context, arg postgres.CopyEventChallengeParams) error
	ListEventChallengePrerequisitePairs(
		ctx context.Context,
		eventID uuid.UUID,
	) ([]postgres.EventChallengePrerequisite, error)
	ListEventChallengeGroupsOf(
		ctx context.Context,
		eventID uuid.UUID,
	) ([]postgres.ListEventChallengeGroupsOfRow, error)
	CreateCopiedEventChallengeGroup(
		ctx context.Context,
		arg postgres.CreateCopiedEventChallengeGroupParams,
	) error
	ListEventChallenges(
		ctx context.Context,
		eventExerciseID uuid.UUID,
	) ([]postgres.EventChallenge, error)
	CreateEventChallengePrerequisite(
		ctx context.Context,
		arg postgres.CreateEventChallengePrerequisiteParams,
	) error
}

// brandRefTypes are the media references an event carries: its logo, icon, preview picture and page images.
func brandRefTypes() []string {
	return []string{
		mediaModel.RefTypeEventLogo, mediaModel.RefTypeEventFavicon,
		mediaModel.RefTypeEventPreviewPicture, mediaModel.RefTypeEventContentImage,
	}
}

// copyEventContent fills the new event with the content of another one, inside the creation transaction: settings,
// theme and landing, pages, the schedule (shifted by the difference of the availability starts), the managers and the
// task sets with their groups, order, points, hint prices and prerequisites. The copied tasks are not published; no
// participant, team, invitation, result, stand, reservation or analytics comes along. The caller copies the media
// references after the commit.
func (u *EventUseCase) copyEventContent(
	ctx context.Context,
	repo IRepository,
	source, created eventModel.Event,
	by uuid.UUID,
	now time.Time,
) ([]exerciseModel.ExerciseVersion, error) {
	if err := repo.CopyEventSettings(
		ctx,
		postgres.CopyEventSettingsParams{NewEventID: created.ID, SourceEventID: source.ID},
	); err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to copy event settings").
			Err()
	}
	if err := repo.CopyEventConfig(
		ctx,
		postgres.CopyEventConfigParams{NewEventID: created.ID, SourceEventID: source.ID},
	); err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to copy event config").
			Err()
	}
	if err := repo.CopyEventPages(
		ctx,
		postgres.CopyEventPagesParams{NewEventID: created.ID, SourceEventID: source.ID, Now: now},
	); err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to copy event pages").Err()
	}
	if err := copyEventSchedule(ctx, repo, source, created, by, now); err != nil {
		return nil, err
	}
	if err := copyEventManagers(ctx, repo, source.ID, created.ID, by, now); err != nil {
		return nil, err
	}
	return u.copyTaskSets(ctx, repo, source.ID, created.ID, by, now)
}

// copyEventSchedule gives the copy the schedule of the source moved by the distance between the two availability
// starts, so it keeps its place relative to the availability window. A shifted schedule the new window cannot hold
// is left out: the organizer sets the dates.
func copyEventSchedule(
	ctx context.Context,
	repo IRepository,
	source, created eventModel.Event,
	by uuid.UUID,
	now time.Time,
) error {
	lifecycle, ok := shiftedLifecycle(source, created)
	if !ok {
		return nil
	}
	expected := created.UpdatedAt
	created.UpdateLifecycle(lifecycle, by, now)
	affected, err := eventRepo.New(repo).UpdateLifecycle(ctx, created, expected)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to copy event schedule").Err()
	}
	if affected == 0 {
		return eventModel.ErrEventModified.Err()
	}
	return nil
}

// shiftedLifecycle is the source's schedule moved by the distance between the two availability starts; false when
// the source has none or the new window cannot hold the moved one.
func shiftedLifecycle(source, created eventModel.Event) (eventModel.Lifecycle, bool) {
	if !source.Lifecycle.Configured {
		return eventModel.Lifecycle{}, false
	}
	shift := created.AvailableFrom.Sub(source.AvailableFrom)
	move := func(t *time.Time) *time.Time {
		if t == nil {
			return nil
		}
		moved := t.Add(shift)
		return &moved
	}
	start, publish := source.Lifecycle.StartAt.Add(shift), source.Lifecycle.PublishAt.Add(shift)
	lifecycle, err := eventModel.NewLifecycle(
		source.Lifecycle.JoinPolicy, publish, start,
		move(source.Lifecycle.FinishAt), move(source.Lifecycle.WithdrawAt), nil,
	)
	if err != nil || publish.Before(created.AvailableFrom) {
		return eventModel.Lifecycle{}, false
	}
	if !created.ArchiveAt.IsZero() && (!start.Before(created.ArchiveAt) ||
		(lifecycle.FinishAt != nil && lifecycle.FinishAt.After(created.ArchiveAt)) ||
		(lifecycle.WithdrawAt != nil && lifecycle.WithdrawAt.After(created.ArchiveAt))) {
		return eventModel.Lifecycle{}, false
	}
	return lifecycle, true
}

// copyEventManagers makes the source's managers the copy's too; the creator stays its owner.
func copyEventManagers(
	ctx context.Context,
	repo IRepository,
	sourceID, newID, by uuid.UUID,
	now time.Time,
) error {
	managers := eventManagerRepo.New(repo)
	list, err := managers.List(ctx, sourceID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list event managers").Err()
	}
	for _, manager := range list {
		if manager.UserID == by {
			continue
		}
		copied, newErr := eventManagerModel.New(newID, manager.UserID, manager.Role, now)
		if newErr != nil {
			return newErr
		}
		if _, err = managers.Upsert(ctx, copied); err != nil {
			return model.ErrPlatform.WithError(err).
				WithMessage("Failed to copy event manager").
				Err()
		}
	}
	return nil
}

// copyTaskSets copies the active sets of the source: the same catalog version, or, for a set the source event owns,
// the event's own exercise copied for the new event.
func (u *EventUseCase) copyTaskSets(
	ctx context.Context,
	repo IRepository,
	sourceID, newID, by uuid.UUID,
	now time.Time,
) ([]exerciseModel.ExerciseVersion, error) {
	groups, err := copyChallengeGroups(ctx, repo, sourceID, newID, now)
	if err != nil {
		return nil, err
	}
	var ownVersions []exerciseModel.ExerciseVersion
	sets, err := eventExerciseRepo.New(repo).List(ctx, sourceID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list event exercises").
			Err()
	}
	copied := map[uuid.UUID]uuid.UUID{} // source challenge -> copied challenge
	for _, set := range sets {
		if set.Status != eventExerciseModel.StatusActive {
			continue
		}
		own, copyErr := u.copySet(ctx, repo, set, newID, by, groups, copied, now)
		if copyErr != nil {
			return nil, copyErr
		}
		if own != nil {
			ownVersions = append(ownVersions, *own)
		}
	}
	pairs, err := repo.ListEventChallengePrerequisitePairs(ctx, sourceID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list prerequisites").
			Err()
	}
	for _, pair := range pairs {
		challenge, hasChallenge := copied[pair.ChallengeID]
		prerequisite, hasPrerequisite := copied[pair.PrerequisiteChallengeID]
		if !hasChallenge || !hasPrerequisite {
			continue
		}
		if err = repo.CreateEventChallengePrerequisite(
			ctx,
			postgres.CreateEventChallengePrerequisiteParams{
				ChallengeID:             challenge,
				PrerequisiteChallengeID: prerequisite,
			},
		); err != nil {
			return nil, model.ErrPlatform.WithError(err).
				WithMessage("Failed to copy prerequisite").
				Err()
		}
	}
	return ownVersions, nil
}

// copySet copies one active set of the source: the attachment (the same version, or a new event-owned exercise
// for an exercise the source event owns, returned) and its board challenges.
func (u *EventUseCase) copySet(ctx context.Context, repo IRepository, set eventExerciseModel.EventExercise, newID, by uuid.UUID, groups, copied map[uuid.UUID]uuid.UUID, now time.Time) (*exerciseModel.ExerciseVersion, error) {
	exerciseID, versionID, own, err := u.copiedExercise(ctx, repo, set, newID, by, now)
	if err != nil {
		return nil, err
	}
	link, err := eventExerciseModel.New(newID, exerciseID, versionID, set.VariantMode, set.FixedVariantIndex, now, by)
	if err != nil {
		return nil, err
	}
	stored, err := eventExerciseRepo.New(repo).Create(ctx, link)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to copy event exercise").Err()
	}
	if err = copyChallenges(ctx, repo, set.ID, stored.ID, groups, copied, now); err != nil {
		return nil, err
	}
	return own, nil
}

// copyChallenges copies the board challenges of one set into the copied set, unpublished and in the copied groups;
// copied maps every source challenge to its copy (for the prerequisites).
func copyChallenges(ctx context.Context, repo IRepository, sourceSet, newSet uuid.UUID, groups, copied map[uuid.UUID]uuid.UUID, now time.Time) error {
	challenges, err := repo.ListEventChallenges(ctx, sourceSet)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	for _, challenge := range challenges {
		newChallenge := uuid.Must(uuid.NewV7())
		group := uuid.NullUUID{}
		if challenge.GroupID.Valid {
			mapped, ok := groups[challenge.GroupID.UUID]
			group = uuid.NullUUID{UUID: mapped, Valid: ok}
		}
		if err = repo.CopyEventChallenge(ctx, postgres.CopyEventChallengeParams{
			NewID: newChallenge, NewEventExerciseID: newSet, Now: now, NewGroupID: group, SourceID: challenge.ID,
		}); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to copy event challenge").Err()
		}
		copied[challenge.ID] = newChallenge
	}
	return nil
}

func copyChallengeGroups(
	ctx context.Context,
	repo IRepository,
	sourceID, newID uuid.UUID,
	now time.Time,
) (map[uuid.UUID]uuid.UUID, error) {
	rows, err := repo.ListEventChallengeGroupsOf(ctx, sourceID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list challenge groups").
			Err()
	}
	mapped := make(map[uuid.UUID]uuid.UUID, len(rows))
	for _, row := range rows {
		id := uuid.Must(uuid.NewV7())
		if err = repo.CreateCopiedEventChallengeGroup(
			ctx,
			postgres.CreateCopiedEventChallengeGroupParams{
				ID: id, EventID: newID, Name: row.Name, OrderIndex: row.OrderIndex, Now: now,
			},
		); err != nil {
			return nil, model.ErrPlatform.WithError(err).
				WithMessage("Failed to copy challenge group").
				Err()
		}
		mapped[row.ID] = id
	}
	return mapped, nil
}

// copiedExercise returns the exercise and version the copied set pins: the same ones for a catalog exercise, a new
// event-owned copy (same texts and content, new ids) for an exercise the source event owns.
func (u *EventUseCase) copiedExercise(
	ctx context.Context,
	repo IRepository,
	set eventExerciseModel.EventExercise,
	newEventID, by uuid.UUID,
	now time.Time,
) (uuid.UUID, uuid.UUID, *exerciseModel.ExerciseVersion, error) {
	exercises := exerciseRepo.New(repo)
	source, err := exercises.GetByID(ctx, set.ExerciseID)
	if err != nil {
		return uuid.Nil, uuid.Nil, nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to get exercise").
			Err()
	}
	if !source.IsEventScoped() {
		return set.ExerciseID, set.ExerciseVersionID, nil, nil
	}
	pinned, err := exercises.GetVersion(ctx, set.ExerciseVersionID)
	if err != nil {
		return uuid.Nil, uuid.Nil, nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to get pinned version").
			Err()
	}
	own, err := exerciseModel.NewEventExercise(
		source.Name,
		source.Description,
		source.Tags,
		newEventID,
		by,
		now,
	)
	if err != nil {
		return uuid.Nil, uuid.Nil, nil, err
	}
	own.ForkedFromExerciseID, own.ForkedFromVersionID = source.ForkedFromExerciseID, source.ForkedFromVersionID
	created, err := exercises.Create(ctx, own)
	if err != nil {
		return uuid.Nil, uuid.Nil, nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to copy event exercise").
			Err()
	}
	version := exerciseModel.ExerciseVersion{
		ID: uuid.Must(
			uuid.NewV7(),
		),
		ExerciseID:  created.ID,
		Status:      exerciseModel.VersionStatusPublished,
		Name:        created.Name,
		Description: created.Description,
		Tags:        created.Tags,
		AdminNote:   pinned.AdminNote,
		Variants:    pinned.Variants,
		CreatedAt:   now,
		CreatedBy:   uuid.NullUUID{UUID: by, Valid: by != uuid.Nil},
		PublishedAt: &now,
	}
	if _, err = exercises.InsertImportedVersion(ctx, version); err != nil {
		return uuid.Nil, uuid.Nil, nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to copy exercise version").
			Err()
	}
	if err = exercises.SetImportedPointers(
		ctx,
		created.ID,
		uuid.NullUUID{},
		uuid.NullUUID{UUID: version.ID, Valid: true},
	); err != nil {
		return uuid.Nil, uuid.Nil, nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to copy exercise version").
			Err()
	}
	return created.ID, version.ID, &version, nil
}

// copyEventMedia gives the copy the same logo, icon, preview picture and page images as the source: the files are
// shared, each event holding its own reference. A failure is logged and never undoes the copy.
func (u *EventUseCase) copyEventMedia(
	ctx context.Context,
	sourceID, newID uuid.UUID,
	versions []exerciseModel.ExerciseVersion,
) {
	if u.brandMedia == nil {
		return
	}
	for _, refType := range brandRefTypes() {
		ids, err := u.brandMedia.GetReferences(ctx, refType, sourceID)
		if err != nil || len(ids) == 0 {
			if err != nil {
				log.Warn().
					Err(err).
					Str("ref_type", refType).
					Msg("event copy: media references were not read")
			}
			continue
		}
		if err = u.brandMedia.ReplaceReferences(ctx, refType, newID, ids); err != nil {
			log.Warn().
				Err(err).
				Str("ref_type", refType).
				Msg("event copy: media references were not copied")
		}
	}
	for _, version := range versions {
		if err := u.brandMedia.ReplaceReferences(
			ctx,
			mediaModel.RefTypeExerciseVersion,
			version.ID,
			exerciseModel.CollectFileIDs(version.Variants),
		); err != nil {
			log.Warn().
				Err(err).
				Stringer("version_id", version.ID).
				Msg("event copy: task files were not referenced")
		}
	}
}
