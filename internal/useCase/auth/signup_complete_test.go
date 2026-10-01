package auth_test

import (
	"context"
	"errors"
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

func newCompleteUC(t *testing.T, superAdminEmail string) (*auth.AuthUseCase, *postgresMocks.MockQuerier, *token.Client) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	tk := token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"})
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo:     repo,
		Token:    tk,
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: &fakeNotifier{},
		Config: config.AuthConfig{
			SessionIdleTTL:  time.Hour,
			Domain:          "test",
			SuperAdminEmail: superAdminEmail,
		},
	})
	return uc, repo, tk
}

func incompleteRow(uid uuid.UUID, email string) postgres.User {
	return postgres.User{
		ID: uid, Email: email,
		Role: string(rbac.RoleUser), Status: string(userModel.UserStatusIncomplete),
	}
}

// The happy path is ONE whole-aggregate UPDATE (the old flow ran a
// 5-statement transaction): the written row must be active, confirmed, with
// ToS, password hash, and a domain-supplied updated_at — then a session.
func TestCompleteRegistration_SingleUpdateActivates(t *testing.T) {
	uc, repo, tk := newCompleteUC(t, "")
	uid := uuid.Must(uuid.NewV7())
	setupToken, _ := tk.GenerateSetupToken(uid)

	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(incompleteRow(uid, "jane@test.test"), nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.AssignableToTypeOf(postgres.UpdateUserParams{})).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if arg.Status != string(userModel.UserStatusActive) || !arg.EmailConfirmed {
				t.Fatalf("must write active+confirmed: %+v", arg)
			}
			if arg.FirstName != "Jane" || arg.LastName != "Doe" {
				t.Fatalf("profile not written: %+v", arg)
			}
			if !arg.HashedPassword.Valid || arg.HashedPassword.String == "" {
				t.Fatal("password hash not written")
			}
			if !arg.TosAcceptedAt.Valid || arg.TosVersion.Int32 != 1 {
				t.Fatalf("ToS not written: %+v", arg)
			}
			if !arg.UpdatedAt.Valid {
				t.Fatal("updated_at must come from the domain touch")
			}
			if arg.Role != string(rbac.RoleUser) {
				t.Fatalf("no promotion expected, got role %s", arg.Role)
			}
			return 1, nil
		})
	repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.CreateSessionParams) (postgres.Session, error) {
			return postgres.Session{ID: arg.ID, UserID: arg.UserID, ExpiresAt: arg.ExpiresAt}, nil
		})

	cookie, _, err := uc.CompleteRegistration(
		context.Background(), setupToken, "Jane", "Doe", "Secret!1", 1, "", authModel.SessionMetadata{},
	)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}
	if cookie == "" {
		t.Fatal("session cookie expected")
	}
}

// The optimistic-lock guard must compare the updated_at LOADED before the
// mutation, not the fresh value CompleteSetup writes. Snapshotting after the
// entity touch makes the WHERE never match the stored row, so every completion
// fails with zero rows (regression guard).
func TestCompleteRegistration_OptimisticLock_UsesLoadedUpdatedAt(t *testing.T) {
	uc, repo, tk := newCompleteUC(t, "")
	uid := uuid.Must(uuid.NewV7())
	setupToken, _ := tk.GenerateSetupToken(uid)

	loadedAt := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	row := incompleteRow(uid, "jane@test.test")
	row.UpdatedAt = pgtype.Timestamptz{Time: loadedAt, Valid: true}

	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(row, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if !arg.ExpectedUpdatedAt.Valid || !arg.ExpectedUpdatedAt.Time.Equal(loadedAt) {
				t.Fatalf("optimistic lock must compare the LOADED updated_at %v, got %v (a mutation leaked into the snapshot)", loadedAt, arg.ExpectedUpdatedAt.Time)
			}
			if arg.UpdatedAt.Time.Equal(loadedAt) {
				t.Fatal("written updated_at must be the fresh touch, not the loaded value")
			}
			return 1, nil
		})
	repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.CreateSessionParams) (postgres.Session, error) {
			return postgres.Session{ID: arg.ID, UserID: arg.UserID, ExpiresAt: arg.ExpiresAt}, nil
		})

	if _, _, err := uc.CompleteRegistration(
		context.Background(), setupToken, "Jane", "Doe", "Secret!1", 1, "", authModel.SessionMetadata{},
	); err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}
}

// The designated super-admin email is promoted inside the SAME single write.
func TestCompleteRegistration_PromotesSuperAdminInSameWrite(t *testing.T) {
	uc, repo, tk := newCompleteUC(t, "root@test.test")
	uid := uuid.Must(uuid.NewV7())
	setupToken, _ := tk.GenerateSetupToken(uid)

	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(incompleteRow(uid, "root@test.test"), nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if arg.Role != string(rbac.RoleSuperAdmin) {
				t.Fatalf("super-admin promotion must ride the same UPDATE, got %s", arg.Role)
			}
			return 1, nil
		})
	repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.CreateSessionParams) (postgres.Session, error) {
			return postgres.Session{ID: arg.ID, UserID: arg.UserID, ExpiresAt: arg.ExpiresAt}, nil
		})

	if _, _, err := uc.CompleteRegistration(
		context.Background(), setupToken, "Root", "Admin", "Secret!1", 1, "", authModel.SessionMetadata{},
	); err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}
}

// A failed write aborts the flow: no session is created.
func TestCompleteRegistration_UpdateFailure_NoSession(t *testing.T) {
	uc, repo, tk := newCompleteUC(t, "")
	uid := uuid.Must(uuid.NewV7())
	setupToken, _ := tk.GenerateSetupToken(uid)

	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(incompleteRow(uid, "j@test.test"), nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(0), errors.New("boom"))
	// no CreateSession expectation: creating one would fail the mock controller

	if _, _, err := uc.CompleteRegistration(
		context.Background(), setupToken, "J", "D", "Secret!1", 1, "", authModel.SessionMetadata{},
	); err == nil {
		t.Fatal("want error when the aggregate write fails")
	}
}

// Zero rows + the row is GONE on the re-read = the user vanished behind the
// token: invalid token (anti-enumeration — never reveal a user existed).
func TestCompleteRegistration_UserGoneOnReRead_InvalidToken(t *testing.T) {
	uc, repo, tk := newCompleteUC(t, "")
	uid := uuid.Must(uuid.NewV7())
	setupToken, _ := tk.GenerateSetupToken(uid)

	gomock.InOrder(
		repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(incompleteRow(uid, "j@test.test"), nil),
		repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(0), nil),
		repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{}, pgx.ErrNoRows),
	)

	_, _, err := uc.CompleteRegistration(
		context.Background(), setupToken, "J", "D", "Secret!1", 1, "", authModel.SessionMetadata{},
	)
	if !errors.Is(err, authModel.ErrInvalidToken.Err()) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

// Zero rows but the row is STILL PRESENT on the re-read = a concurrent write
// (e.g. a double-submitted setup) moved updated_at and won the race: the client
// holds stale state → ErrUserModified (409, reload), not a misleading token error.
func TestCompleteRegistration_ConcurrentModification_UserModified(t *testing.T) {
	uc, repo, tk := newCompleteUC(t, "")
	uid := uuid.Must(uuid.NewV7())
	setupToken, _ := tk.GenerateSetupToken(uid)

	gomock.InOrder(
		repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(incompleteRow(uid, "j@test.test"), nil),
		repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(0), nil),
		repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(incompleteRow(uid, "j@test.test"), nil),
	)

	_, _, err := uc.CompleteRegistration(
		context.Background(), setupToken, "J", "D", "Secret!1", 1, "", authModel.SessionMetadata{},
	)
	if !errors.Is(err, userModel.ErrUserModified.Err()) {
		t.Fatalf("want ErrUserModified, got %v", err)
	}
}
