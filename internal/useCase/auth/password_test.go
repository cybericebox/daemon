package auth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
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
	payloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	"github.com/cybericebox/daemon/internal/model/rbac"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

func newPwUC(t *testing.T) (*auth.AuthUseCase, *postgresMocks.MockQuerier, *password.Client, *fakeNotifier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	pw := password.New(password.Config{HashCost: 4})
	notifier := &fakeNotifier{}
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: pw,
		Notifier: notifier,
		Config:   config.AuthConfig{TemporalCodeTTL: time.Hour, Hosts: testHosts("example.test")},
		// The mail work runs after the answer in production; the tests wait for it.
		Background: func(work func()) { work() },
	})
	return uc, repo, pw, notifier
}

func TestForgotPassword_UnknownSilent(t *testing.T) {
	uc, repo, _, notifier := newPwUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "missing@b.test").Return(postgres.User{}, pgx.ErrNoRows)

	if err := uc.ForgotPassword(context.Background(), "missing@b.test"); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if notifier.calls != 0 {
		t.Fatalf("want 0 notify, got %d", notifier.calls)
	}
}

func TestForgotPassword_KnownSendsCode(t *testing.T) {
	uc, repo, _, notifier := newPwUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{ID: uid, FirstName: "Jane"}, nil)
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).Return(postgres.TemporalCode{}, nil)

	if err := uc.ForgotPassword(context.Background(), "a@b.test"); err != nil {
		t.Fatalf("forgot: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("want 1 notify, got %d", notifier.calls)
	}
}

// An incomplete account has nothing to reset: re-send the continue-registration
// email instead (no reset code is created), same neutral success.
func TestForgotPassword_IncompleteResendsSetupLink(t *testing.T) {
	uc, repo, _, notifier := newPwUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByEmail(gomock.Any(), "i@b.test").
		Return(postgres.User{ID: uid, FirstName: "Ina", Status: string(userModel.UserStatusIncomplete)}, nil)
	// No CreateTemporalCode expected.

	if err := uc.ForgotPassword(context.Background(), "i@b.test"); err != nil {
		t.Fatalf("forgot: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("want 1 notify, got %d", notifier.calls)
	}
	p, ok := notifier.lastPayload.(payloads.ContinueRegistrationPayload)
	if !ok {
		t.Fatalf("want ContinueRegistrationPayload, got %T", notifier.lastPayload)
	}
	if !strings.HasPrefix(p.RegistrationURL, "https://id.example.test/setup?token=") || p.Name != "Ina" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestResetPassword_Success(t *testing.T) {
	uc, repo, _, _ := newPwUC(t)
	uid := uuid.Must(uuid.NewV7())
	data, _ := json.Marshal(temporalCodeModel.TemporalPasswordResettingCodeData{UserID: uid})
	bsCode := strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(rawCode)), "=", "")

	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), auth.ExportHashTemporalCode(rawCode)).Return(postgres.TemporalCode{
		ID: uuid.Must(uuid.NewV7()), Type: temporalCodeModel.PasswordResettingCodeType,
		Data: data, ExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	repo.EXPECT().DeleteTemporalCode(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).Return(postgres.User{Status: "active"}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	// M1: a reset ends EVERY session of the account (the old password may have
	// been lost together with a stolen cookie).
	repo.EXPECT().DeleteUserSessions(gomock.Any(), uid).Return(int64(2), nil)

	if err := uc.ResetPassword(context.Background(), bsCode, "Secret!1"); err != nil {
		t.Fatalf("reset: %v", err)
	}
}

func TestResetPassword_BadBase64(t *testing.T) {
	uc, _, _, _ := newPwUC(t)
	if err := uc.ResetPassword(context.Background(), "!!!not-base64!!!", "Secret!1"); !errors.Is(err, temporalCodeModel.ErrTemporalCodeInvalidCode.Err()) {
		t.Fatalf("want invalid code, got %v", err)
	}
}

func TestSetAccountPassword_WrongOld(t *testing.T) {
	uc, repo, pw, _ := newPwUC(t)
	uid := uuid.Must(uuid.NewV7())
	hashed, _ := pw.Hash("Correct!1")
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, HashedPassword: pgtype.Text{String: hashed, Valid: true}}, nil)

	if err := uc.SetAccountPassword(context.Background(), uid, "Wrong!1", "New!1pass"); !errors.Is(err, authModel.ErrAuthInvalidOldPassword.Err()) {
		t.Fatalf("want ErrAuthInvalidOldPassword, got %v", err)
	}
}

func TestSetAccountPassword_FirstPassword(t *testing.T) {
	uc, repo, _, _ := newPwUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, HashedPassword: pgtype.Text{}}, nil)
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).Return(postgres.User{Status: "active"}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().DeleteUserSessions(gomock.Any(), uid).Return(int64(0), nil)

	if err := uc.SetAccountPassword(context.Background(), uid, "", "New!1pass"); err != nil {
		t.Fatalf("set first password: %v", err)
	}
}

// M1: a password change signs every OTHER device out and keeps the current one.
func TestSetAccountPassword_RevokesOtherSessions(t *testing.T) {
	uc, repo, pw, _ := newPwUC(t)
	uid := uuid.Must(uuid.NewV7())
	current := uuid.Must(uuid.NewV7())
	hashed, _ := pw.Hash("Correct!1")
	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, Status: "active", HashedPassword: pgtype.Text{String: hashed, Valid: true}}, nil).Times(2)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().DeleteUserSessionsExcept(gomock.Any(), postgres.DeleteUserSessionsExceptParams{UserID: uid, ID: current}).Return(int64(3), nil)

	ctx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: uid, SessionID: current, Role: rbac.RoleUser})
	if err := uc.SetAccountPassword(ctx, uid, "Correct!1", "New!1pass"); err != nil {
		t.Fatalf("change: %v", err)
	}
}

// L2: only the SHA-256 of the mailed code is stored.
func TestForgotPassword_StoresHashOfMailedCode(t *testing.T) {
	uc, repo, _, notifier := newPwUC(t)
	uid := uuid.Must(uuid.NewV7())
	var stored string
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{ID: uid, Status: "active"}, nil)
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.CreateTemporalCodeParams) (postgres.TemporalCode, error) {
			stored = p.Code
			return postgres.TemporalCode{}, nil
		})
	if err := uc.ForgotPassword(context.Background(), "a@b.test"); err != nil {
		t.Fatalf("forgot: %v", err)
	}
	link := notifier.lastPayload.(payloads.PasswordResetPayload).ResetURL
	bs := link[strings.Index(link, "token=")+len("token="):]
	raw, err := base64.RawStdEncoding.DecodeString(bs)
	if err != nil {
		t.Fatalf("decode link code: %v", err)
	}
	if string(raw) == stored || stored != auth.ExportHashTemporalCode(string(raw)) {
		t.Fatalf("table must hold only the hash of the mailed code; stored=%q", stored)
	}
}

func TestApplyNewPassword_NoRows(t *testing.T) {
	uc, repo, _, _ := newPwUC(t)
	uid := uuid.Must(uuid.NewV7())
	// GetUserByID succeeds twice (old-password check + aggregate load),
	// then the post-conflict re-read misses: the user vanished (404).
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, HashedPassword: pgtype.Text{}}, nil).Times(2)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{}, pgx.ErrNoRows)

	err := uc.SetAccountPassword(context.Background(), uid, "", "New!1pass")
	if !errors.Is(err, userModel.ErrUserNotFound.Err()) {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
}

// L8: the work that depends on whether the address has an account runs after the answer: the
// request itself only looks the address up, so its timing says nothing about the account.
func TestForgotPassword_MailWorkIsDeferredPastTheAnswer(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	notifier := &fakeNotifier{}
	var queued []func()
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo:       repo,
		Token:      token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password:   password.New(password.Config{HashCost: 4}),
		Notifier:   notifier,
		Config:     config.AuthConfig{TemporalCodeTTL: time.Hour, Hosts: testHosts("example.test")},
		Background: func(work func()) { queued = append(queued, work) },
	})
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{ID: uid, Status: "active"}, nil)
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).Return(postgres.TemporalCode{}, nil)

	if err := uc.ForgotPassword(context.Background(), "a@b.test"); err != nil {
		t.Fatal(err)
	}
	if notifier.calls != 0 {
		t.Fatal("no code may be created and no mail queued before the answer is given")
	}
	if len(queued) != 1 {
		t.Fatalf("the mail work must be handed to the background, got %d jobs", len(queued))
	}
	queued[0]()
	if notifier.calls != 1 {
		t.Fatalf("the deferred work must send the mail, got %d", notifier.calls)
	}
}

// An unknown address queues nothing at all.
func TestForgotPassword_UnknownAddressQueuesNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	jobs := 0
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo: repo, Token: token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}), Notifier: &fakeNotifier{},
		Config:     config.AuthConfig{TemporalCodeTTL: time.Hour, Hosts: testHosts("example.test")},
		Background: func(func()) { jobs++ },
	})
	repo.EXPECT().GetUserByEmail(gomock.Any(), "ghost@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	if err := uc.ForgotPassword(context.Background(), "ghost@b.test"); err != nil || jobs != 0 {
		t.Fatalf("err=%v jobs=%d", err, jobs)
	}
}
