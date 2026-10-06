package auth_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
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

func newInviteUC(t *testing.T) (*auth.AuthUseCase, *postgresMocks.MockQuerier, *fakeNotifier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	allowSetupLinkIssue(repo)
	notifier := &fakeNotifier{}
	// Every queued invitation restarts the unconfirmed-account clock.
	repo.EXPECT().MarkUserInvitationSent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.MarkUserInvitationSentParams) (int64, error) {
		notifier.marked = append(notifier.marked, p.ID)
		return 1, nil
	}).AnyTimes()
	uc := auth.NewAuthUseCase(auth.Dependencies{Sessions: testSessions(t),
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: notifier,
		Config:   config.AuthConfig{TemporalCodeTTL: time.Hour, Hosts: testHosts("example.test")},
	})
	return uc, repo, notifier
}

func inviteCtx(role rbac.Role) context.Context {
	return rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: role})
}

func TestInviteUser_CannotAssignAbove(t *testing.T) {
	uc, _, _ := newInviteUC(t)
	if err := uc.InviteUser(inviteCtx(rbac.RoleAdmin), "x@b.test", rbac.RoleSuperAdmin, "A", "B"); !errors.Is(err, authModel.ErrCannotAssignRole.Err()) {
		t.Fatalf("want ErrCannotAssignRole, got %v", err)
	}
}

func TestInvite_MissingSessionReturnsUnauthenticated(t *testing.T) {
	uc, _, _ := newInviteUC(t)
	if err := uc.InviteUser(context.Background(), "x@b.test", rbac.RoleUser, "", ""); !errors.Is(err, authModel.ErrAuthInvalidSession.Err()) {
		t.Fatalf("InviteUser: want ErrAuthInvalidSession, got %v", err)
	}
	if _, err := uc.InviteUsers(context.Background(), rbac.RoleUser, []string{"x@b.test"}); !errors.Is(err, authModel.ErrAuthInvalidSession.Err()) {
		t.Fatalf("InviteUsers: want ErrAuthInvalidSession, got %v", err)
	}
}

func TestInviteUser_NewAccount_SendsToInvitee(t *testing.T) {
	uc, repo, notifier := newInviteUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "new@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any()).Return(postgres.User{}, nil)
	if err := uc.InviteUser(inviteCtx(rbac.RoleAdmin), "new@b.test", rbac.RoleUser, "Jane", "Roe"); err != nil {
		t.Fatalf("invite: %v", err)
	}
	if notifier.calls != 1 || notifier.lastRecipientEmail != "new@b.test" {
		t.Fatalf("notify: calls=%d recipient=%q", notifier.calls, notifier.lastRecipientEmail)
	}
}

func TestInviteUser_ActiveAccount_Rejected(t *testing.T) {
	uc, repo, _ := newInviteUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "act@b.test").Return(postgres.User{ID: uuid.Must(uuid.NewV7()), Status: string(userModel.UserStatusActive)}, nil)
	if err := uc.InviteUser(inviteCtx(rbac.RoleAdmin), "act@b.test", rbac.RoleUser, "A", "B"); !errors.Is(err, userModel.ErrUserExists.Err()) {
		t.Fatalf("want ErrUserExists, got %v", err)
	}
}

func TestInviteUser_IncompleteAccount_Reinvites(t *testing.T) {
	uc, repo, notifier := newInviteUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByEmail(gomock.Any(), "inc@b.test").Return(postgres.User{ID: uid, Status: string(userModel.UserStatusIncomplete)}, nil)
	repo.EXPECT().DeleteUserProviders(gomock.Any(), uid).Return(int64(0), nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Status: string(userModel.UserStatusIncomplete)}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if arg.Role != string(rbac.RoleAdminViewer) {
				return 0, fmt.Errorf("re-invite must write the invited role, got %s", arg.Role)
			}
			return 1, nil
		})
	if err := uc.InviteUser(inviteCtx(rbac.RoleAdmin), "inc@b.test", rbac.RoleAdminViewer, "A", "B"); err != nil {
		t.Fatalf("re-invite: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("want 1 notify, got %d", notifier.calls)
	}
}

func TestInviteUsers_DedupsAndCollectsResults(t *testing.T) {
	uc, repo, notifier := newInviteUC(t)

	// "a@b.test" appears twice in the input but should only be processed once.
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any()).Return(postgres.User{}, nil)

	repo.EXPECT().GetUserByEmail(gomock.Any(), "c@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any()).Return(postgres.User{}, nil)

	results, err := uc.InviteUsers(inviteCtx(rbac.RoleAdmin), rbac.RoleUser, []string{"a@b.test", "a@b.test", "c@b.test"})
	if err != nil {
		t.Fatalf("InviteUsers: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}

	// Verify unique emails only: "a@b.test" once, "c@b.test" once.
	emails := make(map[string]int)
	for _, r := range results {
		emails[r.Email]++
		if r.Err != nil {
			t.Errorf("unexpected error for %q: %v", r.Email, r.Err)
		}
	}
	if emails["a@b.test"] != 1 {
		t.Errorf("want a@b.test exactly once, got %d", emails["a@b.test"])
	}
	if notifier.calls != 2 {
		t.Fatalf("want 2 notify calls, got %d", notifier.calls)
	}
}

func TestInviteUsers_BatchContinuesOnFailure(t *testing.T) {
	uc, repo, notifier := newInviteUC(t)

	// "bad@b.test" has an active account — should yield ErrUserExists, no CreateUser, no notify.
	repo.EXPECT().GetUserByEmail(gomock.Any(), "bad@b.test").Return(
		postgres.User{ID: uuid.Must(uuid.NewV7()), Status: string(userModel.UserStatusActive)}, nil)

	// "good@b.test" is new — should succeed.
	repo.EXPECT().GetUserByEmail(gomock.Any(), "good@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any()).Return(postgres.User{}, nil)

	results, err := uc.InviteUsers(inviteCtx(rbac.RoleAdmin), rbac.RoleUser, []string{"bad@b.test", "good@b.test"})
	if err != nil {
		t.Fatalf("overall InviteUsers must not fail, got: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].Email != "bad@b.test" {
		t.Errorf("results[0] email want bad@b.test, got %q", results[0].Email)
	}
	if !errors.Is(results[0].Err, userModel.ErrUserExists.Err()) {
		t.Errorf("results[0] want ErrUserExists, got %v", results[0].Err)
	}
	if results[1].Email != "good@b.test" {
		t.Errorf("results[1] email want good@b.test, got %q", results[1].Email)
	}
	if results[1].Err != nil {
		t.Errorf("results[1] want nil error, got %v", results[1].Err)
	}
	if notifier.calls != 1 {
		t.Fatalf("want 1 notify call, got %d", notifier.calls)
	}
}

// Each line gets its own role (empty = user) and outcome: bad address, bad or
// too high role and existing account do not stop the other lines.
func TestInviteEntries_PerLineRolesAndCodes(t *testing.T) {
	uc, repo, notifier := newInviteUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "new@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateUserParams) (postgres.User, error) {
		if p.Role != string(rbac.RoleUser) || p.FirstName != "Olena" || p.LastName != "Koval" || p.Status != string(userModel.UserStatusIncomplete) {
			t.Fatalf("unexpected pending account: %+v", p)
		}
		return postgres.User{ID: p.ID}, nil
	})
	repo.EXPECT().GetUserByEmail(gomock.Any(), "act@b.test").Return(postgres.User{ID: uuid.Must(uuid.NewV7()), Status: string(userModel.UserStatusActive)}, nil)
	results, err := uc.InviteEntries(inviteCtx(rbac.RoleAdmin), []auth.InviteEntry{
		{Email: " New@B.test ", FirstName: "Olena", LastName: "Koval"},
		{Email: "new@b.test"},
		{Email: "broken"},
		{Email: "boss@b.test", Role: rbac.RoleSuperAdmin},
		{Email: "odd@b.test", Role: "wizard"},
		{Email: "act@b.test"},
	})
	if err != nil {
		t.Fatalf("InviteEntries: %v", err)
	}
	got := make([]string, 0, len(results))
	for _, r := range results {
		got = append(got, fmt.Sprintf("%s:%s:%s", r.Email, r.Role, r.Code))
	}
	want := []string{"new@b.test:user:", "broken:user:email_invalid", "boss@b.test:super_admin:role_forbidden", "odd@b.test:wizard:role_invalid", "act@b.test:user:user_exists"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("results:\n got %v\nwant %v", got, want)
	}
	if notifier.calls != 1 || notifier.lastRecipientEmail != "new@b.test" {
		t.Fatalf("notify: calls=%d recipient=%q", notifier.calls, notifier.lastRecipientEmail)
	}
	if len(notifier.marked) != 1 {
		t.Fatalf("the sent invitation must restart the account clock, marked=%v", notifier.marked)
	}
}

func TestInviteEntries_BatchSize(t *testing.T) {
	uc, _, _ := newInviteUC(t)
	if _, err := uc.InviteEntries(inviteCtx(rbac.RoleAdmin), nil); !errors.Is(err, authModel.ErrInviteBatchSize.Err()) {
		t.Fatalf("want ErrInviteBatchSize, got %v", err)
	}
}

// H1: an admin invite over an unclaimed account must not inherit a provider
// somebody bound beforehand — otherwise the pre-registered Google identity of
// an attacker would claim the invited (admin) role.
func TestInviteUser_IncompleteAccount_ResetsProviderBinding(t *testing.T) {
	uc, repo, _ := newInviteUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByEmail(gomock.Any(), "inc@b.test").Return(postgres.User{ID: uid, Status: string(userModel.UserStatusIncomplete)}, nil)
	deleted := false
	repo.EXPECT().DeleteUserProviders(gomock.Any(), uid).DoAndReturn(func(_ context.Context, _ uuid.UUID) (int64, error) {
		deleted = true
		return 1, nil
	})
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Status: string(userModel.UserStatusIncomplete)}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ postgres.UpdateUserParams) (int64, error) {
		if !deleted {
			return 0, fmt.Errorf("role was changed before the old provider binding was dropped")
		}
		return 1, nil
	})
	if err := uc.InviteUser(inviteCtx(rbac.RoleSuperAdmin), "inc@b.test", rbac.RoleAdmin, "A", "B"); err != nil {
		t.Fatalf("re-invite: %v", err)
	}
	if !deleted {
		t.Fatal("provider binding must be reset on re-invite")
	}
}

func TestInviteUser_EmailIsNormalized(t *testing.T) {
	uc, repo, notifier := newInviteUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "mixed@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any()).Return(postgres.User{}, nil)
	if err := uc.InviteUser(inviteCtx(rbac.RoleAdmin), " Mixed@B.test", rbac.RoleUser, "", ""); err != nil {
		t.Fatalf("invite: %v", err)
	}
	if notifier.lastRecipientEmail != "mixed@b.test" {
		t.Fatalf("recipient %q", notifier.lastRecipientEmail)
	}
}

func TestInviteUser_InvalidEmailRefused(t *testing.T) {
	uc, _, _ := newInviteUC(t)
	if err := uc.InviteUser(inviteCtx(rbac.RoleAdmin), "Bob <bob@b.test>", rbac.RoleUser, "", ""); !errors.Is(err, authModel.ErrAuthInvalidEmail.Err()) {
		t.Fatalf("want ErrAuthInvalidEmail, got %v", err)
	}
}

// Admin rights are not handed out by admins: an admin cannot invite one (a super_admin can).
func TestInviteUser_AdminCannotInviteAnAdmin(t *testing.T) {
	uc, _, _ := newInviteUC(t)
	if err := uc.InviteUser(inviteCtx(rbac.RoleAdmin), "x@b.test", rbac.RoleAdmin, "A", "B"); !errors.Is(err, authModel.ErrCannotAssignRole.Err()) {
		t.Fatalf("want ErrCannotAssignRole, got %v", err)
	}
}
