package exercise_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// bulkOutcome is "changed", "deny" or the unexpected error of one item.
func bulkOutcome(r exercise.BulkResult) string {
	switch {
	case r.Err == nil:
		return "changed"
	case errors.Is(r.Err, exerciseModel.ErrExerciseForbidden.Err()):
		return "deny"
	case errors.Is(r.Err, exerciseModel.ErrExerciseNotFound.Err()):
		return "not found"
	case errors.Is(r.Err, exerciseModel.ErrExerciseAccessInvalid.Err()):
		return "invalid"
	default:
		return "error: " + r.Err.Error()
	}
}

// bulkRows are an own event task, another event's task and a catalog task.
func bulkRows(archived bool) (own, foreign, catalog postgres.Exercise) {
	at := pgtype.Timestamptz{Time: archiveLoadedAt, Valid: archived}
	own = postgres.Exercise{ID: uuid.Must(uuid.NewV7()), Name: "own", Scope: 1, OwnerEventID: uuid.NullUUID{UUID: rightsEvent, Valid: true}, UpdatedAt: archiveLoadedAt, ArchivedAt: at}
	foreign = postgres.Exercise{ID: uuid.Must(uuid.NewV7()), Name: "foreign", Scope: 1, OwnerEventID: uuid.NullUUID{UUID: otherEvent, Valid: true}, UpdatedAt: archiveLoadedAt, ArchivedAt: at}
	catalog = postgres.Exercise{ID: uuid.Must(uuid.NewV7()), Name: "catalog", Scope: 0, UpdatedAt: archiveLoadedAt, ArchivedAt: at}
	return own, foreign, catalog
}

// TestBulkSetArchived_PerRole: every item goes through the single archive rule (exercise write): admins archive any
// task; an owner/manager the tasks of their event only; a moderator's foreign task and the catalog are skipped;
// viewers and read-only admins change nothing. A refused item never stops the others.
func TestBulkSetArchived_PerRole(t *testing.T) {
	want := map[string][3]string{ // [own event task, another event's task, catalog task]
		"super_admin":              {"changed", "changed", "changed"},
		"admin":                    {"changed", "changed", "changed"},
		"admin_viewer":             {"deny", "deny", "deny"},
		"event owner":              {"changed", "deny", "deny"},
		"event manager":            {"changed", "deny", "deny"},
		"event viewer":             {"deny", "deny", "deny"},
		"manager of another event": {"deny", "changed", "deny"},
		"user without memberships": {"deny", "deny", "deny"},
	}
	for _, archived := range []bool{false, true} {
		for _, tc := range rightsCases() {
			t.Run(tc.name, func(t *testing.T) {
				userID := uuid.Must(uuid.NewV7())
				uc, q := rightsUseCase(t, tc, userID)
				expectUserNames(q)
				own, foreign, catalog := bulkRows(!archived)
				for _, row := range []postgres.Exercise{own, foreign, catalog} {
					q.EXPECT().GetExerciseByID(gomock.Any(), row.ID).Return(row, nil).AnyTimes()
				}
				written := map[uuid.UUID]bool{}
				byName := map[string]uuid.UUID{own.Name: own.ID, foreign.Name: foreign.ID, catalog.Name: catalog.ID}
				q.EXPECT().UpdateExercise(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateExerciseParams) (int64, error) {
					if arg.ArchivedAt.Valid != archived {
						t.Fatalf("archived_at written %v, want %v", arg.ArchivedAt.Valid, archived)
					}
					written[byName[arg.Name]] = true
					return 1, nil
				}).AnyTimes()
				results, _ := uc.BulkSetArchived(context.Background(), exercise.Actor{UserID: userID, Role: tc.role}, []uuid.UUID{own.ID, foreign.ID, catalog.ID}, archived)
				if len(results) != 3 {
					t.Fatalf("results = %d", len(results))
				}
				var got [3]string
				for i, r := range results {
					got[i] = bulkOutcome(r)
					if r.ID != []uuid.UUID{own.ID, foreign.ID, catalog.ID}[i] {
						t.Fatalf("result %d is for %s", i, r.ID)
					}
					if written[r.ID] != (r.Err == nil) {
						t.Fatalf("item %d: written=%v err=%v", i, written[r.ID], r.Err)
					}
					if r.Err == nil && (r.View.ArchivedAt != nil) != archived {
						t.Fatalf("item %d view archived=%v", i, r.View.ArchivedAt != nil)
					}
				}
				if got != want[tc.name] {
					t.Fatalf("archived=%v: got %v, want %v", archived, got, want[tc.name])
				}
			})
		}
	}
}

// TestBulkSetArchived_MissingAndDuplicates: a missing task is reported and skipped, the rest still change; a repeated
// id is handled once.
func TestBulkSetArchived_MissingAndDuplicates(t *testing.T) {
	userID := uuid.Must(uuid.NewV7())
	uc, q := rightsUseCase(t, rightsCase{role: rbac.RoleAdmin}, userID)
	expectUserNames(q)
	_, _, catalog := bulkRows(false)
	missing := uuid.Must(uuid.NewV7())
	q.EXPECT().GetExerciseByID(gomock.Any(), catalog.ID).Return(catalog, nil).AnyTimes()
	q.EXPECT().GetExerciseByID(gomock.Any(), missing).Return(postgres.Exercise{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().UpdateExercise(gomock.Any(), gomock.Any()).Return(int64(1), nil).Times(1)
	results, _ := uc.BulkSetArchived(context.Background(), exercise.Actor{UserID: userID, Role: rbac.RoleAdmin}, []uuid.UUID{missing, catalog.ID, catalog.ID}, true)
	if len(results) != 2 || bulkOutcome(results[0]) != "not found" || bulkOutcome(results[1]) != "changed" {
		t.Fatalf("results = %+v", results)
	}
}

// TestBulkSetAccess_PerItem: the route gate (exercises.write) is the static check; every item then goes through the
// single access rule: a catalog task changes, an event task is skipped as invalid (its access is its event's) and
// does not stop the next one.
func TestBulkSetAccess_PerItem(t *testing.T) {
	userID := uuid.Must(uuid.NewV7())
	uc, q := rightsUseCase(t, rightsCase{role: rbac.RoleAdmin}, userID)
	expectUserNames(q)
	own, _, catalog := bulkRows(false)
	for _, row := range []postgres.Exercise{own, catalog} {
		q.EXPECT().GetExerciseByID(gomock.Any(), row.ID).Return(row, nil).AnyTimes()
	}
	q.EXPECT().UpdateExercise(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateExerciseParams) (int64, error) {
		if arg.Name != catalog.Name || arg.AccessLevel != int16(exerciseModel.AccessNone) {
			t.Fatalf("unexpected access write %+v", arg)
		}
		return 1, nil
	}).Times(1)
	q.EXPECT().DeleteExerciseEventAccess(gomock.Any(), catalog.ID).Return(nil)
	q.EXPECT().ListExerciseCardExtras(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().ListPublishedVariantDevices(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	results, _ := uc.BulkSetAccess(context.Background(), exercise.Actor{UserID: userID, Role: rbac.RoleAdmin}, []uuid.UUID{own.ID, catalog.ID},
		exercise.SetAccessInput{AccessLevel: exerciseModel.AccessNone, UpdatedBy: userID})
	if got := [2]string{bulkOutcome(results[0]), bulkOutcome(results[1])}; got != [2]string{"invalid", "changed"} {
		t.Fatalf("got %v", got)
	}
	if results[1].View.AccessLevel != "none" {
		t.Fatalf("view access = %q", results[1].View.AccessLevel)
	}
}

// TestBulk_RefusesOverTheCap: more than MaxBulkExercises distinct tasks change nothing (the route caps them first).
func TestBulk_RefusesOverTheCap(t *testing.T) {
	userID := uuid.Must(uuid.NewV7())
	uc, _ := rightsUseCase(t, rightsCase{role: rbac.RoleAdmin}, userID) // no repository call is expected
	ids := make([]uuid.UUID, exercise.MaxBulkExercises+1)
	for i := range ids {
		ids[i] = uuid.Must(uuid.NewV7())
	}
	actor := exercise.Actor{UserID: userID, Role: rbac.RoleAdmin}
	if results, err := uc.BulkSetArchived(context.Background(), actor, ids, true); err == nil || results != nil {
		t.Fatalf("archive over the cap: results=%d err=%v", len(results), err)
	}
	if results, err := uc.BulkSetAccess(context.Background(), actor, ids, exercise.SetAccessInput{AccessLevel: exerciseModel.AccessNone}); err == nil || results != nil {
		t.Fatalf("access over the cap: results=%d err=%v", len(results), err)
	}
}
