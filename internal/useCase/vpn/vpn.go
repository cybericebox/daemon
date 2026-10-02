// Package vpn is the application layer for per-user VPN access records: it
// encrypts a WireGuard config before storage and decrypts it on read, keyed by
// (user, scope). It is the single seam both the test-deploy path and the future
// event-participant path use to persist a user's issued configs. Persistence
// goes through the whole-entity vpnRepo, so sqlc/pgtype never reach this layer.
package vpn

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/delivery/repository/vpnRepo"
	"github.com/cybericebox/daemon/internal/model"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	"github.com/cybericebox/daemon/pkg/secret"
)

// IRepository is the narrow data port the vpnRepo wraps (the sqlc Querier
// satisfies it structurally). The use case builds the domain repository from it,
// so it works only with domain entities.
type IRepository interface {
	vpnRepo.Queries
}

type VPNUseCase struct {
	configs *vpnRepo.Repository
	cipher  *secret.Cipher // nil when VPN_SECRETS_KEY is unset
}

type Dependencies struct {
	Repo   IRepository
	Cipher *secret.Cipher
}

func NewVPNUseCase(deps Dependencies) *VPNUseCase {
	return &VPNUseCase{configs: vpnRepo.New(deps.Repo), cipher: deps.Cipher}
}

// ConfigView is a stored config's metadata for listing (never the ciphertext).
type ConfigView struct {
	Scope     vpnModel.Scope
	ScopeRef  uuid.NullUUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// configContext binds a stored config to its row (user, scope and scope reference): a ciphertext copied to
// another row, or another user's, does not open.
func configContext(userID uuid.UUID, scope vpnModel.Scope, scopeRef uuid.NullUUID) []byte {
	ref := ""
	if scopeRef.Valid {
		ref = scopeRef.UUID.String()
	}
	return []byte(fmt.Sprintf("vpn-config:%s:%v:%s", userID, scope, ref))
}

// StoreConfig encrypts and upserts a user's VPN config for a scope. Re-issuing
// within the same scope replaces the stored ciphertext.
func (u *VPNUseCase) StoreConfig(ctx context.Context, userID uuid.UUID, scope vpnModel.Scope, scopeRef uuid.NullUUID, plaintext string) error {
	if !scope.Valid() {
		return vpnModel.ErrVPNScopeInvalid.Err()
	}
	if u.cipher == nil {
		return vpnModel.ErrVPNSecretsNotConfigured.Err()
	}
	ct, err := u.cipher.EncryptWithContext([]byte(plaintext), configContext(userID, scope, scopeRef))
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to encrypt VPN config").Err()
	}
	if _, err := u.configs.Store(ctx, vpnModel.NewConfig(userID, scope, scopeRef, ct, time.Now())); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to store VPN config").Err()
	}
	return nil
}

// GetConfig returns the decrypted VPN config for a (user, scope), or empty when
// none is stored.
func (u *VPNUseCase) GetConfig(ctx context.Context, userID uuid.UUID, scope vpnModel.Scope, scopeRef uuid.NullUUID) (string, error) {
	c, err := u.configs.Get(ctx, userID, scope, scopeRef)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return "", nil
		}
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to read VPN config").Err()
	}
	if u.cipher == nil {
		return "", vpnModel.ErrVPNSecretsNotConfigured.Err()
	}
	pt, err := u.cipher.DecryptWithContext(c.Config, configContext(userID, scope, scopeRef))
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to decrypt VPN config").Err()
	}
	return string(pt), nil
}

// ListUserConfigs returns metadata for a user's stored configs (no ciphertext),
// so a UI can show which VPNs a user holds without decrypting them.
func (u *VPNUseCase) ListUserConfigs(ctx context.Context, userID uuid.UUID) ([]ConfigView, error) {
	configs, err := u.configs.List(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list VPN configs").Err()
	}
	out := make([]ConfigView, 0, len(configs))
	for _, c := range configs {
		out = append(out, ConfigView{Scope: c.Scope, ScopeRef: c.ScopeRef, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt})
	}
	return out, nil
}

// DeleteConfig removes a user's VPN config for a scope (idempotent).
func (u *VPNUseCase) DeleteConfig(ctx context.Context, userID uuid.UUID, scope vpnModel.Scope, scopeRef uuid.NullUUID) error {
	if _, err := u.configs.Delete(ctx, userID, scope, scopeRef); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete VPN config").Err()
	}
	return nil
}
