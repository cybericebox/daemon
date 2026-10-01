// Package vpnRepo is the repository for the per-user VPN Config aggregate: it
// accepts/returns whole domain entities and keeps all sqlc/pgtype mapping (the
// nullable scope_ref) out of the application layer. The stored Config field is
// ciphertext — encryption/decryption is the use case's concern, not this repo's.
package vpnRepo

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	UpsertUserVPNConfig(ctx context.Context, arg postgres.UpsertUserVPNConfigParams) (postgres.UserVpnConfig, error)
	GetUserVPNConfig(ctx context.Context, arg postgres.GetUserVPNConfigParams) (postgres.UserVpnConfig, error)
	ListUserVPNConfigs(ctx context.Context, userID uuid.UUID) ([]postgres.UserVpnConfig, error)
	DeleteUserVPNConfig(ctx context.Context, arg postgres.DeleteUserVPNConfigParams) (int64, error)
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// ToDomain maps a stored row to the domain entity (Config stays ciphertext).
func ToDomain(row postgres.UserVpnConfig) vpnModel.Config {
	return vpnModel.Config{
		ID:        row.ID,
		UserID:    row.UserID,
		Scope:     vpnModel.Scope(row.Scope),
		ScopeRef:  row.ScopeRef,
		Config:    row.Config,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

// Store upserts the config for its (user, scope, scope_ref) and returns the
// persisted entity. Re-issuing within the same scope replaces the ciphertext.
func (r *Repository) Store(ctx context.Context, c vpnModel.Config) (vpnModel.Config, error) {
	row, err := r.q.UpsertUserVPNConfig(ctx, postgres.UpsertUserVPNConfigParams{
		ID:        c.ID,
		UserID:    c.UserID,
		Scope:     string(c.Scope),
		ScopeRef:  c.ScopeRef,
		Config:    c.Config,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	})
	if err != nil {
		return vpnModel.Config{}, err
	}
	return ToDomain(row), nil
}

// Get loads a user's config for a scope. A null scope_ref matches the test scope
// NULL-safely (the query uses IS NOT DISTINCT FROM). Returns the raw not-found
// error for the caller to classify.
func (r *Repository) Get(ctx context.Context, userID uuid.UUID, scope vpnModel.Scope, scopeRef uuid.NullUUID) (vpnModel.Config, error) {
	row, err := r.q.GetUserVPNConfig(ctx, postgres.GetUserVPNConfigParams{
		UserID: userID, Scope: string(scope), ScopeRef: scopeRef,
	})
	if err != nil {
		return vpnModel.Config{}, err
	}
	return ToDomain(row), nil
}

// List returns all of a user's configs (ciphertext).
func (r *Repository) List(ctx context.Context, userID uuid.UUID) ([]vpnModel.Config, error) {
	rows, err := r.q.ListUserVPNConfigs(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]vpnModel.Config, 0, len(rows))
	for _, row := range rows {
		out = append(out, ToDomain(row))
	}
	return out, nil
}

// Delete removes a user's config for a scope; returns rows affected.
func (r *Repository) Delete(ctx context.Context, userID uuid.UUID, scope vpnModel.Scope, scopeRef uuid.NullUUID) (int64, error) {
	return r.q.DeleteUserVPNConfig(ctx, postgres.DeleteUserVPNConfigParams{
		UserID: userID, Scope: string(scope), ScopeRef: scopeRef,
	})
}
