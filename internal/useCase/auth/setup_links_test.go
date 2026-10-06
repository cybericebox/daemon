package auth_test

import (
	"context"
	"encoding/json"
	"errors"
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
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

// allowSetupLinkIssue lets the use case issue and spend setup links: the store writes a row per
// link and deletes the user's earlier ones.
func allowSetupLinkIssue(repo *postgresMocks.MockQuerier) {
	repo.EXPECT().DeleteTemporalCodesForUser(gomock.Any(), gomock.Cond(func(p postgres.DeleteTemporalCodesForUserParams) bool {
		return p.Type == temporalCodeModel.SetupLinkCodeType
	})).Return(int64(0), nil).AnyTimes()
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Cond(func(p postgres.CreateTemporalCodeParams) bool {
		return p.Type == temporalCodeModel.SetupLinkCodeType
	})).Return(postgres.TemporalCode{}, nil).AnyTimes()
}

// liveSetupLink issues a setup token for uid and registers its live row, as the store would have.
func liveSetupLink(t *testing.T, repo *postgresMocks.MockQuerier, tk *token.Client, uid uuid.UUID) string {
	t.Helper()
	signed, id, expires, err := tk.IssueSetupToken(uid, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(temporalCodeModel.TemporalSetupLinkCodeData{UserID: uid})
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), auth.ExportHashTemporalCode(id)).Return(postgres.TemporalCode{
		ID: uuid.Must(uuid.NewV7()), Type: temporalCodeModel.SetupLinkCodeType, Data: data, ExpiresAt: expires,
	}, nil).AnyTimes()
	allowSetupLinkIssue(repo)
	return signed
}

func bareUC(t *testing.T) (*auth.AuthUseCase, *postgresMocks.MockQuerier, *token.Client) {
	t.Helper()
	repo := postgresMocks.NewMockQuerier(gomock.NewController(t))
	tk := token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"})
	uc := auth.NewAuthUseCase(auth.Dependencies{Sessions: testSessions(t),
		Repo: repo, Token: tk, Password: password.New(password.Config{HashCost: 4}), Notifier: &fakeNotifier{},
		Config: config.AuthConfig{SessionIdleTTL: time.Hour, TemporalCodeTTL: time.Hour, Hosts: testHosts("example.test")},
	})
	return uc, repo, tk
}

// A link whose row is gone (used, replaced by a newer one, never issued) is dead, signature or not.
func TestSetupLinkWithoutItsRowIsRefused(t *testing.T) {
	uc, repo, tk := bareUC(t)
	signed, _, _, err := tk.IssueSetupToken(uuid.Must(uuid.NewV7()), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), gomock.Any()).Return(postgres.TemporalCode{}, pgx.ErrNoRows)
	// no GetUserByID: the user is never reached
	if _, err = uc.GetSetupContext(context.Background(), signed); !errors.Is(err, authModel.ErrInvalidToken.Err()) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestExpiredSetupLinkRowIsRefused(t *testing.T) {
	uc, repo, tk := bareUC(t)
	uid := uuid.Must(uuid.NewV7())
	signed, _, _, _ := tk.IssueSetupToken(uid, time.Hour)
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), gomock.Any()).Return(postgres.TemporalCode{
		Type: temporalCodeModel.SetupLinkCodeType, ExpiresAt: time.Now().Add(-time.Minute),
	}, nil)
	if _, err := uc.GetSetupContext(context.Background(), signed); !errors.Is(err, authModel.ErrInvalidToken.Err()) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestRowOfAnotherCodeTypeIsNotASetupLink(t *testing.T) {
	uc, repo, tk := bareUC(t)
	signed, _, _, _ := tk.IssueSetupToken(uuid.Must(uuid.NewV7()), time.Hour)
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), gomock.Any()).Return(postgres.TemporalCode{
		Type: temporalCodeModel.PasswordResettingCodeType, ExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	if _, err := uc.GetSetupContext(context.Background(), signed); !errors.Is(err, authModel.ErrInvalidToken.Err()) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

// A re-invite revokes the account's earlier link BEFORE the new one is stored.
func TestReinviteReplacesThePreviousSetupLink(t *testing.T) {
	uc, repo, _ := bareUC(t)
	repo.EXPECT().MarkUserInvitationSent(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	uid := uuid.Must(uuid.NewV7())
	var order []string
	repo.EXPECT().GetUserByEmail(gomock.Any(), "inc@b.test").Return(postgres.User{ID: uid, Status: "incomplete"}, nil)
	repo.EXPECT().DeleteUserProviders(gomock.Any(), uid).Return(int64(0), nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Status: "incomplete"}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().DeleteTemporalCodesForUser(gomock.Any(), postgres.DeleteTemporalCodesForUserParams{Type: temporalCodeModel.SetupLinkCodeType, UserID: uid.String()}).
		DoAndReturn(func(context.Context, postgres.DeleteTemporalCodesForUserParams) (int64, error) {
			order = append(order, "revoke")
			return 1, nil
		})
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.CreateTemporalCodeParams) (postgres.TemporalCode, error) {
			if p.Type != temporalCodeModel.SetupLinkCodeType || len(p.Code) != 64 {
				t.Fatalf("a setup link row holds a hash of the token id: %+v", p)
			}
			order = append(order, "store")
			return postgres.TemporalCode{}, nil
		})
	if err := uc.InviteUser(inviteCtx(rbac.RoleAdmin), "inc@b.test", rbac.RoleUser, "A", "B"); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "revoke" || order[1] != "store" {
		t.Fatalf("order = %v", order)
	}
}

// Finishing the setup spends the link.
func TestCompletingSetupSpendsTheLink(t *testing.T) {
	uc, repo, tk := bareUC(t)
	uid := uuid.Must(uuid.NewV7())
	signed, id, expires, _ := tk.IssueSetupToken(uid, time.Hour)
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), auth.ExportHashTemporalCode(id)).Return(postgres.TemporalCode{Type: temporalCodeModel.SetupLinkCodeType, ExpiresAt: expires}, nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(incompleteRow(uid, "jane@test.test"), nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	spent := false
	repo.EXPECT().DeleteTemporalCodesForUser(gomock.Any(), postgres.DeleteTemporalCodesForUserParams{Type: temporalCodeModel.SetupLinkCodeType, UserID: uid.String()}).
		DoAndReturn(func(context.Context, postgres.DeleteTemporalCodesForUserParams) (int64, error) {
			spent = true
			return 1, nil
		})
	repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a postgres.CreateSessionParams) (postgres.Session, error) {
		return postgres.Session{ID: a.ID, UserID: a.UserID, ExpiresAt: a.ExpiresAt}, nil
	})
	if _, _, err := uc.CompleteRegistration(context.Background(), signed, "Jane", "Doe", "Secret!1", 1, "", authModel.SessionMetadata{}); err != nil {
		t.Fatal(err)
	}
	if !spent {
		t.Fatal("completing the setup must delete the link's row")
	}
}
