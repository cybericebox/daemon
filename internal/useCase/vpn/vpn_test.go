package vpn_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	"github.com/cybericebox/daemon/internal/useCase/vpn"
	"github.com/cybericebox/daemon/pkg/secret"
)

const testKey = "6368616e676520746869732070617373776f726420746f206120736563726574"

func TestStoreConfig_EncryptsAndUpserts_GetDecrypts(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	cipher, err := secret.New(testKey)
	if err != nil {
		t.Fatal(err)
	}
	uc := vpn.NewVPNUseCase(vpn.Dependencies{Repo: q, Cipher: cipher})
	userID := uuid.Must(uuid.NewV7())

	var storedCT string
	q.EXPECT().UpsertUserVPNConfig(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpsertUserVPNConfigParams) (postgres.UserVpnConfig, error) {
			if arg.Config == "wg-plaintext" {
				t.Error("config stored as plaintext")
			}
			if arg.Scope != string(vpnModel.ScopeTest) {
				t.Errorf("scope = %q, want test", arg.Scope)
			}
			if arg.ScopeRef.Valid {
				t.Error("test scope must carry a null ref")
			}
			if arg.UserID != userID {
				t.Error("user id mismatch")
			}
			storedCT = arg.Config
			return postgres.UserVpnConfig{ID: arg.ID, Config: arg.Config}, nil
		},
	)
	if err := uc.StoreConfig(context.Background(), userID, vpnModel.ScopeTest, uuid.NullUUID{}, "wg-plaintext"); err != nil {
		t.Fatalf("store: %v", err)
	}

	q.EXPECT().GetUserVPNConfig(gomock.Any(), gomock.Any()).Return(postgres.UserVpnConfig{Config: storedCT}, nil)
	got, err := uc.GetConfig(context.Background(), userID, vpnModel.ScopeTest, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "wg-plaintext" {
		t.Errorf("decrypted config = %q, want wg-plaintext", got)
	}
}

func TestStoreConfig_InvalidScope(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	cipher, _ := secret.New(testKey)
	uc := vpn.NewVPNUseCase(vpn.Dependencies{Repo: q, Cipher: cipher})
	err := uc.StoreConfig(context.Background(), uuid.Must(uuid.NewV7()), vpnModel.Scope("bogus"), uuid.NullUUID{}, "x")
	if !errors.Is(err, vpnModel.ErrVPNScopeInvalid.Err()) {
		t.Errorf("want scope-invalid, got %v", err)
	}
}

func TestStoreConfig_NoCipher(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := vpn.NewVPNUseCase(vpn.Dependencies{Repo: q, Cipher: nil})
	err := uc.StoreConfig(context.Background(), uuid.Must(uuid.NewV7()), vpnModel.ScopeTest, uuid.NullUUID{}, "x")
	if !errors.Is(err, vpnModel.ErrVPNSecretsNotConfigured.Err()) {
		t.Errorf("want secrets-not-configured, got %v", err)
	}
}

// A stored config is bound to its row: the same ciphertext under another user, scope or reference does not open.
func TestAStoredConfigDoesNotOpenForAnotherUserOrScope(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	cipher, _ := secret.New(testKey)
	uc := vpn.NewVPNUseCase(vpn.Dependencies{Repo: q, Cipher: cipher})
	owner, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	var storedCT string
	q.EXPECT().UpsertUserVPNConfig(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpsertUserVPNConfigParams) (postgres.UserVpnConfig, error) {
			storedCT = arg.Config
			return postgres.UserVpnConfig{ID: arg.ID, Config: arg.Config}, nil
		})
	if err := uc.StoreConfig(context.Background(), owner, vpnModel.ScopeTest, uuid.NullUUID{}, "wg-secret"); err != nil {
		t.Fatal(err)
	}
	q.EXPECT().GetUserVPNConfig(gomock.Any(), gomock.Any()).Return(postgres.UserVpnConfig{Config: storedCT}, nil).AnyTimes()
	if got, err := uc.GetConfig(context.Background(), owner, vpnModel.ScopeTest, uuid.NullUUID{}); err != nil || got != "wg-secret" {
		t.Fatalf("the owner's row opens: %q %v", got, err)
	}
	if _, err := uc.GetConfig(context.Background(), other, vpnModel.ScopeTest, uuid.NullUUID{}); err == nil {
		t.Fatal("the same ciphertext under another user must not open")
	}
	if _, err := uc.GetConfig(context.Background(), owner, vpnModel.ScopeTest, uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}); err == nil {
		t.Fatal("the same ciphertext under another reference must not open")
	}
}
