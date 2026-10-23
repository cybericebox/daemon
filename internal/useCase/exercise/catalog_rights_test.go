package exercise_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// The catalog rights per role (docs/reviews/catalog-rights.md): who may create, import and export where.
// Event roles: 0 owner, 1 manager, 2 viewer; "outsider" is a user with no membership of the event.
type rightsCase struct {
	name        string
	role        rbac.Role
	memberships []postgres.ListUserEventMembershipsRow
}

var (
	rightsEvent = uuid.FromStringOrNil("01900000-0000-7000-8000-00000000e001")
	otherEvent  = uuid.FromStringOrNil("01900000-0000-7000-8000-00000000e002")
)

func memberOf(event uuid.UUID, role int16) []postgres.ListUserEventMembershipsRow {
	return []postgres.ListUserEventMembershipsRow{{EventID: event, Role: role}}
}

func rightsCases() []rightsCase {
	return []rightsCase{
		{name: "super_admin", role: rbac.RoleSuperAdmin},
		{name: "admin", role: rbac.RoleAdmin},
		{name: "admin_viewer", role: rbac.RoleAdminViewer},
		{name: "event owner", role: rbac.RoleUser, memberships: memberOf(rightsEvent, 0)},
		{name: "event manager", role: rbac.RoleUser, memberships: memberOf(rightsEvent, 1)},
		{name: "event viewer", role: rbac.RoleUser, memberships: memberOf(rightsEvent, 2)},
		{name: "manager of another event", role: rbac.RoleUser, memberships: memberOf(otherEvent, 1)},
		{name: "user without memberships", role: rbac.RoleUser},
	}
}

func rightsUseCase(t *testing.T, tc rightsCase, userID uuid.UUID) (*exercise.ExerciseUseCase, *postgresMocks.MockQuerier) {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	q.EXPECT().ListUserEventMemberships(gomock.Any(), userID).Return(tc.memberships, nil).AnyTimes()
	q.EXPECT().GetEventInfrastructureAllowed(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	return exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()}), q
}

func allowed(err error) string {
	switch {
	case err == nil:
		return "allow"
	case errors.Is(err, exerciseModel.ErrExerciseForbidden.Err()):
		return "deny"
	default:
		return "error: " + err.Error()
	}
}

// TestAuthorizeImport_PerRole: the catalog takes imports from exercises.write only (super_admin, admin); an
// event from exercises.write or its owner/manager. Viewers, managers of other events and plain users import nowhere.
func TestAuthorizeImport_PerRole(t *testing.T) {
	want := map[string][2]string{ // [catalog, rightsEvent]
		"super_admin":              {"allow", "allow"},
		"admin":                    {"allow", "allow"},
		"admin_viewer":             {"deny", "deny"},
		"event owner":              {"deny", "allow"},
		"event manager":            {"deny", "allow"},
		"event viewer":             {"deny", "deny"},
		"manager of another event": {"deny", "deny"},
		"user without memberships": {"deny", "deny"},
	}
	for _, tc := range rightsCases() {
		t.Run(tc.name, func(t *testing.T) {
			userID := uuid.Must(uuid.NewV7())
			uc, _ := rightsUseCase(t, tc, userID)
			actor := exercise.Actor{UserID: userID, Role: tc.role}
			event := rightsEvent
			got := [2]string{allowed(uc.AuthorizeImport(context.Background(), actor, nil)), allowed(uc.AuthorizeImport(context.Background(), actor, &event))}
			if got != want[tc.name] {
				t.Fatalf("import [catalog, event] = %v, want %v", got, want[tc.name])
			}
		})
	}
}

// TestRequireAuthor_PerRole: the target-free import routes (the example, the upload of an archive) are for
// whoever may import somewhere.
func TestRequireAuthor_PerRole(t *testing.T) {
	want := map[string]string{
		"super_admin": "allow", "admin": "allow", "admin_viewer": "deny", "event owner": "allow", "event manager": "allow",
		"event viewer": "deny", "manager of another event": "allow", "user without memberships": "deny",
	}
	for _, tc := range rightsCases() {
		t.Run(tc.name, func(t *testing.T) {
			userID := uuid.Must(uuid.NewV7())
			uc, _ := rightsUseCase(t, tc, userID)
			if got := allowed(uc.RequireAuthor(context.Background(), exercise.Actor{UserID: userID, Role: tc.role})); got != want[tc.name] {
				t.Fatalf("got %s, want %s", got, want[tc.name])
			}
		})
	}
}

// TestAuthorizeExport_PerRole: exercises.export exports any task; an owner/manager only the tasks of their
// event — never a catalog task (even one attached to their event), never another event's task; viewers nothing.
// One task outside the scope refuses the whole selection.
func TestAuthorizeExport_PerRole(t *testing.T) {
	own := postgres.Exercise{ID: uuid.Must(uuid.NewV7()), Scope: 1, OwnerEventID: uuid.NullUUID{UUID: rightsEvent, Valid: true}}
	foreign := postgres.Exercise{ID: uuid.Must(uuid.NewV7()), Scope: 1, OwnerEventID: uuid.NullUUID{UUID: otherEvent, Valid: true}}
	catalog := postgres.Exercise{ID: uuid.Must(uuid.NewV7()), Scope: 0}
	want := map[string][4]string{ // [own event task, another event's task, catalog task, own + catalog]
		"super_admin":              {"allow", "allow", "allow", "allow"},
		"admin":                    {"allow", "allow", "allow", "allow"},
		"admin_viewer":             {"deny", "deny", "deny", "deny"},
		"event owner":              {"allow", "deny", "deny", "deny"},
		"event manager":            {"allow", "deny", "deny", "deny"},
		"event viewer":             {"deny", "deny", "deny", "deny"},
		"manager of another event": {"deny", "allow", "deny", "deny"},
		"user without memberships": {"deny", "deny", "deny", "deny"},
	}
	for _, tc := range rightsCases() {
		t.Run(tc.name, func(t *testing.T) {
			userID := uuid.Must(uuid.NewV7())
			uc, q := rightsUseCase(t, tc, userID)
			for _, row := range []postgres.Exercise{own, foreign, catalog} {
				q.EXPECT().GetExerciseByID(gomock.Any(), row.ID).Return(row, nil).AnyTimes()
			}
			actor := exercise.Actor{UserID: userID, Role: tc.role}
			export := func(ids ...uuid.UUID) string {
				return allowed(uc.AuthorizeExport(context.Background(), actor, ids))
			}
			got := [4]string{export(own.ID), export(foreign.ID), export(catalog.ID), export(own.ID, catalog.ID)}
			if got != want[tc.name] {
				t.Fatalf("export = %v, want %v", got, want[tc.name])
			}
		})
	}
}

// TestAuthorizeExport_MissingTask: an unknown id is not found (nothing exported).
func TestAuthorizeExport_MissingTask(t *testing.T) {
	userID, missing := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc, q := rightsUseCase(t, rightsCase{role: rbac.RoleUser, memberships: memberOf(rightsEvent, 1)}, userID)
	q.EXPECT().GetExerciseByID(gomock.Any(), missing).Return(postgres.Exercise{}, pgx.ErrNoRows)
	err := uc.AuthorizeExport(context.Background(), exercise.Actor{UserID: userID, Role: rbac.RoleUser}, []uuid.UUID{missing})
	if !errors.Is(err, exerciseModel.ErrExerciseNotFound.Err()) {
		t.Fatalf("got %v", err)
	}
}

// TestGetAccessSummary_ImportExportPerRole: the app shows import and export to who may use them somewhere.
func TestGetAccessSummary_ImportExportPerRole(t *testing.T) {
	want := map[string][3]bool{ // [CanCreateCatalog, CanImport, CanExport]
		"super_admin":              {true, true, true},
		"admin":                    {true, true, true},
		"admin_viewer":             {false, false, false},
		"event owner":              {false, true, true},
		"event manager":            {false, true, true},
		"event viewer":             {false, false, false},
		"manager of another event": {false, true, true},
		"user without memberships": {false, false, false},
	}
	for _, tc := range rightsCases() {
		t.Run(tc.name, func(t *testing.T) {
			userID := uuid.Must(uuid.NewV7())
			uc, _ := rightsUseCase(t, tc, userID)
			summary, err := uc.GetAccessSummary(context.Background(), exercise.Actor{UserID: userID, Role: tc.role})
			if err != nil {
				t.Fatal(err)
			}
			if got := [3]bool{summary.CanCreateCatalog, summary.CanImport, summary.CanExport}; got != want[tc.name] {
				t.Fatalf("summary %v, want %v", got, want[tc.name])
			}
		})
	}
}

// TestListPermissions_ExportPerRow: in the table a moderator may select for export only the tasks of their event.
func TestListPermissions_ExportPerRow(t *testing.T) {
	userID := uuid.Must(uuid.NewV7())
	uc, q := rightsUseCase(t, rightsCase{role: rbac.RoleUser, memberships: memberOf(rightsEvent, 1)}, userID)
	own := postgres.Exercise{ID: uuid.Must(uuid.NewV7()), Scope: 1, OwnerEventID: uuid.NullUUID{UUID: rightsEvent, Valid: true}}
	catalog := postgres.Exercise{ID: uuid.Must(uuid.NewV7()), PublishedVersionID: uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}}
	q.EXPECT().ListExercisesPage(gomock.Any(), gomock.Any()).Return([]postgres.ListExercisesPageRow{{Exercise: own, Status: "none"}, {Exercise: catalog, Status: "published"}}, nil)
	q.EXPECT().CountExercisesPage(gomock.Any(), gomock.Any()).Return(int64(2), nil)
	q.EXPECT().ListExerciseCardExtras(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().ListVisibleVariantDevices(gomock.Any(), gomock.Any()).Return(nil, nil)
	res, err := uc.ListExercisesFor(context.Background(), exercise.Actor{UserID: userID, Role: rbac.RoleUser}, exercise.ExercisesFilter{Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Exercises[0].Permissions.CanExport || res.Exercises[1].Permissions.CanExport {
		t.Fatalf("own event task exportable, catalog task not: %+v / %+v", res.Exercises[0].Permissions, res.Exercises[1].Permissions)
	}
}
