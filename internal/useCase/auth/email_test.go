package auth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

func newEmailUC(t *testing.T) (*auth.AuthUseCase, *postgresMocks.MockQuerier, *fakeNotifier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	notifier := &fakeNotifier{}
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: notifier,
		Config:   config.AuthConfig{TemporalCodeTTL: time.Hour, Hosts: testHosts("example.test")},
	})
	return uc, repo, notifier
}

// accountWithPassword is the stored row of a user whose password is "Correct!1".
func accountWithPassword(t *testing.T, uid uuid.UUID) postgres.User {
	t.Helper()
	hashed, err := password.New(password.Config{HashCost: 4}).Hash("Correct!1")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return postgres.User{ID: uid, Email: "old@b.test", FirstName: "Jane", HashedPassword: pgtype.Text{String: hashed, Valid: true}}
}

func TestRequestEmailChange_Taken(t *testing.T) {
	uc, repo, _ := newEmailUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(accountWithPassword(t, uid), nil)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "taken@b.test").Return(postgres.User{ID: uuid.Must(uuid.NewV7())}, nil)
	if err := uc.RequestEmailChange(context.Background(), uid, "taken@b.test", "Correct!1"); !errors.Is(err, userModel.ErrUserExists.Err()) {
		t.Fatalf("want ErrUserExists, got %v", err)
	}
}

func TestRequestEmailChange_SendsToNewAddress(t *testing.T) {
	uc, repo, notifier := newEmailUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(accountWithPassword(t, uid), nil)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "new@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).Return(postgres.TemporalCode{}, nil)

	if err := uc.RequestEmailChange(context.Background(), uid, "new@b.test", "Correct!1"); err != nil {
		t.Fatalf("request: %v", err)
	}
	if notifier.calls != 1 || notifier.lastRecipientEmail != "new@b.test" {
		t.Fatalf("notify: calls=%d recipient=%q (want 1 / new@b.test)", notifier.calls, notifier.lastRecipientEmail)
	}
}

// M1: a stolen session alone cannot move the account to another mailbox.
func TestRequestEmailChange_WrongPasswordRefused(t *testing.T) {
	uc, repo, notifier := newEmailUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(accountWithPassword(t, uid), nil)
	// No availability probe, no code, no mail.
	if err := uc.RequestEmailChange(context.Background(), uid, "new@b.test", "Wrong!1"); !errors.Is(err, authModel.ErrAuthInvalidOldPassword.Err()) {
		t.Fatalf("want ErrAuthInvalidOldPassword, got %v", err)
	}
	if notifier.calls != 0 {
		t.Fatalf("no mail may be sent, got %d", notifier.calls)
	}
}

func TestRequestEmailChange_PasswordlessAccountMustSetOne(t *testing.T) {
	uc, repo, _ := newEmailUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid}, nil)
	if err := uc.RequestEmailChange(context.Background(), uid, "new@b.test", ""); !errors.Is(err, authModel.ErrAuthPasswordRequired.Err()) {
		t.Fatalf("want ErrAuthPasswordRequired, got %v", err)
	}
}

func TestConfirmEmailChange_Success(t *testing.T) {
	uc, repo, notifier := newEmailUC(t)
	uid := uuid.Must(uuid.NewV7())
	data, _ := json.Marshal(temporalCodeModel.TemporalEmailChangeCodeData{UserID: uid, Email: "new@b.test"})
	bsCode := strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(rawCode)), "=", "")
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), auth.ExportHashTemporalCode(rawCode)).Return(postgres.TemporalCode{
		ID: uuid.Must(uuid.NewV7()), Type: temporalCodeModel.EmailChangeCodeType, Data: data, ExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	repo.EXPECT().DeleteTemporalCode(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "new@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).Return(postgres.User{ID: uid, Email: "old@b.test", FirstName: "Jane", Status: "active"}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if arg.Email != "new@b.test" || !arg.EmailConfirmed {
				return 0, fmt.Errorf("email change must write address+confirmed: %+v", arg)
			}
			return 1, nil
		})
	// M6: the pending recovery and email-change codes issued before the change are dead.
	for _, codeType := range []int32{temporalCodeModel.PasswordResettingCodeType, temporalCodeModel.EmailChangeCodeType} {
		repo.EXPECT().DeleteTemporalCodesForUser(gomock.Any(), postgres.DeleteTemporalCodesForUserParams{Type: codeType, UserID: uid.String()}).Return(int64(1), nil)
	}
	// The Google identity vouched for the old address: it is unlinked.
	repo.EXPECT().DeleteUserProviders(gomock.Any(), uid).Return(int64(1), nil)
	// M1: the recovery address changed — every session ends.
	repo.EXPECT().DeleteUserSessions(gomock.Any(), uid).Return(int64(1), nil)

	if err := uc.ConfirmEmailChange(context.Background(), bsCode); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	// The OLD address is told, with the new one and a way back.
	p, ok := notifier.lastPayload.(payloads.EmailChangedPayload)
	if !ok || notifier.calls != 1 || notifier.lastRecipientEmail != "old@b.test" || p.NewEmail != "new@b.test" || p.Name != "Jane" || !strings.HasSuffix(p.ResetURL, "/forgot-password") {
		t.Fatalf("old-address notice: calls=%d to=%q payload=%+v", notifier.calls, notifier.lastRecipientEmail, notifier.lastPayload)
	}
}
