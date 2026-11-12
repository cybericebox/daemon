// Package secretKeysRepo reads, and only reads, the stored ciphertexts of the platform's keyed secrets, one place per
// table and column, so the use case can tell which key sealed each of them and whether it still opens.
package secretKeysRepo

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

type Repository struct{ db postgres.DBTX }

func New(db postgres.DBTX) *Repository { return &Repository{db: db} }

// each runs the query and hands every row to scan, which reads it from the row's own columns.
func (r *Repository) each(ctx context.Context, query string, scan func(row interface{ Scan(...any) error }) error) error {
	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err = scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// SMTPPasswords visits the sealed SMTP passwords (mail_smtp_configs.password_ciphertext).
func (r *Repository) SMTPPasswords(ctx context.Context, visit func(id uuid.UUID, ciphertext string) error) error {
	return r.each(ctx, `SELECT id, password_ciphertext FROM mail_smtp_configs WHERE password_ciphertext <> ''`,
		func(row interface{ Scan(...any) error }) error {
			var id uuid.UUID
			var ct string
			if err := row.Scan(&id, &ct); err != nil {
				return err
			}
			return visit(id, ct)
		})
}

// AgentKeys visits the sealed private keys of the infrastructure agents; an empty value is not visited as empty
// (the callback gets "" for a column that is not set).
func (r *Repository) AgentKeys(ctx context.Context, visit func(id uuid.UUID, clientKey, accessKey string) error) error {
	return r.each(ctx, `SELECT id, client_key_ciphertext, access_private_key_ciphertext FROM infrastructure_agents
WHERE client_key_ciphertext <> '' OR access_private_key_ciphertext <> ''`,
		func(row interface{ Scan(...any) error }) error {
			var id uuid.UUID
			var clientKey, accessKey string
			if err := row.Scan(&id, &clientKey, &accessKey); err != nil {
				return err
			}
			return visit(id, clientKey, accessKey)
		})
}

// RegistrySettings visits the sealed push and pull tokens of the image registry settings (one row at most).
func (r *Repository) RegistrySettings(ctx context.Context, visit func(push, pull string) error) error {
	return r.each(ctx, `SELECT push_token_ciphertext, pull_token_ciphertext FROM image_registry_settings`,
		func(row interface{ Scan(...any) error }) error {
			var push, pull string
			if err := row.Scan(&push, &pull); err != nil {
				return err
			}
			return visit(push, pull)
		})
}

// EventRegistryTokens visits the sealed registry tokens of the events (event_registry_secrets.token_ciphertext).
func (r *Repository) EventRegistryTokens(ctx context.Context, visit func(id uuid.UUID, ciphertext string) error) error {
	return r.each(ctx, `SELECT id, token_ciphertext FROM event_registry_secrets WHERE token_ciphertext <> ''`,
		func(row interface{ Scan(...any) error }) error {
			var id uuid.UUID
			var ct string
			if err := row.Scan(&id, &ct); err != nil {
				return err
			}
			return visit(id, ct)
		})
}

// VPNConfigs visits the sealed VPN client configs (user_vpn_configs.config).
func (r *Repository) VPNConfigs(ctx context.Context, visit func(userID uuid.UUID, scope string, scopeRef uuid.NullUUID, ciphertext string) error) error {
	return r.each(ctx, `SELECT user_id, scope, scope_ref, config FROM user_vpn_configs WHERE config <> ''`,
		func(row interface{ Scan(...any) error }) error {
			var userID uuid.UUID
			var scope, ct string
			var ref uuid.NullUUID
			if err := row.Scan(&userID, &scope, &ref, &ct); err != nil {
				return err
			}
			return visit(userID, scope, ref, ct)
		})
}

// ExerciseVersions visits the variants document of every exercise version that holds a secret env value; the secrets
// are sealed inside it (topology.devices[].bootstrap.env[]).
func (r *Repository) ExerciseVersions(ctx context.Context, visit func(versionID uuid.UUID, variants []byte) error) error {
	return r.each(ctx, `SELECT id, variants FROM exercise_versions
WHERE jsonb_path_exists(variants, '$[*].topology.devices[*].bootstrap.env[*] ? (@.secret == true)')`,
		func(row interface{ Scan(...any) error }) error {
			var id uuid.UUID
			var variants []byte
			if err := row.Scan(&id, &variants); err != nil {
				return err
			}
			if err := visit(id, variants); err != nil {
				return fmt.Errorf("exercise version %s: %w", id, err)
			}
			return nil
		})
}
