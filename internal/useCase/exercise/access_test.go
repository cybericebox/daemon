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
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// TestAuthorizeExercise covers the W4 policy: RBAC grants admins everything;
// event exercises follow the owner event membership (read for any member,
// changes for owners/managers); catalog exercises are readable (published
// only) through the SQL availability rule and never writable by managers.
func TestAuthorizeExercise(t *testing.T) {
	userID, eventID, exerciseID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	eventRow := postgres.Exercise{ID: exerciseID, Scope: 1, OwnerEventID: uuid.NullUUID{UUID: eventID, Valid: true}}
	catalogRow := postgres.Exercise{ID: exerciseID}
	member := func(role int16) []postgres.ListUserEventMembershipsRow {
		return []postgres.ListUserEventMembershipsRow{{EventID: eventID, Role: role}}
	}
	for _, tc := range []struct {
		name        string
		role        rbac.Role
		row         *postgres.Exercise
		memberships []postgres.ListUserEventMembershipsRow
		readable    *bool
		action      exercise.Action
		wantErr     error
		wantFull    bool
	}{
		{name: "admin writes anything", role: rbac.RoleSuperAdmin, action: exercise.ActionPublish, wantFull: true},
		{name: "manager publishes own event exercise", role: rbac.RoleUser, row: &eventRow, memberships: member(1), action: exercise.ActionPublish, wantFull: true},
		{name: "viewer reads own event exercise", role: rbac.RoleUser, row: &eventRow, memberships: member(2), action: exercise.ActionRead, wantFull: true},
		{name: "viewer cannot edit", role: rbac.RoleUser, row: &eventRow, memberships: member(2), action: exercise.ActionWrite, wantErr: exerciseModel.ErrExerciseForbidden.Err()},
		{name: "stranger cannot read an event exercise", role: rbac.RoleUser, row: &eventRow, memberships: nil, action: exercise.ActionReadPublished, wantErr: exerciseModel.ErrExerciseForbidden.Err()},
		{name: "manager reads an available catalog exercise, published only", role: rbac.RoleUser, row: &catalogRow, readable: ptr(true), action: exercise.ActionReadPublished},
		{name: "unavailable catalog exercise", role: rbac.RoleUser, row: &catalogRow, readable: ptr(false), action: exercise.ActionReadPublished, wantErr: exerciseModel.ErrExerciseForbidden.Err()},
		{name: "manager never edits the catalog", role: rbac.RoleUser, row: &catalogRow, action: exercise.ActionWrite, wantErr: exerciseModel.ErrExerciseForbidden.Err()},
		{name: "manager never reads catalog history", role: rbac.RoleUser, row: &catalogRow, action: exercise.ActionRead, wantErr: exerciseModel.ErrExerciseForbidden.Err()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := postgresMocks.NewMockQuerier(ctrl)
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
			if tc.row != nil {
				q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(*tc.row, nil)
			}
			if tc.row == &eventRow {
				q.EXPECT().ListUserEventMemberships(gomock.Any(), userID).Return(tc.memberships, nil)
			}
			if tc.readable != nil {
				q.EXPECT().IsExerciseReadableBy(gomock.Any(), postgres.IsExerciseReadableByParams{ExerciseID: exerciseID, ViewerID: userID}).Return(*tc.readable, nil)
			}
			access, err := uc.AuthorizeExercise(context.Background(), exercise.Actor{UserID: userID, Role: tc.role}, exerciseID, tc.action)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil || access.Full != tc.wantFull {
				t.Fatalf("access=%+v err=%v", access, err)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

// TestSetExerciseAccess_NoneClearsSelection: «недоступно нікому» writes
// access level 3 and drops any selected events (no insert).
func TestSetExerciseAccess_NoneClearsSelection(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	exerciseID := uuid.Must(uuid.NewV7())
	row := postgres.Exercise{ID: exerciseID, AccessLevel: 1}
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(row, nil).MinTimes(1)
	q.EXPECT().UpdateExercise(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateExerciseParams) (int64, error) {
		if arg.AccessLevel != int16(exerciseModel.AccessNone) {
			t.Fatalf("written access level = %d", arg.AccessLevel)
		}
		row.AccessLevel = arg.AccessLevel
		return 1, nil
	})
	q.EXPECT().DeleteExerciseEventAccess(gomock.Any(), exerciseID).Return(nil)
	q.EXPECT().ListExerciseCardExtras(gomock.Any(), gomock.Any()).Return(nil, nil)

	view, err := uc.SetExerciseAccess(context.Background(), exercise.Actor{UserID: uuid.Must(uuid.NewV7()), Role: rbac.RoleSuperAdmin}, exerciseID,
		exercise.SetAccessInput{AccessLevel: exerciseModel.AccessNone, EventIDs: []uuid.UUID{uuid.Must(uuid.NewV7())}})
	if err != nil || view.AccessLevel != "none" || len(view.AccessEvents) != 0 {
		t.Fatalf("view=%+v err=%v", view.ExerciseScopeView, err)
	}
}

// M10: exercises.read (admin, admin_viewer) is not a licence for any media
// id. The media table also holds participants' answer files and avatars.
func TestAuthorizeFileDownload_AdminsReadOnlyExerciseFiles(t *testing.T) {
	ctx := context.Background()
	fileID, uploader := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	file := mediaModel.File{ID: fileID, CreatedBy: uuid.NullUUID{UUID: uploader, Valid: true}}
	referenced := func(q *postgresMocks.MockQuerier, ids ...uuid.UUID) {
		q.EXPECT().ListFileExerciseIDs(gomock.Any(), postgres.ListFileExerciseIDsParams{FileID: fileID, RefType: mediaModel.RefTypeExerciseVersion}).Return(ids, nil).AnyTimes()
	}
	newUCFor := func(t *testing.T) (*exercise.ExerciseUseCase, *postgresMocks.MockQuerier) {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		return exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()}), q
	}

	t.Run("admin_viewer: an answer file no exercise references", func(t *testing.T) {
		uc, q := newUCFor(t)
		referenced(q) // no exercise version references it
		err := uc.AuthorizeFileDownload(ctx, exercise.Actor{UserID: uuid.Must(uuid.NewV7()), Role: rbac.RoleAdminViewer}, file)
		if !errors.Is(err, mediaModel.ErrFileNotFound.Err()) {
			t.Fatalf("an unreferenced media file must not be served to a read-only admin: %v", err)
		}
	})
	t.Run("admin: the same", func(t *testing.T) {
		uc, q := newUCFor(t)
		referenced(q)
		if err := uc.AuthorizeFileDownload(ctx, exercise.Actor{UserID: uuid.Must(uuid.NewV7()), Role: rbac.RoleAdmin}, file); !errors.Is(err, mediaModel.ErrFileNotFound.Err()) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an exercise's file is readable by admins", func(t *testing.T) {
		uc, q := newUCFor(t)
		referenced(q, uuid.Must(uuid.NewV7()))
		if err := uc.AuthorizeFileDownload(ctx, exercise.Actor{UserID: uuid.Must(uuid.NewV7()), Role: rbac.RoleAdminViewer}, file); err != nil {
			t.Fatalf("an exercise file must stay readable: %v", err)
		}
	})
	t.Run("the uploader reads their pending upload", func(t *testing.T) {
		uc, _ := newUCFor(t)
		if err := uc.AuthorizeFileDownload(ctx, exercise.Actor{UserID: uploader, Role: rbac.RoleUser}, file); err != nil {
			t.Fatalf("uploader: %v", err)
		}
	})
}
