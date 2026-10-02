package postgres_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/model/notification/emaildefaults"
	"github.com/cybericebox/daemon/internal/model/notification/inappdefaults"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func readMigration(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("migrations", name))
	require.NoError(t, err)
	return string(raw)
}

// TestEmailTemplatesRedesignMigration: at head every seeded platform template
// equals the code-owned default; the down migration restores the previous
// rows (and the two dead ones), and running up again replaces only untouched
// rows: an admin-edited type keeps its content.
func TestEmailTemplatesRedesignMigration(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	up := readMigration(t, "0127_email_templates_redesign.up.sql")
	down := readMigration(t, "0127_email_templates_redesign.down.sql")

	type row struct {
		subject, preheader string
		body               string
	}
	load := func(typ string) (row, bool) {
		var r row
		var body []byte
		err := db.Pool.QueryRow(ctx, `SELECT subject, preheader, body FROM notification_email_templates
			WHERE notification_type = $1 AND scope_event_id IS NULL AND status = 'published'`, typ).Scan(&r.subject, &r.preheader, &body)
		if err != nil {
			return r, false
		}
		r.body = string(body)
		return r, true
	}
	requireDefaults := func(skip string) {
		for _, tpl := range emaildefaults.All(emaildefaults.UK) {
			if tpl.Type == skip || emaildefaults.StartupSeeded[tpl.Type] {
				continue
			}
			got, ok := load(tpl.Type)
			require.True(t, ok, tpl.Type)
			require.Equal(t, tpl.Subject, got.subject, tpl.Type)
			require.Equal(t, tpl.Preheader, got.preheader, tpl.Type)
			var want, have any
			require.NoError(t, json.Unmarshal(tpl.Body, &want))
			require.NoError(t, json.Unmarshal([]byte(got.body), &have))
			require.Equal(t, want, have, tpl.Type)
		}
	}

	requireDefaults("")
	for _, dead := range []string{"participant.enrolled", "participant.invitation.declined"} {
		_, ok := load(dead)
		require.False(t, ok, dead+" is deleted")
	}

	// Down: the old content is back, including the dead rows.
	_, err := db.Pool.Exec(ctx, down)
	require.NoError(t, err)
	old, ok := load("password_reset")
	require.True(t, ok)
	require.Contains(t, old.body, "Скинути пароль")
	require.NotContains(t, old.body, "facts")
	_, ok = load("participant.enrolled")
	require.True(t, ok, "the dead row is restored")

	// An admin edit (a row written by a user) is left alone by up.
	_, err = db.Pool.Exec(ctx, `UPDATE notification_email_templates SET subject = 'Мій текст'
		WHERE notification_type = 'flag_accepted' AND scope_event_id IS NULL`)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `INSERT INTO notification_email_templates (id, notification_type, status, subject, body)
		VALUES (gen_random_uuid(), 'flag_accepted', 'draft', 'Чернетка', '[]')`)
	require.NoError(t, err)

	_, err = db.Pool.Exec(ctx, up)
	require.NoError(t, err)
	kept, _ := load("flag_accepted")
	require.Equal(t, "Мій текст", kept.subject, "a type with a draft is not replaced")
	requireDefaults("flag_accepted")
}

// TestInAppTemplatesRedesignMigration: at head the seeded in-app copy equals
// the code-owned default (icon and tone untouched), down restores the old copy,
// and up leaves a type with a draft alone.
func TestInAppTemplatesRedesignMigration(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	up := readMigration(t, "0128_in_app_templates_redesign.up.sql")
	down := readMigration(t, "0128_in_app_templates_redesign.down.sql")

	load := func(typ string) (title, body, link, icon string) {
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT title, body, link, icon FROM notification_in_app_templates
			WHERE notification_type = $1 AND scope_event_id IS NULL AND status = 'published'`, typ).Scan(&title, &body, &link, &icon))
		return
	}
	requireDefaults := func(skip string) {
		for _, tpl := range inappdefaults.All(inappdefaults.UK) {
			if tpl.Type == skip || emaildefaults.StartupSeeded[tpl.Type] {
				continue
			}
			title, body, link, icon := load(tpl.Type)
			require.Equal(t, tpl.Title, title, tpl.Type)
			require.Equal(t, tpl.Body, body, tpl.Type)
			require.Equal(t, tpl.Link, link, tpl.Type)
			require.NotEmpty(t, icon, tpl.Type)
		}
	}
	requireDefaults("")

	_, err := db.Pool.Exec(ctx, down)
	require.NoError(t, err)
	title, _, _, _ := load("participant.event.finished")
	require.Equal(t, "Захід завершено", title)
	_, body, link, _ := load("participant.event.start_reminder")
	require.Contains(t, body, "(за київським часом)")
	require.Equal(t, "{{event_url}}", link, "the old link is restored")

	_, err = db.Pool.Exec(ctx, `INSERT INTO notification_in_app_templates (id, notification_type, status, title, body)
		VALUES (gen_random_uuid(), 'flag_accepted', 'draft', 'Чернетка', 'x')`)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, up)
	require.NoError(t, err)
	oldTitle, _, _, _ := load("flag_accepted")
	require.Equal(t, "Прапор зараховано", oldTitle)
	_, body, _, _ = load("flag_accepted")
	require.Contains(t, body, "Ви отримали", "a type with a draft keeps its old copy")
	requireDefaults("flag_accepted")
}
