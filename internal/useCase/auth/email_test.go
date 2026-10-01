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
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
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
		Config:   config.AuthConfig{TemporalCodeTTL: time.Hour, Domain: "example.test"},
	})
	return uc, repo, notifier
}

func TestRequestEmailChange_Taken(t *testing.T) {
	uc, repo, _ := newEmailUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByEmail(gomock.Any(), "taken@b.test").Return(postgres.User{ID: uuid.Must(uuid.NewV7())}, nil)
	if err := uc.RequestEmailChange(context.Background(), uid, "taken@b.test"); !errors.Is(err, userModel.ErrUserExists.Err()) {
		t.Fatalf("want ErrUserExists, got %v", err)
	}
}

func TestRequestEmailChange_SendsToNewAddress(t *testing.T) {
	uc, repo, notifier := newEmailUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByEmail(gomock.Any(), "new@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Email: "old@b.test", FirstName: "Jane"}, nil)
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).Return(postgres.TemporalCode{}, nil)

	if err := uc.RequestEmailChange(context.Background(), uid, "new@b.test"); err != nil {
		t.Fatalf("request: %v", err)
	}
	if notifier.calls != 1 || notifier.lastRecipientEmail != "new@b.test" {
		t.Fatalf("notify: calls=%d recipient=%q (want 1 / new@b.test)", notifier.calls, notifier.lastRecipientEmail)
	}
}

func TestConfirmEmailChange_Success(t *testing.T) {
	uc, repo, _ := newEmailUC(t)
	uid := uuid.Must(uuid.NewV7())
	data, _ := json.Marshal(temporalCodeModel.TemporalEmailChangeCodeData{UserID: uid, Email: "new@b.test"})
	bsCode := strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte("raw")), "=", "")
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), "raw").Return(postgres.TemporalCode{
		ID: uuid.Must(uuid.NewV7()), Code: "raw", Type: temporalCodeModel.EmailChangeCodeType, Data: data, ExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	repo.EXPECT().DeleteTemporalCode(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "new@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).Return(postgres.User{Status: "active"}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if arg.Email != "new@b.test" || !arg.EmailConfirmed {
				return 0, fmt.Errorf("email change must write address+confirmed: %+v", arg)
			}
			return 1, nil
		})

	if err := uc.ConfirmEmailChange(context.Background(), bsCode); err != nil {
		t.Fatalf("confirm: %v", err)
	}
}
