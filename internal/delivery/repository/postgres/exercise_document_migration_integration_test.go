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

// Migration 0223 turns the stored tasks and devices into the shape of cib-exercise/1.0 and runs again without
// changing anything.
func TestMigration0223ConvertsStoredVersions(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	ex := mustCreateExercise(t, exerciseRepo.New(db.Queries), "Document 1.0")
	web, db1 := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	legacy := func(flag string) string {
		return `{"id":"` + uuid.Must(uuid.NewV7()).String() + `","index":1,"tasks":[` +
			`{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"Env flag","difficulty":"easy","flag":["` + flag + `"],"linked_device_id":"` + web + `","device_flag_var":"FLAG"},` +
			`{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"Random flag","difficulty":"easy","linked_device_id":"` + db1 + `","device_flag_var":"FLAG_DB"},` +
			`{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"Plain","difficulty":"easy","flag":["ICE{a}","ICE{b}"]}],` +
			`"topology":{"vpn":{"enabled":true},"devices":[` +
			`{"id":"` + web + `","name":"web","type":"node","profile":"container","image":"nginx:1","env_vars":[{"name":"MODE","value":"ctf","secret":false},{"name":"PASS","value":"sealed","secret":true,"origin":"o1"}]},` +
			`{"id":"` + db1 + `","name":"db","type":"node","profile":"container","image":"pg:1"},` +
			`{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"sw","type":"unmanaged-switch"}]}}`
	}
	legacyVariants := `[` + legacy("ICE{one}") + `,` + legacy("ICE{two}") + `]`
	id := uuid.Must(uuid.NewV7())
	_, err := db.Pool.Exec(
		ctx,
		`INSERT INTO exercise_versions(id,exercise_id,status,variants,created_at,name,description,tags) VALUES($1,$2,'checkpoint',$3::jsonb,$4,'n','',ARRAY[]::text[])`,
		id,
		ex.ID,
		legacyVariants,
		itNow,
	)
	require.NoError(t, err)

	script, err := os.ReadFile("migrations/0223_exercise_document_1_0.up.sql")
	require.NoError(t, err)
	read := func() []map[string]any {
		var raw []byte
		require.NoError(
			t,
			db.Pool.QueryRow(ctx, `SELECT variants FROM exercise_versions WHERE id=$1`, id).
				Scan(&raw),
		)
		var out []map[string]any
		require.NoError(t, json.Unmarshal(raw, &out))
		return out
	}
	_, err = db.Pool.Exec(ctx, string(script))
	require.NoError(t, err)
	first := read()
	_, err = db.Pool.Exec(ctx, string(script))
	require.NoError(t, err)
	require.Equal(t, first, read(), "a second run changes nothing")

	for vi, variant := range first {
		tasks := variant["tasks"].([]any)
		require.Len(t, tasks, 3)
		for ti, key := range []string{"task_1", "task_2", "task_3"} {
			task := tasks[ti].(map[string]any)
			require.Equal(t, key, task["key"])
			for _, gone := range []string{"flag", "linked_device_id", "device_flag_var"} {
				require.NotContains(t, task, gone)
			}
		}
		answer := func(ti int) map[string]any {
			return tasks[ti].(map[string]any)["check"].(map[string]any)["answer"].(map[string]any)
		}
		require.Equal(t, "flag", answer(0)["type"])
		require.Equal(t, []any{[]string{"ICE{one}", "ICE{two}"}[vi]}, answer(0)["values"])
		require.NotContains(t, answer(1), "values", "a task without a flag stays generated")
		require.Equal(t, []any{"ICE{a}", "ICE{b}"}, answer(2)["values"])

		devices := variant["topology"].(map[string]any)["devices"].([]any)
		webDevice, dbDevice, sw := devices[0].(map[string]any), devices[1].(map[string]any), devices[2].(map[string]any)
		require.NotContains(t, webDevice, "env_vars")
		require.Equal(t, []any{
			map[string]any{"name": "MODE", "value": "ctf", "secret": false},
			map[string]any{"name": "PASS", "value": "sealed", "secret": true, "origin": "o1"},
			map[string]any{"name": "FLAG", "value": "{{task.task_1}}", "secret": false},
		}, webDevice["bootstrap"].(map[string]any)["env"])
		require.Equal(
			t,
			[]any{map[string]any{"name": "FLAG_DB", "value": "{{task.task_2}}", "secret": false}},
			dbDevice["bootstrap"].(map[string]any)["env"],
		)
		require.NotContains(t, sw, "bootstrap")
	}

	// The reverse brings the links back.
	down, err := os.ReadFile("migrations/0223_exercise_document_1_0.down.sql")
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, string(down))
	require.NoError(t, err)
	var linkedDevice, flagVar, plainVar, firstFlag string
	require.NoError(
		t,
		db.Pool.QueryRow(ctx, `SELECT variants#>>'{0,tasks,0,linked_device_id}', variants#>>'{0,tasks,0,device_flag_var}',
		variants#>>'{0,topology,devices,0,env_vars,0,name}', variants#>>'{0,tasks,0,flag,0}' FROM exercise_versions WHERE id=$1`, id).
			Scan(&linkedDevice, &flagVar, &plainVar, &firstFlag),
	)
	require.Equal(
		t,
		[]string{web, "FLAG", "MODE", "ICE{one}"},
		[]string{linkedDevice, flagVar, plainVar, firstFlag},
	)
}

// A version that cannot be converted stops the migration with a reason; nothing is changed silently.
func TestMigration0223FailsLoudlyOnWhatItCannotConvert(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	ex := mustCreateExercise(t, exerciseRepo.New(db.Queries), "Document 1.0 failures")
	script, err := os.ReadFile("migrations/0223_exercise_document_1_0.up.sql")
	require.NoError(t, err)
	device := uuid.Must(uuid.NewV7()).String()
	variant := func(task, envVars string) string {
		return `[{"id":"` + uuid.Must(uuid.NewV7()).
			String() +
			`","tasks":[` + task + `],"topology":{"devices":[{"id":"` + device +
			`","name":"web","type":"node","profile":"container","image":"nginx:1","env_vars":` + envVars + `}]}}]`
	}
	cases := map[string]struct{ task, env, reason string }{
		"missing device": {
			`{"name":"A task","flag":["ICE{x}"],"linked_device_id":"` + uuid.Must(uuid.NewV7()).
				String() +
				`","device_flag_var":"FLAG"}`,
			`[]`,
			"does not have",
		},
		"variable taken": {
			`{"name":"A task","flag":["ICE{x}"],"linked_device_id":"` + device + `","device_flag_var":"FLAG"}`,
			`[{"name":"FLAG","value":"x","secret":false}]`,
			"already used",
		},
		"no variable": {
			`{"name":"A task","flag":["ICE{x}"],"linked_device_id":"` + device + `"}`,
			`[]`,
			"device_flag_var is empty",
		},
		"no device": {
			`{"name":"A task","flag":["ICE{x}"],"device_flag_var":"FLAG"}`,
			`[]`,
			"no device is linked",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			id := uuid.Must(uuid.NewV7())
			_, insertErr := db.Pool.Exec(
				ctx,
				`INSERT INTO exercise_versions(id,exercise_id,status,variants,created_at,name,description,tags) VALUES($1,$2,'checkpoint',$3::jsonb,$4,'n','',ARRAY[]::text[])`,
				id,
				ex.ID,
				variant(c.task, c.env),
				itNow,
			)
			require.NoError(t, insertErr)
			t.Cleanup(
				func() { _, _ = db.Pool.Exec(ctx, `DELETE FROM exercise_versions WHERE id=$1`, id) },
			)
			_, runErr := db.Pool.Exec(ctx, string(script))
			require.ErrorContains(t, runErr, c.reason)
			require.ErrorContains(t, runErr, id.String())
			var raw []byte
			require.NoError(
				t,
				db.Pool.QueryRow(ctx, `SELECT variants FROM exercise_versions WHERE id=$1`, id).
					Scan(&raw),
			)
			require.Contains(t, string(raw), `"env_vars"`, "the stored version is untouched")
		})
	}
}
