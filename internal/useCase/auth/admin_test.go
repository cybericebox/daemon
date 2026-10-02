package auth_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

func newAdminUC(t *testing.T) (*auth.AuthUseCase, *postgresMocks.MockQuerier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: &fakeNotifier{},
		Config:   config.AuthConfig{},
	})
	return uc, repo
}

func adminCtx(role rbac.Role) context.Context {
	return rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{
		UserID:    uuid.Must(uuid.NewV7()),
		SessionID: uuid.Must(uuid.NewV7()),
		Role:      role,
	})
}

// --- ListUsers ---

func TestAdminListUsers_FirstPageNoMore(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	uid := uuid.Must(uuid.NewV7())
	// pageSize 10 → use-case requests 11; repo returns 1 → HasMore false, no cursor.
	repo.EXPECT().ListUsersCursor(gomock.Any(), gomock.AssignableToTypeOf(postgres.ListUsersCursorParams{})).
		DoAndReturn(func(_ context.Context, arg postgres.ListUsersCursorParams) ([]postgres.User, error) {
			if arg.LimitVal != 11 {
				t.Fatalf("want limit+1=11, got %d", arg.LimitVal)
			}
			if arg.Search != "alice" {
				t.Fatalf("want search=alice, got %q", arg.Search)
			}
			return []postgres.User{{ID: uid, Email: "alice@test.test", Role: "user"}}, nil
		})
	repo.EXPECT().CountUsers(gomock.Any(), gomock.AssignableToTypeOf(postgres.CountUsersParams{})).
		DoAndReturn(func(_ context.Context, arg postgres.CountUsersParams) (int64, error) {
			if arg.Search != "alice" {
				t.Fatalf("Count: want search=alice, got %q", arg.Search)
			}
			return 7, nil
		})
	res, err := uc.ListUsers(ctx, auth.UsersFilter{Search: "alice", PageSize: 10})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(res.Users) != 1 || res.HasMore || res.NextCursor != uuid.Nil {
		t.Fatalf("unexpected: %+v", res)
	}
	if res.Total != 7 {
		t.Fatalf("Total = %d, want 7", res.Total)
	}
}

func TestAdminListUsers_HasMoreEmitsCursor(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	last := uuid.Must(uuid.NewV7())
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	// pageSize 2 → requests 3; repo returns 3 → HasMore true, cursor is the
	// id of the 2nd (last kept) row.
	repo.EXPECT().ListUsersCursor(gomock.Any(), gomock.Any()).Return([]postgres.User{
		{ID: uuid.Must(uuid.NewV7()), Email: "a@b", Role: "user", CreatedAt: ts.Add(2 * time.Hour)},
		{ID: last, Email: "c@d", Role: "user", CreatedAt: ts},
		{ID: uuid.Must(uuid.NewV7()), Email: "e@f", Role: "user", CreatedAt: ts.Add(-time.Hour)},
	}, nil)
	repo.EXPECT().CountUsers(gomock.Any(), gomock.Any()).Return(int64(3), nil)
	res, err := uc.ListUsers(ctx, auth.UsersFilter{PageSize: 2})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(res.Users) != 2 || !res.HasMore {
		t.Fatalf("unexpected: %+v", res)
	}
	if res.NextCursor != last {
		t.Fatalf("NextCursor = %v, want last kept id %v", res.NextCursor, last)
	}
}

func TestAdminListUsers_CursorLooksUpKeysetPosition(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	cursor := uuid.Must(uuid.NewV7())
	ts := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	// The cursor row's (created_at, id) become the keyset position.
	repo.EXPECT().GetUserByID(gomock.Any(), cursor).Return(postgres.User{ID: cursor, CreatedAt: ts}, nil)
	repo.EXPECT().ListUsersCursor(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.ListUsersCursorParams) ([]postgres.User, error) {
			if !arg.CursorCreatedAt.Equal(ts) || arg.CursorID != cursor {
				t.Fatalf("keyset position not taken from cursor row: %+v", arg)
			}
			return []postgres.User{}, nil
		})
	repo.EXPECT().CountUsers(gomock.Any(), gomock.Any()).Return(int64(0), nil)

	if _, err := uc.ListUsers(ctx, auth.UsersFilter{Cursor: cursor, PageSize: 5}); err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
}

func TestAdminListUsers_UnknownCursorFallsBackToFirstPage(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	cursor := uuid.Must(uuid.NewV7())

	// Cursor row vanished (deleted between pages) — forgiving: first page.
	repo.EXPECT().GetUserByID(gomock.Any(), cursor).Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().ListUsersCursor(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.ListUsersCursorParams) ([]postgres.User, error) {
			if arg.CursorCreatedAt.Year() != 9999 {
				t.Fatalf("want first-page sentinel position, got %+v", arg)
			}
			return []postgres.User{}, nil
		})
	repo.EXPECT().CountUsers(gomock.Any(), gomock.Any()).Return(int64(0), nil)

	if _, err := uc.ListUsers(ctx, auth.UsersFilter{Cursor: cursor, PageSize: 5}); err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
}

func TestGetUserStats_Aggregates(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	repo.EXPECT().CountUsers(gomock.Any(), gomock.AssignableToTypeOf(postgres.CountUsersParams{})).Return(int64(42), nil)
	repo.EXPECT().CountUsersByRoleAll(gomock.Any()).Return([]postgres.CountUsersByRoleAllRow{
		{Role: "admin", Count: 3}, {Role: "user", Count: 39},
	}, nil)
	repo.EXPECT().CountUsersByStatus(gomock.Any(), string(userModel.UserStatusBlocked)).Return(int64(5), nil)
	repo.EXPECT().CountUsersCreatedSince(gomock.Any(), gomock.Any()).Return(int64(8), nil)
	repo.EXPECT().CountUsersActiveSince(gomock.Any(), gomock.Any()).Return(int64(20), nil)
	repo.EXPECT().AvgDailyActiveSince(gomock.Any(), gomock.Any()).Return(4.5, nil)
	repo.EXPECT().RegistrationsByDaySince(gomock.Any(), gomock.Any()).Return([]postgres.RegistrationsByDaySinceRow{
		{Day: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC), Count: 8},
	}, nil)
	s, err := uc.GetUserStats(ctx)
	if err != nil {
		t.Fatalf("GetUserStats: %v", err)
	}
	if s.Total != 42 || s.Blocked != 5 || len(s.ByRole) != 2 {
		t.Fatalf("unexpected stats: %+v", s)
	}
	if s.NewLast7d != 8 || s.ActiveLast7d != 20 || s.AvgDailyActive7d != 4.5 || len(s.RegistrationsByDay) != 1 {
		t.Fatalf("unexpected window stats: %+v", s)
	}
}

// --- UpdateUserRole ---

func TestAdminUpdateUserRole_InvalidRole(t *testing.T) {
	uc, _ := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)

	err := uc.UpdateUserRole(ctx, uuid.Must(uuid.NewV7()), "ghost")
	if !errors.Is(err, authModel.ErrInvalidRole.Err()) {
		t.Fatalf("want ErrInvalidRole, got %v", err)
	}
}

func TestAdminUpdateUserRole_MissingSessionReturnsUnauthenticated(t *testing.T) {
	uc, _ := newAdminUC(t)
	err := uc.UpdateUserRole(context.Background(), uuid.Must(uuid.NewV7()), rbac.RoleUser)
	if !errors.Is(err, authModel.ErrAuthInvalidSession.Err()) {
		t.Fatalf("want ErrAuthInvalidSession, got %v", err)
	}
}

func TestAdminUpdateUserRole_CannotAssignAbove(t *testing.T) {
	uc, _ := newAdminUC(t)
	// admin cannot assign super_admin
	ctx := adminCtx(rbac.RoleAdmin)

	err := uc.UpdateUserRole(ctx, uuid.Must(uuid.NewV7()), rbac.RoleSuperAdmin)
	if !errors.Is(err, authModel.ErrCannotAssignRole.Err()) {
		t.Fatalf("want ErrCannotAssignRole, got %v", err)
	}
}

func TestAdminUpdateUserRole_Success(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	actor, _ := rbac.CurrentUserSessionFromContext(ctx)
	uid := uuid.Must(uuid.NewV7())

	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Role: string(rbac.RoleUser), Status: "active"}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if arg.Role != string(rbac.RoleAdmin) {
				return 0, fmt.Errorf("want role admin written, got %s", arg.Role)
			}
			if !arg.UpdatedBy.Valid || arg.UpdatedBy.UUID != actor.UserID {
				return 0, fmt.Errorf("want actor %s recorded, got %+v", actor.UserID, arg.UpdatedBy)
			}
			return 1, nil
		})

	if err := uc.UpdateUserRole(ctx, uid, rbac.RoleAdmin); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}
}

func TestAdminUpdateUserRole_DemoteSuperAdmin_LastOne(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	uid := uuid.Must(uuid.NewV7())

	// target user IS a super_admin and demoting to admin
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Role: string(rbac.RoleSuperAdmin)}, nil)
	// only 1 super_admin exists
	repo.EXPECT().CountUsers(gomock.Any(), postgres.CountUsersParams{Roles: []string{string(rbac.RoleSuperAdmin)}, Status: "active"}).Return(int64(1), nil)

	err := uc.UpdateUserRole(ctx, uid, rbac.RoleAdmin)
	if !errors.Is(err, authModel.ErrLastSuperAdmin.Err()) {
		t.Fatalf("want ErrLastSuperAdmin, got %v", err)
	}
}

func TestAdminUpdateUserRole_OrdinaryAdminCannotDemoteSuperAdmin(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleAdmin)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Role: string(rbac.RoleSuperAdmin), Status: "active"}, nil)

	err := uc.UpdateUserRole(ctx, uid, rbac.RoleUser)
	if !errors.Is(err, authModel.ErrInsufficientPermission.Err()) {
		t.Fatalf("want ErrInsufficientPermission, got %v", err)
	}
}

func TestAdminUpdateUserRole_UserNotFound(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	uid := uuid.Must(uuid.NewV7())

	// target user is a regular user (no lockout check triggered)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Role: string(rbac.RoleUser)}, nil)
	// UpdateUser returns 0 rows affected and the re-read misses too —
	// the user vanished (404), not a concurrent modification (409).
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{}, pgx.ErrNoRows)

	err := uc.UpdateUserRole(ctx, uid, rbac.RoleAdmin)
	if !errors.Is(err, userModel.ErrUserNotFound.Err()) {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
}

// --- DeleteUser ---

func TestAdminDeleteUser_LastSuperAdminBlocked(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	uid := uuid.Must(uuid.NewV7())

	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Role: string(rbac.RoleSuperAdmin)}, nil)
	repo.EXPECT().CountUsers(gomock.Any(), postgres.CountUsersParams{Roles: []string{string(rbac.RoleSuperAdmin)}, Status: "active"}).Return(int64(1), nil)

	err := uc.DeleteUser(ctx, uid)
	if !errors.Is(err, authModel.ErrLastSuperAdmin.Err()) {
		t.Fatalf("want ErrLastSuperAdmin, got %v", err)
	}
}

func TestAdminDeleteUser_OrdinaryAdminCannotDeleteSuperAdmin(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleAdmin)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Role: string(rbac.RoleSuperAdmin), Status: "active"}, nil)

	err := uc.DeleteUser(ctx, uid)
	if !errors.Is(err, authModel.ErrInsufficientPermission.Err()) {
		t.Fatalf("want ErrInsufficientPermission, got %v", err)
	}
}

func TestAdminDeleteUser_Success(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	uid := uuid.Must(uuid.NewV7())

	// Single aggregate load inside the cascade; the guard reads the role from it.
	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, Role: string(rbac.RoleUser), Status: "active"}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if arg.Status != "deleted" || !arg.DeletedAt.Valid || arg.FirstName != "" {
				return 0, fmt.Errorf("soft delete must scrub the row: %+v", arg)
			}
			return 1, nil
		})
	repo.EXPECT().DeleteUserSessions(gomock.Any(), uid).Return(int64(1), nil)
	repo.EXPECT().DeleteUserProviders(gomock.Any(), uid).Return(int64(0), nil)

	if err := uc.DeleteUser(ctx, uid); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
}

func TestAdminDeleteUser_UserNotFound(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	uid := uuid.Must(uuid.NewV7())

	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{}, pgx.ErrNoRows)

	err := uc.DeleteUser(ctx, uid)
	if !errors.Is(err, userModel.ErrUserNotFound.Err()) {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
}

// --- UpdateUserStatus ---

func TestAdminUpdateUserStatus_InvalidStatus(t *testing.T) {
	uc, _ := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)

	err := uc.UpdateUserStatus(ctx, uuid.Must(uuid.NewV7()), "unknown_status")
	if !errors.Is(err, authModel.ErrInvalidUserStatus.Err()) {
		t.Fatalf("want ErrInvalidUserStatus, got %v", err)
	}
}

func TestAdminUpdateUserStatus_OrdinaryAdminCannotBlockSuperAdmin(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleAdmin)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Role: string(rbac.RoleSuperAdmin), Status: "active"}, nil)

	err := uc.UpdateUserStatus(ctx, uid, userModel.UserStatusBlocked)
	if !errors.Is(err, authModel.ErrInsufficientPermission.Err()) {
		t.Fatalf("want ErrInsufficientPermission, got %v", err)
	}
}

func TestAdminUpdateUserStatus_Success(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	uid := uuid.Must(uuid.NewV7())

	// Setting status to "active" — no session revocation expected.
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Status: "blocked"}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if arg.Status != string(userModel.UserStatusActive) {
				return 0, fmt.Errorf("want active written, got %s", arg.Status)
			}
			return 1, nil
		})

	if err := uc.UpdateUserStatus(ctx, uid, userModel.UserStatusActive); err != nil {
		t.Fatalf("UpdateUserStatus: %v", err)
	}
}

func TestUpdateUserStatus_BlockedRevokesSessions(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	uid := uuid.Must(uuid.NewV7())

	// Both the aggregate write AND DeleteUserSessions must happen when blocking.
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Status: "active"}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if arg.Status != string(userModel.UserStatusBlocked) {
				return 0, fmt.Errorf("want blocked written, got %s", arg.Status)
			}
			return 1, nil
		})
	repo.EXPECT().DeleteUserSessions(gomock.Any(), uid).Return(int64(2), nil)

	if err := uc.UpdateUserStatus(ctx, uid, userModel.UserStatusBlocked); err != nil {
		t.Fatalf("UpdateUserStatus(blocked): %v", err)
	}
}

func TestAdminUpdateUserStatus_UserNotFound(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	uid := uuid.Must(uuid.NewV7())

	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Status: "active"}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{}, pgx.ErrNoRows)

	err := uc.UpdateUserStatus(ctx, uid, userModel.UserStatusActive)
	if !errors.Is(err, userModel.ErrUserNotFound.Err()) {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
}

// --- GetUser ---

func TestGetUser_NotFound(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleAdmin)
	id := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), id).Return(postgres.User{}, pgx.ErrNoRows)
	if _, err := uc.GetUser(ctx, id); !errors.Is(err, userModel.ErrUserNotFound.Err()) {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
}

func TestGetUser_ReturnsDetailWithSignInMethods(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleAdmin)
	id := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), id).Return(postgres.User{
		ID: id, Email: "a@b.test", FirstName: "Al", LastName: "Ice",
		Role: string(rbac.RoleAdmin), Status: string(userModel.UserStatusActive),
		EmailConfirmed: true,
		HashedPassword: pgtype.Text{String: "x", Valid: true},
	}, nil)
	repo.EXPECT().GetUserProviders(gomock.Any(), id).Return([]postgres.UserProvider{
		{Provider: userModel.GoogleProvider},
	}, nil)

	got, err := uc.GetUser(ctx, id)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Email != "a@b.test" || got.Status != userModel.UserStatusActive {
		t.Fatalf("unexpected detail: %+v", got)
	}
	if len(got.SignInMethods) != 2 || got.SignInMethods[0] != "email" || got.SignInMethods[1] != userModel.GoogleProvider {
		t.Fatalf("unexpected sign-in methods: %v", got.SignInMethods)
	}
}

// L10: blocking is a way out of service just like demoting or deleting.
func TestAdminUpdateUserStatus_BlockLastActiveSuperAdminRefused(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Role: string(rbac.RoleSuperAdmin), Status: "active"}, nil)
	repo.EXPECT().CountUsers(gomock.Any(), postgres.CountUsersParams{Roles: []string{string(rbac.RoleSuperAdmin)}, Status: "active"}).Return(int64(1), nil)
	// no UpdateUser, no session revocation

	if err := uc.UpdateUserStatus(ctx, uid, userModel.UserStatusBlocked); !errors.Is(err, authModel.ErrLastSuperAdmin.Err()) {
		t.Fatalf("want ErrLastSuperAdmin, got %v", err)
	}
}

// A super_admin who is already blocked is not counted on: demoting or
// deleting it cannot lock the platform.
func TestAdminDemoteBlockedSuperAdminIsAllowed(t *testing.T) {
	uc, repo := newAdminUC(t)
	ctx := adminCtx(rbac.RoleSuperAdmin)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Role: string(rbac.RoleSuperAdmin), Status: "blocked"}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	if err := uc.UpdateUserRole(ctx, uid, rbac.RoleAdmin); err != nil {
		t.Fatalf("demote blocked super_admin: %v", err)
	}
}

// L10 TOCTOU PoC: two super_admins demoted at the same moment each saw "two left" and both passed,
// leaving none. The check and its write are one decision now.
func TestAdminConcurrentDemotionsLeaveOneSuperAdmin(t *testing.T) {
	uc, repo := newAdminUC(t)
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	active := int64(2)
	var mu sync.Mutex
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.User, error) {
		return postgres.User{ID: id, Role: string(rbac.RoleSuperAdmin), Status: "active"}, nil
	}).AnyTimes()
	repo.EXPECT().CountUsers(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, postgres.CountUsersParams) (int64, error) {
		mu.Lock()
		defer mu.Unlock()
		return active, nil
	}).AnyTimes()
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, postgres.UpdateUserParams) (int64, error) {
		time.Sleep(30 * time.Millisecond) // the window between the check and the write
		mu.Lock()
		active--
		mu.Unlock()
		return 1, nil
	}).AnyTimes()

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, id := range []uuid.UUID{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = uc.UpdateUserRole(adminCtx(rbac.RoleSuperAdmin), id, rbac.RoleAdmin)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range results {
		if err == nil {
			ok++
		} else if !errors.Is(err, authModel.ErrLastSuperAdmin.Err()) {
			t.Fatalf("unexpected error %v", err)
		}
	}
	if ok != 1 || active != 1 {
		t.Fatalf("exactly one demotion may succeed: ok=%d active=%d", ok, active)
	}
}
