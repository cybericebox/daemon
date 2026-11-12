package postgres_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// Migration 0225 turns external and terminal of a stored device into the access list, runs again without changing
// anything, and the reverse brings the old keys back.
func TestMigration0225ConvertsDeviceAccess(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	ex := mustCreateExercise(t, exerciseRepo.New(db.Queries), "Device access")
	stored := `[{"id":"` + uuid.Must(uuid.NewV7()).String() + `","tasks":[],"topology":{"devices":[` +
		`{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"web","type":"node","image":"nginx:1","external":{"port":8080,"protocol":"https"},"terminal":{"enabled":true,"command":"/bin/bash","user":"www"}},` +
		`{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"off","type":"node","image":"pg:1","terminal":{"enabled":false}},` +
		`{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"plain","type":"node","image":"pg:1"},` +
		`{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"sw","type":"unmanaged-switch"}]}}]`
	id := uuid.Must(uuid.NewV7())
	_, err := db.Pool.Exec(
		ctx,
		`INSERT INTO exercise_versions(id,exercise_id,status,variants,created_at,name,description,tags) VALUES($1,$2,'checkpoint',$3::jsonb,$4,'n','',ARRAY[]::text[])`,
		id, ex.ID, stored, itNow,
	)
	require.NoError(t, err)
	up, err := os.ReadFile("migrations/0225_device_access_list.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("migrations/0225_device_access_list.down.sql")
	require.NoError(t, err)
	read := func() []any {
		var raw []byte
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT variants#>'{0,topology,devices}' FROM exercise_versions WHERE id=$1`, id).Scan(&raw))
		var out []any
		require.NoError(t, json.Unmarshal(raw, &out))
		return out
	}
	_, err = db.Pool.Exec(ctx, string(up))
	require.NoError(t, err)
	first := read()
	_, err = db.Pool.Exec(ctx, string(up))
	require.NoError(t, err)
	require.Equal(t, first, read(), "a second run changes nothing")

	web := first[0].(map[string]any)
	require.NotContains(t, web, "external")
	require.NotContains(t, web, "terminal")
	require.Equal(t, []any{
		map[string]any{"kind": "http", "port": float64(8080), "protocol": "https"},
		map[string]any{"kind": "terminal", "command": "/bin/bash", "user": "www"},
	}, web["access"])
	require.NotContains(t, first[1].(map[string]any), "terminal", "a terminal that was off leaves nothing")
	require.NotContains(t, first[1].(map[string]any), "access")
	require.NotContains(t, first[2].(map[string]any), "access")

	_, err = db.Pool.Exec(ctx, string(down))
	require.NoError(t, err)
	back := read()[0].(map[string]any)
	require.NotContains(t, back, "access")
	require.Equal(t, map[string]any{"port": float64(8080), "protocol": "https"}, back["external"])
	require.Equal(t, map[string]any{"enabled": true, "command": "/bin/bash", "user": "www"}, back["terminal"])
}

// A device with both the access list and an old key is ambiguous: the migration stops with the version; vnc and rdp stop the
// reverse. Nothing is changed silently.
func TestMigration0225FailsLoudlyOnWhatItCannotConvert(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	ex := mustCreateExercise(t, exerciseRepo.New(db.Queries), "Device access failures")
	up, err := os.ReadFile("migrations/0225_device_access_list.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("migrations/0225_device_access_list.down.sql")
	require.NoError(t, err)
	insert := func(devices string) uuid.UUID {
		id := uuid.Must(uuid.NewV7())
		_, insertErr := db.Pool.Exec(
			ctx,
			`INSERT INTO exercise_versions(id,exercise_id,status,variants,created_at,name,description,tags) VALUES($1,$2,'checkpoint',$3::jsonb,$4,'n','',ARRAY[]::text[])`,
			id, ex.ID, `[{"id":"`+uuid.Must(uuid.NewV7()).String()+`","tasks":[],"topology":{"devices":[`+devices+`]}}]`, itNow,
		)
		require.NoError(t, insertErr)
		t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `DELETE FROM exercise_versions WHERE id=$1`, id) })
		return id
	}
	both := insert(`{"name":"web","type":"node","external":{"port":80},"access":[{"kind":"http","port":80}]}`)
	_, err = db.Pool.Exec(ctx, string(up))
	require.ErrorContains(t, err, "has an access list")
	require.ErrorContains(t, err, both.String())

	_, err = db.Pool.Exec(ctx, `DELETE FROM exercise_versions WHERE id=$1`, both)
	require.NoError(t, err)
	vnc := insert(`{"name":"web","type":"node","access":[{"kind":"vnc","port":5900}]}`)
	_, err = db.Pool.Exec(ctx, string(down))
	require.ErrorContains(t, err, "vnc or rdp")
	require.ErrorContains(t, err, vnc.String())
}
