package auth_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

func newAccountUC(t *testing.T) (*auth.AuthUseCase, *postgresMocks.MockQuerier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := auth.NewAuthUseCase(auth.Dependencies{Sessions: testSessions(t),
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: &fakeNotifier{},
		Config:   config.AuthConfig{},
	})
	return uc, repo
}

func TestGetAccount_MapsProvidersAndPassword(t *testing.T) {
	uc, repo := newAccountUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{
		ID: uid, Email: "a@b.test", FirstName: "Jane", Role: "user",
		HashedPassword: pgtype.Text{String: "h", Valid: true},
	}, nil)
	repo.EXPECT().GetUserProviders(gomock.Any(), uid).Return([]postgres.UserProvider{{Provider: "google"}}, nil)

	acc, err := uc.GetAccount(context.Background(), uid)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if !acc.HasPassword || len(acc.Providers) != 1 || acc.Providers[0] != "google" || acc.Email != "a@b.test" {
		t.Fatalf("unexpected account: %+v", acc)
	}
}

func TestDeleteAccount_SoftDeletesAndCutsLinks(t *testing.T) {
	uc, repo := newAccountUC(t)
	uid := uuid.Must(uuid.NewV7())
	// Single aggregate load: the lockout guard reads the role from it.
	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, Status: "active", HashedPassword: hashedPassword(t, "Correct!1")}, nil).Times(2) // re-auth, then the aggregate
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if arg.Status != "deleted" || !arg.DeletedAt.Valid || arg.FirstName != "" {
				return 0, fmt.Errorf("soft delete must scrub the row: %+v", arg)
			}
			return 1, nil
		})
	repo.EXPECT().RevokeUserSessions(gomock.Any(), gomock.Any()).Return(revokedRows(2), nil)
	repo.EXPECT().DeleteUserProviders(gomock.Any(), uid).Return(int64(1), nil)

	if err := uc.DeleteAccount(context.Background(), uid, "Correct!1"); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
}

func TestUpdateAccountProfile_NotFound(t *testing.T) {
	uc, repo := newAccountUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{}, pgx.ErrNoRows)
	if err := uc.UpdateAccountProfile(context.Background(), uid, "A", "B"); !errors.Is(err, userModel.ErrUserNotFound.Err()) {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
}

// Two admins/tabs racing on the same aggregate: the second write must fail
// with a 409 conflict, not silently overwrite (lost update) and not a 404.
func TestUpdateAccountProfile_ConcurrentModification_Conflict(t *testing.T) {
	uc, repo := newAccountUC(t)
	uid := uuid.Must(uuid.NewV7())

	// Load succeeds, the guarded UPDATE hits 0 rows, and the re-fetch shows
	// the row still exists — so it was modified concurrently.
	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, Status: "active"}, nil).Times(2)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(0), nil)

	err := uc.UpdateAccountProfile(context.Background(), uid, "A", "B")
	if !errors.Is(err, userModel.ErrUserModified.Err()) {
		t.Fatalf("want ErrUserModified (409), got %v", err)
	}
}

// The self-service path must carry the same lockout guard as the admin path:
// the LAST super_admin cannot delete their own account.
func TestDeleteAccount_LastSuperAdmin_Blocked(t *testing.T) {
	uc, repo := newAccountUC(t)
	uid := uuid.Must(uuid.NewV7())

	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, Role: string(rbac.RoleSuperAdmin), Status: "active", HashedPassword: hashedPassword(t, "Correct!1")}, nil).Times(2)
	repo.EXPECT().CountUsers(gomock.Any(), postgres.CountUsersParams{Roles: []string{string(rbac.RoleSuperAdmin)}, Status: "active"}).Return(int64(1), nil)
	// no UpdateUser / RevokeUserSessions expectations: the cascade must not run

	err := uc.DeleteAccount(context.Background(), uid, "Correct!1")
	if !errors.Is(err, authModel.ErrLastSuperAdmin.Err()) {
		t.Fatalf("want ErrLastSuperAdmin, got %v", err)
	}
}

// A super_admin who is NOT the last one may delete their own account.
func TestDeleteAccount_SuperAdminWithPeer_Allowed(t *testing.T) {
	uc, repo := newAccountUC(t)
	uid := uuid.Must(uuid.NewV7())

	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, Role: string(rbac.RoleSuperAdmin), Status: "active", HashedPassword: hashedPassword(t, "Correct!1")}, nil).Times(2)
	repo.EXPECT().CountUsers(gomock.Any(), postgres.CountUsersParams{Roles: []string{string(rbac.RoleSuperAdmin)}, Status: "active"}).Return(int64(2), nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().RevokeUserSessions(gomock.Any(), gomock.Any()).Return(revokedRows(1), nil)
	repo.EXPECT().DeleteUserProviders(gomock.Any(), uid).Return(int64(0), nil)

	if err := uc.DeleteAccount(context.Background(), uid, "Correct!1"); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
}

// The inactivity purge reuses the deletion cascade.
func TestDeleteInactiveAccount_StillInactive_Deletes(t *testing.T) {
	uc, repo := newAccountUC(t)
	uid := uuid.Must(uuid.NewV7())
	warnedAt := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, Status: "active", LastSeen: warnedAt.AddDate(-3, 0, 0)}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if arg.Status != "deleted" || !arg.DeletedAt.Valid {
				return 0, fmt.Errorf("inactive account must be soft-deleted: %+v", arg)
			}
			return 1, nil
		})
	repo.EXPECT().RevokeUserSessions(gomock.Any(), gomock.Any()).Return(revokedRows(0), nil)
	repo.EXPECT().DeleteUserProviders(gomock.Any(), uid).Return(int64(0), nil)

	deleted, err := uc.DeleteInactiveAccount(context.Background(), uid, warnedAt)
	if err != nil || !deleted {
		t.Fatalf("DeleteInactiveAccount: deleted=%v err=%v", deleted, err)
	}
}

// An owner who signed in after the warning keeps the account.
func TestDeleteInactiveAccount_SeenAfterWarning_Keeps(t *testing.T) {
	uc, repo := newAccountUC(t)
	uid := uuid.Must(uuid.NewV7())
	warnedAt := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, Status: "active", LastSeen: warnedAt.Add(time.Hour)}, nil)
	// no UpdateUser / RevokeUserSessions expectations: nothing may change

	deleted, err := uc.DeleteInactiveAccount(context.Background(), uid, warnedAt)
	if err != nil || deleted {
		t.Fatalf("DeleteInactiveAccount: deleted=%v err=%v", deleted, err)
	}
}

func hashedPassword(t *testing.T, plain string) pgtype.Text {
	t.Helper()
	hashed, err := password.New(password.Config{HashCost: 4}).Hash(plain)
	if err != nil {
		t.Fatal(err)
	}
	return pgtype.Text{String: hashed, Valid: true}
}

// L7: ending the account needs the owner, not only a session.
func TestDeleteAccount_WrongPasswordDeletesNothing(t *testing.T) {
	uc, repo := newAccountUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Status: "active", HashedPassword: hashedPassword(t, "Correct!1")}, nil)
	// no UpdateUser, no session or provider removal

	if err := uc.DeleteAccount(context.Background(), uid, "Wrong!1"); !errors.Is(err, authModel.ErrAuthInvalidOldPassword.Err()) {
		t.Fatalf("want ErrAuthInvalidOldPassword, got %v", err)
	}
}

// An account without a password (Google only) re-confirms with a sign-in from the last minutes.
func TestDeleteAccount_GoogleOnlyNeedsARecentSignIn(t *testing.T) {
	for name, age := range map[string]time.Duration{"recent": 2 * time.Minute, "old": 3 * time.Hour} {
		t.Run(name, func(t *testing.T) {
			uc, repo := newAccountUC(t)
			uid, sid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			ctx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: uid, SessionID: sid, Role: rbac.RoleUser})
			repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Status: "active"}, nil).AnyTimes()
			repo.EXPECT().GetSessionByID(gomock.Any(), sid).Return(postgres.Session{ID: sid, UserID: uid, CreatedAt: time.Now().Add(-age), ExpiresAt: time.Now().Add(time.Hour)}, nil)
			if name == "recent" {
				repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(1), nil)
				repo.EXPECT().RevokeUserSessions(gomock.Any(), gomock.Any()).Return(revokedRows(1), nil)
				repo.EXPECT().DeleteUserProviders(gomock.Any(), uid).Return(int64(1), nil)
				if err := uc.DeleteAccount(ctx, uid, ""); err != nil {
					t.Fatalf("a recent sign-in must be enough: %v", err)
				}
				return
			}
			if err := uc.DeleteAccount(ctx, uid, ""); !errors.Is(err, authModel.ErrAuthReauthRequired.Err()) {
				t.Fatalf("an old session of a Google-only account must be refused, got %v", err)
			}
		})
	}
}
