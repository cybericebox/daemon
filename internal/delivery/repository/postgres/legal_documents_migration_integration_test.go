package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/testhelpers"
)

// TestLegalDocumentsMigration: the down migration brings the version number back (1 for an accepted row), and running
// up again gives every row with an acceptance time both documents, leaves the others empty and drops the version.
func TestLegalDocumentsMigration(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	up := readMigration(t, "0231_legal_documents.up.sql")
	down := readMigration(t, "0231_legal_documents.down.sql")

	columns := func() map[string]bool {
		rows, err := db.Pool.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_name = 'users'`)
		require.NoError(t, err)
		defer rows.Close()
		out := map[string]bool{}
		for rows.Next() {
			var name string
			require.NoError(t, rows.Scan(&name))
			out[name] = true
		}
		require.NoError(t, rows.Err())
		return out
	}
	have := columns()
	require.True(t, have["legal_documents"])
	require.False(t, have["tos_version"])

	_, err := db.Pool.Exec(ctx, down)
	require.NoError(t, err)
	have = columns()
	require.False(t, have["legal_documents"])
	require.True(t, have["tos_version"])

	insert := func(email, acceptedAt string) {
		_, execErr := db.Pool.Exec(ctx, `INSERT INTO users (id, email, first_name, last_name, role, status, email_confirmed, tos_accepted_at, tos_version, updated_at, created_at)
			VALUES (gen_random_uuid(), $1, 'A', 'B', 'user', 'active', true, `+acceptedAt+`, `+map[bool]string{true: "1", false: "NULL"}[acceptedAt != "NULL"]+`, now(), now())`, email)
		require.NoError(t, execErr)
	}
	insert("accepted@x.test", "now()")
	insert("never@x.test", "NULL")

	_, err = db.Pool.Exec(ctx, up)
	require.NoError(t, err)
	have = columns()
	require.True(t, have["legal_documents"])
	require.False(t, have["tos_version"])

	docs := func(email string) []string {
		var out []string
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT legal_documents FROM users WHERE email = $1`, email).Scan(&out))
		return out
	}
	require.Equal(t, []string{"terms", "privacy"}, docs("accepted@x.test"))
	require.Empty(t, docs("never@x.test"))
}
