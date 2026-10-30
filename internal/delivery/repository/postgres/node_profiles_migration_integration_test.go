package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// Migration 0207 rewrites the device type container as a node with the container profile in every stored version,
// leaves switches, hubs and the rest of the document alone, and runs again without changing anything.
func TestMigration0207TurnsContainersIntoNodes(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	ex := mustCreateExercise(t, exerciseRepo.New(db.Queries), "Node profiles")
	legacy := `[{"id":"` + uuid.Must(uuid.NewV7()).String() + `","topology":{"vpn":{"enabled":true},"devices":[` +
		`{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"web","type":"container","image":"nginx:1"},` +
		`{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"sw","type":"unmanaged-switch"}]}},{"id":"` + uuid.Must(uuid.NewV7()).String() + `"}]`
	id := uuid.Must(uuid.NewV7())
	_, err := db.Pool.Exec(ctx, `INSERT INTO exercise_versions(id,exercise_id,status,variants,created_at,name,description,tags) VALUES($1,$2,'checkpoint',$3::jsonb,$4,'n','',ARRAY[]::text[])`, id, ex.ID, legacy, itNow)
	require.NoError(t, err)

	script, err := os.ReadFile("migrations/0207_node_profiles.up.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = db.Pool.Exec(ctx, string(script))
		require.NoError(t, err)
	}

	var web, profile, image, sw string
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT variants#>>'{0,topology,devices,0,type}', variants#>>'{0,topology,devices,0,profile}',
		variants#>>'{0,topology,devices,0,image}', variants#>>'{0,topology,devices,1,type}' FROM exercise_versions WHERE id=$1`, id).Scan(&web, &profile, &image, &sw))
	require.Equal(t, []string{"node", "container", "nginx:1", "unmanaged-switch"}, []string{web, profile, image, sw})
	var vpn, variants int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT (variants#>>'{0,topology,vpn,enabled}')::boolean::int, jsonb_array_length(variants) FROM exercise_versions WHERE id=$1`, id).Scan(&vpn, &variants))
	require.Equal(t, 1, vpn)
	require.Equal(t, 2, variants)
}
