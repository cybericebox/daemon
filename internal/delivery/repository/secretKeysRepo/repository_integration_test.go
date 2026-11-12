package secretKeysRepo_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/secretKeysRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The queries run against the real schema: every table and column the scan reads exists, the empty values are skipped and the
// nullable scope reference and the jsonb path filter work.
func TestRepositoryReadsEverySealedColumn(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	r := secretKeysRepo.New(db.Pool)

	// Empty database: every query is valid and visits nothing.
	require.NoError(t, r.SMTPPasswords(ctx, func(uuid.UUID, string) error { require.Fail(t, "visited"); return nil }))
	require.NoError(t, r.AgentKeys(ctx, func(uuid.UUID, string, string) error { require.Fail(t, "visited"); return nil }))
	require.NoError(t, r.RegistrySettings(ctx, func(string, string) error { require.Fail(t, "visited"); return nil }))
	require.NoError(t, r.EventRegistryTokens(ctx, func(uuid.UUID, string) error { require.Fail(t, "visited"); return nil }))
	require.NoError(t, r.VPNConfigs(ctx, func(uuid.UUID, string, uuid.NullUUID, string) error { require.Fail(t, "visited"); return nil }))
	require.NoError(t, r.ExerciseVersions(ctx, func(uuid.UUID, []byte) error { require.Fail(t, "visited"); return nil }))

	// The rows reference users, events and exercises; the foreign keys are not what is tested here.
	conn, err := db.Pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, execErr := conn.Exec(ctx, sql, args...)
		require.NoError(t, execErr, sql)
	}
	exec(`SET session_replication_role = replica`)
	now := time.Now().UTC()

	smtpID, agentID, eventSecretID, userID, refID, exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()),
		uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	exec(`INSERT INTO mail_smtp_configs (id, host, port, tls_mode, password_ciphertext) VALUES ($1, 'smtp.example', 587, 'starttls', 'sealed-smtp')`, smtpID)
	exec(`INSERT INTO mail_smtp_configs (id, host, port, tls_mode, password_ciphertext, scope_event_id) VALUES ($1, 'smtp2.example', 587, 'starttls', '', $2)`, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()))
	exec(`INSERT INTO infrastructure_agents (id, key, name, configured, created_at, updated_at, client_key_ciphertext, access_private_key_ciphertext)
VALUES ($1, 'a', 'a', true, $2, $2, 'sealed-client', '')`, agentID, now)
	exec(`INSERT INTO image_registry_settings (id, provider, account, repository, push_token_ciphertext, pull_token_ciphertext, updated_at)
VALUES (true, 'ghcr', 'acme', 'repo', 'sealed-push', '', $1)`, now)
	exec(`INSERT INTO event_registry_secrets (id, event_id, host, username, token_ciphertext, created_at, updated_at)
VALUES ($1, $2, 'ghcr.io', 'u', 'sealed-event', $3, $3)`, eventSecretID, uuid.Must(uuid.NewV7()), now)
	exec(`INSERT INTO user_vpn_configs (id, user_id, scope, scope_ref, config, created_at, updated_at) VALUES ($1, $2, 'event', $3, 'sealed-vpn', $4, $4)`,
		uuid.Must(uuid.NewV7()), userID, refID, now)
	exec(`INSERT INTO user_vpn_configs (id, user_id, scope, scope_ref, config, created_at, updated_at) VALUES ($1, $2, 'test', NULL, 'sealed-test', $3, $3)`,
		uuid.Must(uuid.NewV7()), userID, now)
	exec(`INSERT INTO exercises (id, name, description, tags, created_at, updated_at) VALUES ($1, 'e', '', '{}', $2, $2)`, exerciseID, now)
	exec(`INSERT INTO exercise_versions (id, exercise_id, status, name, description, tags, variants, created_at) VALUES ($1, $2, 'draft', 'n', '', '{}',
'[{"id":"v","topology":{"devices":[{"name":"web","bootstrap":{"env":[{"name":"P","value":"sealed-env","secret":true}]}}]}}]', $3)`, versionID, exerciseID, now)
	exec(`INSERT INTO exercise_versions (id, exercise_id, status, name, description, tags, variants, created_at) VALUES ($1, $2, 'draft', 'n', '', '{}',
'[{"id":"w","topology":{"devices":[{"name":"web","bootstrap":{"env":[{"name":"P","value":"plain","secret":false}]}}]}}]', $3)`, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), now)

	var smtp []string
	require.NoError(t, r.SMTPPasswords(ctx, func(id uuid.UUID, ct string) error {
		require.Equal(t, smtpID, id)
		smtp = append(smtp, ct)
		return nil
	}))
	require.Equal(t, []string{"sealed-smtp"}, smtp, "an empty password is not a value")

	require.NoError(t, r.AgentKeys(ctx, func(id uuid.UUID, clientKey, accessKey string) error {
		require.Equal(t, agentID, id)
		require.Equal(t, "sealed-client", clientKey)
		require.Empty(t, accessKey)
		return nil
	}))
	require.NoError(t, r.RegistrySettings(ctx, func(push, pull string) error {
		require.Equal(t, "sealed-push", push)
		require.Empty(t, pull)
		return nil
	}))
	require.NoError(t, r.EventRegistryTokens(ctx, func(id uuid.UUID, ct string) error {
		require.Equal(t, eventSecretID, id)
		require.Equal(t, "sealed-event", ct)
		return nil
	}))
	refs := map[string]uuid.NullUUID{}
	require.NoError(t, r.VPNConfigs(ctx, func(user uuid.UUID, scope string, ref uuid.NullUUID, ct string) error {
		require.Equal(t, userID, user)
		refs[scope] = ref
		return nil
	}))
	require.Equal(t, uuid.NullUUID{UUID: refID, Valid: true}, refs["event"])
	require.False(t, refs["test"].Valid, "a test config has no scope reference")
	versions := 0
	require.NoError(t, r.ExerciseVersions(ctx, func(id uuid.UUID, variants []byte) error {
		versions++
		require.Equal(t, versionID, id, "a version without a secret env var is not visited")
		require.Contains(t, string(variants), "sealed-env")
		return nil
	}))
	require.Equal(t, 1, versions)
}
