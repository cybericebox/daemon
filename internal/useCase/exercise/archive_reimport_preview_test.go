package exercise_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/internal/testhelpers"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/jackc/pgx/v5"
	"io"
	"testing"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestReimportPreviewReturnsDuplicateSuggestionsAndBindsExactSourceAndTarget(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	media, sources := newFakeMedia(), newFakeSources()
	id, draftID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	doc := []byte(`{"schema":"cib-exercise/1","exercise":{"id":"` + id.String() + `","name":"Portable task"},"versions":[{"status":"draft","variants":[]}]}`)
	archive, err := exercise.BuildArchiveV1(map[string][]byte{"a/exercise.json": doc, "b/exercise.json": doc})
	require.NoError(t, err)
	sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
	q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(postgres.Exercise{ID: id, Name: "Portable task", DraftVersionID: uuid.NullUUID{UUID: draftID, Valid: true}}, nil).AnyTimes()
	draft := postgres.ExerciseVersion{ID: draftID, ExerciseID: id, Status: "draft", Name: "Portable task", Tags: []string{}, Variants: []byte(`[]`)}
	q.EXPECT().GetDraftVersion(gomock.Any(), id).DoAndReturn(func(context.Context, uuid.UUID) (postgres.ExerciseVersion, error) { return draft, nil }).AnyTimes()
	uc := newUC(q, media)
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	actor := exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}
	in := exercise.PreviewArchiveInput{UploadID: archiveUpID}
	preview, err := uc.PreviewArchiveImport(context.Background(), actor, in)
	require.NoError(t, err)
	require.Len(t, preview.Items, 2, "auto-match collisions must not hide the source rows before selectors can render")
	require.Equal(t, "a", preview.Items[0].ItemKey)
	require.Equal(t, "b", preview.Items[1].ItemKey)
	require.Equal(t, id, preview.Items[0].Target.ExerciseID)
	require.Equal(t, preview.Items[0].Target.ExerciseID, preview.Items[1].Target.ExerciseID)
	require.NotEmpty(t, preview.Items[0].ConfirmationRevision)
	require.NotEqual(t, preview.Items[0].ConfirmationRevision, preview.Items[1].ConfirmationRevision)
	repeated, err := uc.PreviewArchiveImport(context.Background(), actor, in)
	require.NoError(t, err)
	require.Equal(t, preview.Items[0].ConfirmationRevision, repeated.Items[0].ConfirmationRevision, "generated parse IDs must not randomize confirmation")
	newInput := in
	newInput.Targets = []exercise.ImportTarget{{ItemKey: "a", Mode: "new"}, {ItemKey: "b", Mode: "new"}}
	newPreview, err := uc.PreviewArchiveImport(context.Background(), actor, newInput)
	require.NoError(t, err)
	require.True(t, newPreview.Items[0].Summary.MetadataChanged, "an explicit NEW choice compares against an empty destination, never its auto-match suggestion")
	require.Nil(t, newPreview.Items[0].TargetExercise)
	draft.AdminNote = "unrelated suggested target changed"
	repeatedNew, err := uc.PreviewArchiveImport(context.Background(), actor, newInput)
	require.NoError(t, err)
	require.Equal(t, newPreview.Items[0].ConfirmationRevision, repeatedNew.Items[0].ConfirmationRevision, "NEW confirmation must not depend on an unselected suggested task")
	draft.AdminNote = ""

	otherSource := uuid.Must(uuid.NewV7())
	sources.stage(otherSource, importer, archive, mediaModel.UploadPurposeExerciseArchive)
	otherPreview, err := uc.PreviewArchiveImport(context.Background(), actor, exercise.PreviewArchiveInput{UploadID: otherSource})
	require.NoError(t, err)
	require.NotEqual(t, preview.Items[0].ConfirmationRevision, otherPreview.Items[0].ConfirmationRevision, "archive A cannot authorize an update from archive B")
	draft.AdminNote = "new acknowledged draft content"
	changed, err := uc.PreviewArchiveImport(context.Background(), actor, in)
	require.NoError(t, err)
	require.NotEqual(t, preview.Items[0].ConfirmationRevision, changed.Items[0].ConfirmationRevision)
	in.Targets = []exercise.ImportTarget{{ItemKey: "a", Mode: "update", ExerciseID: id}, {ItemKey: "b", Mode: "update", ExerciseID: id}}
	_, err = uc.PreviewArchiveImport(context.Background(), actor, in)
	require.ErrorIs(t, err, exerciseModel.ErrImportTargetDuplicate.Err())
	require.Equal(t, "b", publicContext(t, err)["itemKey"])
	require.Equal(t, "target", publicContext(t, err)["field"])
	require.Empty(t, sources.dropped)
	require.Empty(t, media.uploads)
	require.Empty(t, media.replaced)
	require.Empty(t, media.removed)
}

func TestReimportPreviewRejectsUnownedAndIncompleteSourcesBeforeAnyRepositoryAccess(t *testing.T) {
	for _, kind := range []string{"foreign", "wrong purpose", "incomplete"} {
		t.Run(kind, func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			sources := newFakeSources()
			owner, purpose := importer, mediaModel.UploadPurposeExerciseArchive
			if kind == "foreign" {
				owner = otherUser
			}
			if kind == "wrong purpose" {
				purpose = mediaModel.UploadPurposeImageBlob
			}
			sources.stage(archiveUpID, owner, []byte("not parsed"), purpose)
			if kind == "incomplete" {
				upload := sources.uploads[archiveUpID]
				upload.ChunksReceived = 0
				sources.uploads[archiveUpID] = upload
			}
			uc := newUC(q, newFakeMedia())
			uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
			_, err := uc.PreviewArchiveImport(context.Background(), exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}, exercise.PreviewArchiveInput{UploadID: archiveUpID})
			if kind == "foreign" {
				require.ErrorIs(t, err, mediaModel.ErrUploadNotFound.Err())
			} else {
				require.ErrorIs(t, err, exerciseModel.ErrArchiveUploadNotReady.Err())
			}
		})
	}
}

func TestReimportPreviewSummaryDoesNotExposeFlagsOrSecrets(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	sources := newFakeSources()
	archive := mustExerciseArchive(t, `{"name":"Portable task"}`, `[{"status":"draft","variants":[{"tasks":[{"name":"Task name","difficulty":"easy","flag":["ICE{never_return_this}"]}]}]}]`)
	sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
	uc := newUC(q, newFakeMedia())
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	view, err := uc.PreviewArchiveImport(context.Background(), exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}, exercise.PreviewArchiveInput{UploadID: archiveUpID, Targets: []exercise.ImportTarget{{ItemKey: ".", Mode: "new"}}})
	require.NoError(t, err)
	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "ICE{")
	require.NotContains(t, string(encoded), "variants\":")
	require.Equal(t, 1, view.Items[0].Summary.Variants.Added)
}

func TestReimportPreviewFixedTargetAndInvalidExplicitPlans(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	sources := newFakeSources()
	id, sourceID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(postgres.Exercise{ID: id, Name: "Existing target"}, nil).AnyTimes()
	archive := mustExerciseArchive(t, `{"id":"`+sourceID.String()+`","name":"Portable task"}`, `[{"status":"draft","variants":[]}]`)
	sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
	uc := newUC(q, newFakeMedia())
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	actor := exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}
	in := exercise.PreviewArchiveInput{UploadID: archiveUpID, FixedExerciseID: id}
	view, err := uc.PreviewArchiveImport(context.Background(), actor, in)
	require.NoError(t, err)
	require.True(t, view.Items[0].SourceIDMismatch)
	require.Equal(t, id, view.Items[0].Target.ExerciseID)
	for _, choices := range [][]exercise.ImportTarget{
		{}, {{ItemKey: "unknown", Mode: "new"}}, {{ItemKey: ".", Mode: "update"}}, {{ItemKey: ".", Mode: "invalid"}}, {{ItemKey: ".", Mode: "new", ExerciseID: id}}, {{ItemKey: ".", Mode: "new"}, {ItemKey: ".", Mode: "new"}},
	} {
		_, err = uc.PreviewArchiveImport(context.Background(), actor, exercise.PreviewArchiveInput{UploadID: archiveUpID, Targets: choices})
		require.ErrorIs(t, err, exerciseModel.ErrImportSelectionInvalid.Err())
		require.Equal(t, "target", publicContext(t, err)["field"])
	}
	in.Targets = []exercise.ImportTarget{{ItemKey: ".", Mode: "new"}}
	_, err = uc.PreviewArchiveImport(context.Background(), actor, in)
	require.ErrorIs(t, err, exerciseModel.ErrImportFixedTargetInvalid.Err())
}

func TestReimportPreviewExplicitTargetDoesNotFallBackAndSuggestionsDoNotLeakNames(t *testing.T) {
	for _, kind := range []string{"missing", "forbidden", "archived"} {
		t.Run(kind, func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			sources := newFakeSources()
			id := uuid.Must(uuid.NewV7())
			event := uuid.Must(uuid.NewV7())
			entity := postgres.Exercise{ID: id, Name: "Denied private name"}
			actor := exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}
			var expected error
			switch kind {
			case "missing":
				q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(postgres.Exercise{}, errNoRows()).AnyTimes()
				expected = exerciseModel.ErrExerciseNotFound.Err()
			case "archived":
				entity.ArchivedAt = pgtype.Timestamptz{Time: importJobT0, Valid: true}
				q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(entity, nil).AnyTimes()
				expected = exerciseModel.ErrExerciseArchived.Err()
			case "forbidden":
				actor.Role = rbac.RoleUser
				q.EXPECT().ListUserEventMemberships(gomock.Any(), importer).Return([]postgres.ListUserEventMembershipsRow{{EventID: event, Role: 1}}, nil).AnyTimes()
				q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(entity, nil).AnyTimes()
				expected = exerciseModel.ErrExerciseForbidden.Err()
			}
			archive := mustExerciseArchive(t, `{"id":"`+id.String()+`","name":"Portable task"}`, `[{"status":"draft","variants":[]}]`)
			sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
			uc := newUC(q, newFakeMedia())
			uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
			view, err := uc.PreviewArchiveImport(context.Background(), actor, exercise.PreviewArchiveInput{UploadID: archiveUpID})
			require.NoError(t, err)
			require.Equal(t, "new", view.Items[0].Target.Mode)
			require.Nil(t, view.Items[0].TargetExercise)
			body, _ := json.Marshal(view)
			require.NotContains(t, string(body), "Denied private name")
			_, err = uc.PreviewArchiveImport(context.Background(), actor, exercise.PreviewArchiveInput{UploadID: archiveUpID, Targets: []exercise.ImportTarget{{ItemKey: ".", Mode: "update", ExerciseID: id}}})
			require.ErrorIs(t, err, expected)
			require.Equal(t, ".", publicContext(t, err)["itemKey"])
		})
	}
}

type previewFiles struct {
	*fakeSources
	file   mediaModel.File
	body   []byte
	opened int
}

func (f *previewFiles) GetFile(context.Context, uuid.UUID) (mediaModel.File, error) {
	return f.file, nil
}
func (f *previewFiles) OpenFileAt(context.Context, uuid.UUID) (io.ReaderAt, mediaModel.File, error) {
	f.opened++
	return bytes.NewReader(f.body), f.file, nil
}

func TestReimportPreviewStoredFileChecksOwnerBeforeOpening(t *testing.T) {
	for _, owned := range []bool{false, true} {
		t.Run(fmt.Sprint(owned), func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			id := uuid.Must(uuid.NewV7())
			owner := otherUser
			if owned {
				owner = importer
			}
			archive := mustExerciseArchive(t, `{"name":"Portable task"}`, `[{"status":"draft","variants":[]}]`)
			sources := &previewFiles{fakeSources: newFakeSources(), file: mediaModel.File{ID: id, CreatedBy: uuid.NullUUID{UUID: owner, Valid: true}, SizeBytes: int64(len(archive))}, body: archive}
			uc := newUC(q, newFakeMedia())
			uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
			_, err := uc.PreviewArchiveImport(context.Background(), exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}, exercise.PreviewArchiveInput{FileID: id})
			if owned {
				require.NoError(t, err)
				require.Equal(t, 1, sources.opened)
			} else {
				require.ErrorIs(t, err, mediaModel.ErrFileNotFound.Err())
				require.Zero(t, sources.opened)
			}
		})
	}
}

func TestReimportPreviewUpdateUsesSaveDraftInfrastructureGateAndDefersPublishResources(t *testing.T) {
	ctx := context.Background()
	actor := exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}
	targetID, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	archive, err := exercise.BuildArchiveNoManifest(examplePreset(t, "xlarge", false))
	require.NoError(t, err)
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	sources := newFakeSources()
	sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
	uc := newUC(q, newFakeMedia())
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	target := postgres.Exercise{ID: targetID, Name: "Existing target"}
	q.EXPECT().GetExerciseByID(gomock.Any(), targetID).DoAndReturn(func(context.Context, uuid.UUID) (postgres.Exercise, error) { return target, nil }).AnyTimes()
	_, err = uc.PreviewArchiveImport(ctx, actor, exercise.PreviewArchiveInput{UploadID: archiveUpID, Targets: []exercise.ImportTarget{{ItemKey: ".", Mode: "new"}}})
	require.ErrorIs(t, err, exerciseModel.ErrDevicesNeedElevation.Err(), "NEW retains the full-import published resource gates")
	_, err = uc.PreviewArchiveImport(ctx, actor, exercise.PreviewArchiveInput{UploadID: archiveUpID, Targets: []exercise.ImportTarget{{ItemKey: ".", Mode: "update", ExerciseID: targetID}}})
	require.NoError(t, err, "UPDATE becomes a draft; resource elevation is required only when it is published")
	target.Scope = 1
	target.OwnerEventID = uuid.NullUUID{UUID: eventID, Valid: true}
	q.EXPECT().GetEventInfrastructureAllowed(gomock.Any(), eventID).Return(false, nil)
	_, err = uc.PreviewArchiveImport(ctx, actor, exercise.PreviewArchiveInput{UploadID: archiveUpID, Targets: []exercise.ImportTarget{{ItemKey: ".", Mode: "update", ExerciseID: targetID}}})
	require.ErrorIs(t, err, exerciseModel.ErrExerciseInfrastructureNotAllowed.Err(), "ordinary draft-save infrastructure restrictions must already apply")
}

func TestReimportPreviewWorksInRealReadOnlyPostgresTransaction(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	entity, err := exerciseModel.NewExercise("Existing target", "", nil, uuid.Nil, importJobT0)
	require.NoError(t, err)
	entity, err = repo.Create(ctx, entity)
	require.NoError(t, err)
	_, err = repo.UpsertDraft(ctx, entity.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "acknowledged draft"}, importJobT0, uuid.NullUUID{})
	require.NoError(t, err)
	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	sources := newFakeSources()
	archive := mustExerciseArchive(t, `{"id":"`+entity.ID.String()+`","name":"Portable task"}`, `[{"status":"draft","variants":[]}]`)
	sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
	media := newFakeMedia()
	uc := newUC(postgres.New(tx), media)
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	view, err := uc.PreviewArchiveImport(ctx, exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}, exercise.PreviewArchiveInput{UploadID: archiveUpID})
	require.NoError(t, err)
	require.True(t, view.Items[0].ReplacesDraft)
	require.True(t, view.Items[0].Summary.MetadataChanged)
	page, err := uc.ListImportTargets(ctx, exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}, "", uuid.Nil, 1)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, entity.ID, page.Items[0].ID)
	require.NoError(t, tx.Commit(ctx))
	require.Empty(t, media.replaced)
	require.Empty(t, sources.dropped)
}

type previewImages struct {
	*fakeImages
	inspected int
}

func (f *previewImages) InspectImageArchive(_ context.Context, a *ociregistry.ImageArchive, name string) (imageRegistryUseCase.ArchiveImageInspection, error) {
	f.inspected++
	layers := []imageRegistryUseCase.Descriptor{}
	for _, layer := range a.Layers {
		layers = append(layers, imageRegistryUseCase.Descriptor{MediaType: layer.MediaType, Digest: layer.Digest, Size: layer.Size})
	}
	manifest, err := imageRegistryModel.BuildManifest(imageRegistryUseCase.Descriptor{MediaType: a.Config.MediaType, Digest: a.Config.Digest, Size: a.Config.Size}, layers)
	if err != nil {
		return imageRegistryUseCase.ArchiveImageInspection{}, err
	}
	ref, err := imageRegistryModel.NewPlatformRef(name, manifest.Digest)
	return imageRegistryUseCase.ArchiveImageInspection{Reference: ref.String(), Digest: manifest.Digest, Reused: true}, err
}
func TestReimportPreviewIndexesEveryImageBeforeReadOnlyInspectionAndCountsManifestOnce(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			sources := newFakeSources()
			images := &previewImages{fakeImages: &fakeImages{}}
			second := webImageTar
			if corrupt {
				second = []byte("broken later image")
			}
			archive, err := exercise.BuildArchiveV1(map[string][]byte{
				"exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Images task"},"versions":[{"status":"draft","variants":[{"topology":{"devices":[{"name":"web","type":"container","image":{"file":"images/a.tar"}},{"name":"tool","type":"container","image":{"file":"images/b.tar"}},{"name":"external","type":"container","image":"external:image"}]}}]}]}`),
				"images/a.tar":  webImageTar, "images/b.tar": second,
			})
			require.NoError(t, err)
			sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
			uc := newUC(q, newFakeMedia())
			uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources, Images: images})
			view, err := uc.PreviewArchiveImport(context.Background(), exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}, exercise.PreviewArchiveInput{UploadID: archiveUpID})
			if corrupt {
				require.Error(t, err)
				require.Zero(t, images.inspected, "all embedded bytes must validate before registry inspection")
			} else {
				require.NoError(t, err)
				require.Equal(t, exercise.ImportImageChange{Reused: 1}, view.Items[0].Summary.Images)
				require.Equal(t, 2, images.inspected)
			}
			require.Empty(t, images.pushes)
			require.Empty(t, sources.dropped)
		})
	}
}

func TestReimportPreviewRejectsWholeInvalidArchiveAndWrongPassword(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	sources := newFakeSources()
	uc := newUC(q, newFakeMedia())
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	actor := exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}
	doc := []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Portable task"},"versions":[{"status":"draft","variants":[]}]}`)
	badDoc := []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Invalid item"},"versions":[{"status":"checkpoint","exercise":{"name":"x"},"variants":[]}]}`)
	bundle, err := exercise.BuildArchiveV1(map[string][]byte{"a/exercise.json": doc, "b/exercise.json": badDoc})
	require.NoError(t, err)
	sources.stage(archiveUpID, importer, bundle, mediaModel.UploadPurposeExerciseArchive)
	_, err = uc.PreviewArchiveImport(context.Background(), actor, exercise.PreviewArchiveInput{UploadID: archiveUpID})
	require.Error(t, err)
	protected, err := exercise.BuildProtectedArchiveV1(map[string][]byte{"exercise.json": doc}, "correct password")
	require.NoError(t, err)
	sources.stage(archiveUpID, importer, protected, mediaModel.UploadPurposeExerciseArchive)
	for _, password := range []string{"", "wrong password"} {
		_, err = uc.PreviewArchiveImport(context.Background(), actor, exercise.PreviewArchiveInput{UploadID: archiveUpID, Password: password})
		require.Error(t, err)
	}
	_, err = uc.PreviewArchiveImport(context.Background(), actor, exercise.PreviewArchiveInput{UploadID: archiveUpID, Password: "correct password"})
	require.NoError(t, err)
	_, err = uc.PreviewArchiveImport(context.Background(), actor, exercise.PreviewArchiveInput{UploadID: archiveUpID, FileID: uuid.Must(uuid.NewV7())})
	require.ErrorIs(t, err, exerciseModel.ErrImportSourceInvalid.Err())
}

func TestReimportPreviewRejectsAChangedDraftPointer(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	sources := newFakeSources()
	id, oldID, newID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(postgres.Exercise{ID: id, Name: "Existing target", DraftVersionID: uuid.NullUUID{UUID: oldID, Valid: true}}, nil)
	q.EXPECT().GetDraftVersion(gomock.Any(), id).Return(postgres.ExerciseVersion{ID: newID, ExerciseID: id, Name: "Concurrent draft", Status: "draft", Variants: []byte(`[]`)}, nil)
	archive := mustExerciseArchive(t, `{"name":"Portable task"}`, `[{"status":"draft","variants":[]}]`)
	sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
	uc := newUC(q, newFakeMedia())
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	_, err := uc.PreviewArchiveImport(context.Background(), exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}, exercise.PreviewArchiveInput{UploadID: archiveUpID, FixedExerciseID: id})
	require.ErrorIs(t, err, exerciseModel.ErrExerciseModified.Err(), "the old parent pointer must never authorize a different current draft")
}

func TestReimportPreviewEventUpdateDoesNotNeedCatalogCreationRights(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	sources := newFakeSources()
	id, event := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	target := postgres.Exercise{ID: id, Name: "Event target", Scope: 1, OwnerEventID: uuid.NullUUID{UUID: event, Valid: true}}
	q.EXPECT().ListUserEventMemberships(gomock.Any(), importer).Return([]postgres.ListUserEventMembershipsRow{{EventID: event, Role: 1}}, nil).AnyTimes()
	q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(target, nil).AnyTimes()
	q.EXPECT().GetImportTargetContext(gomock.Any(), id).Return(postgres.GetImportTargetContextRow{OwnerEventName: "Owner event", OwnerEventTag: "owner"}, nil)
	archive := mustExerciseArchive(t, `{"name":"Portable task"}`, `[{"status":"draft","variants":[]}]`)
	sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
	uc := newUC(q, newFakeMedia())
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	actor := exercise.Actor{UserID: importer, Role: rbac.RoleUser}
	view, err := uc.PreviewArchiveImport(context.Background(), actor, exercise.PreviewArchiveInput{UploadID: archiveUpID, Targets: []exercise.ImportTarget{{ItemKey: ".", Mode: "update", ExerciseID: id}}})
	require.NoError(t, err)
	require.Equal(t, "Owner event", view.Items[0].TargetExercise.OwnerEventName)
	_, err = uc.PreviewArchiveImport(context.Background(), actor, exercise.PreviewArchiveInput{UploadID: archiveUpID, Targets: []exercise.ImportTarget{{ItemKey: ".", Mode: "new"}}})
	require.ErrorIs(t, err, exerciseModel.ErrExerciseForbidden.Err(), "an actual catalog NEW choice still needs creation rights")
}

func TestReimportPreviewRejectsAnUnpublishedFormerCurrentVersion(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	sources := newFakeSources()
	id, version := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(postgres.Exercise{ID: id, Name: "Existing target", PublishedVersionID: uuid.NullUUID{UUID: version, Valid: true}}, nil)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), version).Return(postgres.ExerciseVersion{ID: version, ExerciseID: id, Name: "Former public snapshot", Status: "unpublished", Variants: []byte(`[]`)}, nil)
	archive := mustExerciseArchive(t, `{"name":"Portable task"}`, `[{"status":"draft","variants":[]}]`)
	sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
	uc := newUC(q, newFakeMedia())
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	_, err := uc.PreviewArchiveImport(context.Background(), exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}, exercise.PreviewArchiveInput{UploadID: archiveUpID, FixedExerciseID: id})
	require.ErrorIs(t, err, exerciseModel.ErrExerciseModified.Err(), "a concurrent Publish cannot leave a former version presented as the current working baseline")
}
