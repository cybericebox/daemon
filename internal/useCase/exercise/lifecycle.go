package exercise

import (
	"context"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

type SaveDraftInput struct {
	AdminNote string
	Variants  []exerciseModel.Variant
	SavedBy   uuid.UUID
}

// SaveDraft stores the working copy as-is: it may be incomplete (autosave while
// typing). Only JSON shape is enforced (HTTP binding); business validation runs
// in PublishDraft. Kept secrets are merged and fresh ones encrypted, then the
// draft slot is upserted in one CTE. Route gate: exercises.write.
func (u *ExerciseUseCase) SaveDraft(ctx context.Context, exerciseID uuid.UUID, in SaveDraftInput) (VersionView, error) {
	e, err := u.loadEditable(ctx, exerciseID)
	if err != nil {
		return VersionView{}, err
	}
	version := exerciseModel.ExerciseVersion{
		AdminNote: in.AdminNote,
		Variants:  in.Variants,
	}
	// Assign ids to any new variant/task/device BEFORE the secret merge and
	// encryption: those helpers key by variant id, and a variant new to this
	// save must have its final id or it collides with every other id-less
	// variant. Tasks at the same position across variants receive one
	// canonical ID, which is the stable event-board identity.
	normalizeContentIDs(version.Variants)
	if err = u.requireInfrastructureAllowed(ctx, e, version.Variants); err != nil {
		return VersionView{}, err
	}

	// Snapshot which secret fields the caller actually submitted BEFORE the
	// merge fills blanks from the stored draft — merged values are ciphertext
	// and must never be re-encrypted.
	submitted := submittedSecretKeys(version.Variants)
	mergeSource, err := u.secretMergeSource(ctx, e)
	if err != nil {
		return VersionView{}, err
	}
	if mergeSource != nil {
		mergeKeptSecrets(version.Variants, mergeSource.Variants)
	}
	if err := u.encryptSecrets(version.Variants, submitted); err != nil {
		return VersionView{}, err
	}

	by := uuid.NullUUID{UUID: in.SavedBy, Valid: in.SavedBy != uuid.Nil}
	saved, err := u.exercises.UpsertDraft(ctx, exerciseID, uuid.Must(uuid.NewV7()), version, time.Now(), by)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return VersionView{}, exerciseModel.ErrExerciseNotFound.Err()
		}
		return VersionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save draft").Err()
	}
	if err = u.media.ReplaceReferences(ctx, mediaModel.RefTypeExerciseVersion, saved.ID, exerciseModel.CollectFileIDs(saved.Variants)); err != nil {
		return VersionView{}, err
	}
	return u.versionView(saved), nil
}

// secretMergeSource resolves which stored version's secrets a blank "keep"
// field in an incoming SaveDraft merges from: the draft when one exists,
// otherwise the published version (the lazily materialized working copy) —
// else an admin who PUTs back the secret-masked published content unchanged
// would silently store every secret as empty. nil, nil = nothing to merge.
func (u *ExerciseUseCase) secretMergeSource(ctx context.Context, e exerciseModel.Exercise) (*exerciseModel.ExerciseVersion, error) {
	if draft, err := u.exercises.GetDraft(ctx, e.ID); err == nil {
		return &draft, nil
	} else if !repositoryTools.IsObjectNotFoundError(err) {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get current draft").Err()
	}
	if !e.PublishedVersionID.Valid {
		return nil, nil
	}
	published, err := u.exercises.GetVersion(ctx, e.PublishedVersionID.UUID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			// Stale pointer / concurrent unpublish race — nothing to merge from.
			return nil, nil
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get published version").Err()
	}
	return &published, nil
}

// PublishDraft promotes the draft after the full publish validation via the
// atomic Publish CTE (Task 6). Route gate: exercises.publish.
func (u *ExerciseUseCase) PublishDraft(ctx context.Context, exerciseID uuid.UUID) (VersionView, error) {
	e, err := u.loadEditable(ctx, exerciseID)
	if err != nil {
		return VersionView{}, err
	}
	draft, err := u.exercises.GetDraft(ctx, exerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return VersionView{}, exerciseModel.ErrNoDraft.Err()
		}
		return VersionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get draft").Err()
	}
	if err = draft.ValidateForPublish(); err != nil {
		return VersionView{}, err
	}
	if err = u.requireInfrastructureAllowed(ctx, e, draft.Variants); err != nil {
		return VersionView{}, err
	}
	if err = u.requireVariantsFit(draft.Variants); err != nil {
		return VersionView{}, err
	}
	published, err := u.exercises.Publish(ctx, exerciseID, time.Now())
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			// Draft vanished between read and promote — concurrent discard.
			return VersionView{}, exerciseModel.ErrNoDraft.WithMessage("Draft was removed concurrently").Err()
		}
		return VersionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to publish draft").Err()
	}
	return u.versionView(published), nil
}

// DiscardDraft deletes the draft slot; the exercise's draft pointer clears via
// FK (Discard CTE, Task 6). Route gate: exercises.publish.
func (u *ExerciseUseCase) DiscardDraft(ctx context.Context, exerciseID uuid.UUID) error {
	if _, err := u.loadEditable(ctx, exerciseID); err != nil {
		return err
	}
	draftID, err := u.exercises.Discard(ctx, exerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return exerciseModel.ErrNoDraft.WithMessage("Exercise has no draft to discard").Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to discard draft").Err()
	}
	return u.media.RemoveReferences(ctx, mediaModel.RefTypeExerciseVersion, draftID)
}

// RollbackToVersion clones a historical version into a fresh draft via the
// CreateDraftFrom CTE (Task 6). Route gate: exercises.publish.
func (u *ExerciseUseCase) RollbackToVersion(ctx context.Context, exerciseID, versionID, by uuid.UUID) (VersionView, error) {
	if _, err := u.loadEditable(ctx, exerciseID); err != nil {
		return VersionView{}, err
	}
	byNull := uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}
	draft, err := u.exercises.CreateDraftFrom(ctx, exerciseID, versionID, uuid.Must(uuid.NewV7()), time.Now(), byNull)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			// Zero rows: source missing OR draft already exists — re-read to tell.
			// The re-read must also check ownership: a versionID belonging to a
			// DIFFERENT exercise must read as "not found", never leak into
			// ErrDraftAlreadyExists (anti-IDOR, same rule as GetVersion above).
			if v, vErr := u.exercises.GetVersion(ctx, versionID); vErr != nil || v.ExerciseID != exerciseID {
				return VersionView{}, exerciseModel.ErrExerciseVersionNotFound.Err()
			}
			return VersionView{}, exerciseModel.ErrDraftAlreadyExists.Err()
		}
		return VersionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to rollback to version").Err()
	}
	if err = u.media.ReplaceReferences(ctx, mediaModel.RefTypeExerciseVersion, draft.ID, exerciseModel.CollectFileIDs(draft.Variants)); err != nil {
		return VersionView{}, err
	}
	return u.versionView(draft), nil
}

// CreateCheckpoint records one explicit, immutable snapshot of the working
// copy (draft row, else the published content), optionally labelled. The
// label never touches AdminNote (working-copy content). Frequent autosaves
// never call this method. Route gate: exercises.write.
func (u *ExerciseUseCase) CreateCheckpoint(ctx context.Context, exerciseID, by uuid.UUID, label string) (VersionView, error) {
	if _, err := u.loadEditable(ctx, exerciseID); err != nil {
		return VersionView{}, err
	}
	checkpoint, err := u.exercises.CreateCheckpoint(ctx, exerciseID, uuid.Must(uuid.NewV7()), strings.TrimSpace(label), time.Now(), uuid.NullUUID{
		UUID: by, Valid: by != uuid.Nil,
	})
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return VersionView{}, exerciseModel.ErrNoDraft.WithMessage("Exercise has no content to snapshot").Err()
		}
		return VersionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create exercise checkpoint").Err()
	}
	if err = u.media.ReplaceReferences(ctx, mediaModel.RefTypeExerciseVersion, checkpoint.ID, exerciseModel.CollectFileIDs(checkpoint.Variants)); err != nil {
		return VersionView{}, err
	}
	return u.versionView(checkpoint), nil
}

// RestoreToVersion preserves an existing draft as a checkpoint atomically with
// replacing its content. With no draft it creates one from the source version.
// Stored JSON is copied server-side, so encrypted secret values are not
// accidentally encrypted again or exposed to the browser.
func (u *ExerciseUseCase) RestoreToVersion(ctx context.Context, exerciseID, versionID, by uuid.UUID) (VersionView, error) {
	if _, err := u.loadEditable(ctx, exerciseID); err != nil {
		return VersionView{}, err
	}
	source, err := u.exercises.GetVersion(ctx, versionID)
	if err != nil || source.ExerciseID != exerciseID || source.Status.IsDraft() {
		if err != nil && !repositoryTools.IsObjectNotFoundError(err) {
			return VersionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get restore source").Err()
		}
		return VersionView{}, exerciseModel.ErrExerciseVersionNotFound.Err()
	}
	byNull := uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}
	now := time.Now()
	_, err = u.exercises.GetDraft(ctx, exerciseID)
	if err != nil && !repositoryTools.IsObjectNotFoundError(err) {
		return VersionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get current draft").Err()
	}
	if repositoryTools.IsObjectNotFoundError(err) {
		restored, createErr := u.exercises.CreateDraftFrom(ctx, exerciseID, versionID, uuid.Must(uuid.NewV7()), now, byNull)
		if createErr != nil {
			return VersionView{}, model.ErrPlatform.WithError(createErr).WithMessage("Failed to restore exercise version").Err()
		}
		if err = u.media.ReplaceReferences(ctx, mediaModel.RefTypeExerciseVersion, restored.ID, exerciseModel.CollectFileIDs(restored.Variants)); err != nil {
			return VersionView{}, err
		}
		return u.versionView(restored), nil
	}
	checkpointID := uuid.Must(uuid.NewV7())
	restored, err := u.exercises.RestoreVersionPreservingDraft(ctx, exerciseID, versionID, checkpointID, now, byNull)
	if err != nil {
		return VersionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to restore exercise version").Err()
	}
	// Read the actual preserved snapshot, not the draft pre-read above: another
	// autosave may have completed between that pre-read and the atomic SQL copy.
	checkpoint, err := u.exercises.GetVersion(ctx, checkpointID)
	if err != nil {
		return VersionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load preserved checkpoint").Err()
	}
	if err = u.media.ReplaceReferences(ctx, mediaModel.RefTypeExerciseVersion, checkpointID, exerciseModel.CollectFileIDs(checkpoint.Variants)); err != nil {
		return VersionView{}, err
	}
	if err = u.media.ReplaceReferences(ctx, mediaModel.RefTypeExerciseVersion, restored.ID, exerciseModel.CollectFileIDs(restored.Variants)); err != nil {
		return VersionView{}, err
	}
	return u.versionView(restored), nil
}

// GetVersion returns one version's content with secrets masked. Route gate:
// exercises.read.
func (u *ExerciseUseCase) GetVersion(ctx context.Context, exerciseID, versionID uuid.UUID) (VersionView, error) {
	v, err := u.exercises.GetVersion(ctx, versionID)
	if err != nil || v.ExerciseID != exerciseID {
		if err != nil && !repositoryTools.IsObjectNotFoundError(err) {
			return VersionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get version").Err()
		}
		return VersionView{}, exerciseModel.ErrExerciseVersionNotFound.Err()
	}
	return u.versionView(v), nil
}

// GetWorkingCopy returns the editable working copy. It always exists while
// the exercise does, materialized lazily: the draft row when one exists;
// otherwise the published version's content (its own ID, Status "published")
// — the first SaveDraft turns it into a draft row; otherwise an empty copy
// (uuid.Nil ID, Status "draft", no variants). Route gate: exercises.read.
func (u *ExerciseUseCase) GetWorkingCopy(ctx context.Context, exerciseID uuid.UUID) (VersionView, error) {
	e, err := u.exercises.GetByID(ctx, exerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return VersionView{}, exerciseModel.ErrExerciseNotFound.Err()
		}
		return VersionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}
	draft, err := u.exercises.GetDraft(ctx, exerciseID)
	if err == nil {
		return u.versionView(draft), nil
	}
	if !repositoryTools.IsObjectNotFoundError(err) {
		return VersionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get draft").Err()
	}
	if e.PublishedVersionID.Valid {
		published, pubErr := u.exercises.GetVersion(ctx, e.PublishedVersionID.UUID)
		if pubErr == nil {
			return u.versionView(published), nil
		}
		if !repositoryTools.IsObjectNotFoundError(pubErr) {
			return VersionView{}, model.ErrPlatform.WithError(pubErr).WithMessage("Failed to get published version").Err()
		}
		// Stale pointer (concurrent change): fall through to the empty copy.
	}
	return VersionView{
		ExerciseID: e.ID,
		Status:     string(exerciseModel.VersionStatusDraft),
		Variants:   []exerciseModel.Variant{},
		CreatedAt:  e.CreatedAt,
		CreatedBy:  uuidPtr(e.CreatedBy),
	}, nil
}

// ListVersions returns the exercise's version history (no content payload).
// Route gate: exercises.read.
func (u *ExerciseUseCase) ListVersions(ctx context.Context, exerciseID uuid.UUID) ([]VersionListItem, error) {
	versions, err := u.exercises.ListVersions(ctx, exerciseID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list versions").Err()
	}
	out := make([]VersionListItem, 0, len(versions))
	for _, v := range versions {
		out = append(out, VersionListItem{
			ID: v.ID, Status: string(v.Status), AdminNote: v.AdminNote, Label: v.Label,
			VariantCount: len(v.Variants), CreatedAt: v.CreatedAt,
			CreatedBy: uuidPtr(v.CreatedBy), PublishedAt: v.PublishedAt,
		})
	}
	return out, nil
}

// normalizeContentIDs assigns UUIDv7 ids to variants/tasks/devices that
// arrived without one (new items from the editor). Connections address
// devices by id, and the secret merge/encrypt passes key by variant id, so
// every id must exist before either runs — this MUST run before
// submittedSecretKeys/mergeKeptSecrets/encryptSecrets in SaveDraft.
func normalizeContentIDs(variants []exerciseModel.Variant) {
	for vi := range variants {
		if variants[vi].ID == uuid.Nil {
			variants[vi].ID = uuid.Must(uuid.NewV7())
		}
		for ti := range variants[vi].Tasks {
			if vi == 0 && variants[vi].Tasks[ti].ID == uuid.Nil {
				variants[vi].Tasks[ti].ID = uuid.Must(uuid.NewV7())
			}
			if vi > 0 && variants[vi].Tasks[ti].ID == uuid.Nil && ti < len(variants[0].Tasks) {
				variants[vi].Tasks[ti].ID = variants[0].Tasks[ti].ID
			}
			for hi := range variants[vi].Tasks[ti].Hints {
				hint := &variants[vi].Tasks[ti].Hints[hi]
				if hint.ID != uuid.Nil {
					continue
				}
				if vi > 0 && ti < len(variants[0].Tasks) && hi < len(variants[0].Tasks[ti].Hints) {
					hint.ID = variants[0].Tasks[ti].Hints[hi].ID
				} else {
					hint.ID = uuid.Must(uuid.NewV7())
				}
			}
			for pi := range variants[vi].Tasks[ti].Placeholders {
				placeholder := &variants[vi].Tasks[ti].Placeholders[pi]
				if placeholder.Key == "" {
					placeholder.Key = "ph_" + strings.ReplaceAll(uuid.Must(uuid.NewV7()).String(), "-", "")
				}
			}
		}
		for di := range variants[vi].Topology.Devices {
			if variants[vi].Topology.Devices[di].ID == uuid.Nil {
				variants[vi].Topology.Devices[di].ID = uuid.Must(uuid.NewV7())
			}
		}
	}
}
