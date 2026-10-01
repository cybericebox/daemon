package exercise_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestPublishDraft_NoDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID}, nil)
	q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{}, errNoRows())

	_, err := uc.PublishDraft(context.Background(), exID)
	if !errors.Is(err, exerciseModel.ErrNoDraft.Err()) {
		t.Fatalf("want ErrNoDraft, got %v", err)
	}
}

func TestPublishDraft_ExerciseMissing(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{}, errNoRows())

	_, err := uc.PublishDraft(context.Background(), exID)
	if !errors.Is(err, exerciseModel.ErrExerciseNotFound.Err()) {
		t.Fatalf("want ErrExerciseNotFound, got %v", err)
	}
}

func TestPublishDraft_InvalidTopologyRejected(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID := uuid.Must(uuid.NewV7())

	// Draft with a connection to a ghost device fails ValidateForPublish.
	vs := draftVariants("", false)
	vs[0].Topology.Connections = []exerciseModel.Connection{{Endpoints: []exerciseModel.Endpoint{
		{Kind: exerciseModel.EndpointDevice, DeviceID: uuid.Must(uuid.NewV7()), Interface: "eth0"},
		{Kind: exerciseModel.EndpointDevice, DeviceID: uuid.Must(uuid.NewV7()), Interface: "eth0"},
	}}}
	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID}, nil)
	q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{
		ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "draft", Variants: mustMarshal(t, vs),
	}, nil)

	_, err := uc.PublishDraft(context.Background(), exID)
	if !errors.Is(err, exerciseModel.ErrEndpointUnresolved.Err()) {
		t.Fatalf("want ErrEndpointUnresolved, got %v", err)
	}
}

func TestGetVersion_MasksSecrets(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID, vID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	vs := draftVariants("ciphertext-blob", true)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), vID).Return(postgres.ExerciseVersion{
		ID: vID, ExerciseID: exID, Status: "draft", Variants: mustMarshal(t, vs),
	}, nil)

	view, err := uc.GetVersion(context.Background(), exID, vID)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	env := view.Variants[0].Topology.Devices[0].EnvVars[0]
	if env.Value != "" || !env.Secret {
		t.Fatalf("secret value must be masked in views: %+v", env)
	}
}

func TestGetVersion_WrongExercise(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	vID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetExerciseVersionByID(gomock.Any(), vID).Return(postgres.ExerciseVersion{
		ID: vID, ExerciseID: uuid.Must(uuid.NewV7()), Status: "draft", Variants: []byte(`[]`),
	}, nil)

	_, err := uc.GetVersion(context.Background(), uuid.Must(uuid.NewV7()), vID)
	if !errors.Is(err, exerciseModel.ErrExerciseVersionNotFound.Err()) {
		t.Fatalf("want ErrExerciseVersionNotFound, got %v", err)
	}
}

func TestDiscardDraft_NoDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID := uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)

	q.EXPECT().DiscardExerciseDraft(gomock.Any(), exID).Return(uuid.Nil, errNoRows())

	err := uc.DiscardDraft(context.Background(), exID)
	if !errors.Is(err, exerciseModel.ErrNoDraft.Err()) {
		t.Fatalf("want ErrNoDraft, got %v", err)
	}
}

func TestDiscardDraft_RemovesMediaReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	m := newFakeMedia()
	uc := newUC(q, m)
	exID, draftID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)

	q.EXPECT().DiscardExerciseDraft(gomock.Any(), exID).Return(draftID, nil)

	if err := uc.DiscardDraft(context.Background(), exID); err != nil {
		t.Fatalf("DiscardDraft: %v", err)
	}
	if len(m.removed) != 1 || m.removed[0] != draftID {
		t.Fatalf("draft's media references must be removed: %v", m.removed)
	}
}

func TestRollbackToVersion_VersionMissing(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID, vID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)

	q.EXPECT().CreateDraftFromVersion(gomock.Any(), gomock.Any()).Return(postgres.CreateDraftFromVersionRow{}, errNoRows())
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), vID).Return(postgres.ExerciseVersion{}, errNoRows())

	_, err := uc.RollbackToVersion(context.Background(), exID, vID, uuid.Nil)
	if !errors.Is(err, exerciseModel.ErrExerciseVersionNotFound.Err()) {
		t.Fatalf("want ErrExerciseVersionNotFound, got %v", err)
	}
}

// TestRollbackToVersion_ForeignVersionIsNotFound is the anti-IDOR regression:
// a versionID that exists but belongs to a DIFFERENT exercise must read as
// ErrExerciseVersionNotFound (404), never ErrDraftAlreadyExists (409) — the
// latter would leak "this versionID exists" to a caller with no rights over
// it, contradicting ErrExerciseVersionNotFound's documented anti-IDOR
// multi-site annotation.
func TestRollbackToVersion_ForeignVersionIsNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID, otherExID, vID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)

	q.EXPECT().CreateDraftFromVersion(gomock.Any(), gomock.Any()).Return(postgres.CreateDraftFromVersionRow{}, errNoRows())
	// Version exists, but under a different exercise.
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), vID).Return(postgres.ExerciseVersion{ID: vID, ExerciseID: otherExID}, nil)

	_, err := uc.RollbackToVersion(context.Background(), exID, vID, uuid.Nil)
	if !errors.Is(err, exerciseModel.ErrExerciseVersionNotFound.Err()) {
		t.Fatalf("want ErrExerciseVersionNotFound, got %v", err)
	}
}

func TestRollbackToVersion_DraftAlreadyExists(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID, vID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)

	q.EXPECT().CreateDraftFromVersion(gomock.Any(), gomock.Any()).Return(postgres.CreateDraftFromVersionRow{}, errNoRows())
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), vID).Return(postgres.ExerciseVersion{ID: vID, ExerciseID: exID}, nil)

	_, err := uc.RollbackToVersion(context.Background(), exID, vID, uuid.Nil)
	if !errors.Is(err, exerciseModel.ErrDraftAlreadyExists.Err()) {
		t.Fatalf("want ErrDraftAlreadyExists, got %v", err)
	}
}

func TestCreateCheckpoint_NoDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID := uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)
	q.EXPECT().CreateExerciseCheckpoint(gomock.Any(), gomock.Any()).Return(postgres.ExerciseVersion{}, errNoRows())
	_, err := uc.CreateCheckpoint(context.Background(), exID, uuid.Nil, "")
	if !errors.Is(err, exerciseModel.ErrNoDraft.Err()) {
		t.Fatalf("want ErrNoDraft, got %v", err)
	}
}

func TestCreateCheckpoint_PassesTrimmedLabel(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID := uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)
	q.EXPECT().CreateExerciseCheckpoint(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseCheckpointParams) (postgres.ExerciseVersion, error) {
			if arg.Label != "Before refactor" || arg.ExerciseID != exID {
				t.Fatalf("unexpected checkpoint params: %+v", arg)
			}
			return postgres.ExerciseVersion{ID: arg.NewID, ExerciseID: exID, Status: "checkpoint", AdminNote: "content note", Label: arg.Label, Variants: []byte(`[]`)}, nil
		})
	view, err := uc.CreateCheckpoint(context.Background(), exID, uuid.Nil, "  Before refactor  ")
	if err != nil || view.Label != "Before refactor" || view.AdminNote != "content note" {
		t.Fatalf("view=%+v err=%v", view, err)
	}
}

func TestListVersions_MapsLabel(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID := uuid.Must(uuid.NewV7())
	q.EXPECT().ListExerciseVersions(gomock.Any(), exID).Return([]postgres.ExerciseVersion{
		{ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "checkpoint", Label: "Before refactor", Variants: []byte(`[]`)},
	}, nil)
	items, err := uc.ListVersions(context.Background(), exID)
	if err != nil || len(items) != 1 || items[0].Label != "Before refactor" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestRestoreToVersion_ForeignSourceDoesNotTouchDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID, sourceID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), sourceID).Return(postgres.ExerciseVersion{
		ID: sourceID, ExerciseID: uuid.Must(uuid.NewV7()), Status: "checkpoint", Variants: []byte(`[]`),
	}, nil)
	_, err := uc.RestoreToVersion(context.Background(), exID, sourceID, uuid.Nil)
	if !errors.Is(err, exerciseModel.ErrExerciseVersionNotFound.Err()) {
		t.Fatalf("want ErrExerciseVersionNotFound, got %v", err)
	}
}

func TestListVersions_MapsToListItems(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID := uuid.Must(uuid.NewV7())
	vs := draftVariants("v", false)

	q.EXPECT().ListExerciseVersions(gomock.Any(), exID).Return([]postgres.ExerciseVersion{
		{ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "published", Variants: mustMarshal(t, vs)},
	}, nil)

	items, err := uc.ListVersions(context.Background(), exID)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(items) != 1 || items[0].Status != "published" || items[0].VariantCount != 1 {
		t.Fatalf("unexpected list items: %+v", items)
	}
}
