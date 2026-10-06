package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cybericebox/daemon/internal/testhelpers"
)

const oldSeededFooter = `{"type": "rich_text", "content": {"root": {"type": "root", "children": [{"type": "paragraph", "children": [{"type": "text", "text": "%s", "format": 0}]}]}}}`

// TestSeededTemplateFooterMigration: at head no seeded platform template
// carries the duplicate «Автоматичне повідомлення платформи» footer, and the
// migration removes it only where the last two blocks are still exactly the
// seeded divider + line.
func TestSeededTemplateFooterMigration(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()

	var left int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM notification_email_templates WHERE body::text LIKE '%Автоматичне повідомлення%'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("seeded footer must be gone at head: %d %v", left, err)
	}

	sqlBytes, err := os.ReadFile(filepath.Join("migrations", "0121_drop_seeded_template_footer.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	const untouched, seeded, customized = "participant.invitation.accepted", "participant.invitation.sent", "participant.invitation.expired"
	old := strings.Replace(oldSeededFooter, "%s", "Cyber ICE Box · Автоматичне повідомлення платформи.", 1)
	custom := strings.Replace(oldSeededFooter, "%s", "Cyber ICE Box · Автоматичне повідомлення платформи. Наш офіс: Київ", 1)
	appendBlocks := func(typ, node string) {
		if _, err := db.Pool.Exec(ctx, `UPDATE notification_email_templates
			SET body = body || jsonb_build_array('{"type": "divider"}'::jsonb, $2::jsonb)
			WHERE notification_type = $1 AND scope_event_id IS NULL`, typ, node); err != nil {
			t.Fatal(err)
		}
	}
	bodyOf := func(typ string) string {
		var b string
		if err := db.Pool.QueryRow(ctx, `SELECT body::text FROM notification_email_templates
			WHERE notification_type = $1 AND scope_event_id IS NULL AND status = 'published'`, typ).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	wasSeeded, wasCustom, wasUntouched := bodyOf(seeded), bodyOf(customized), bodyOf(untouched)
	appendBlocks(seeded, old)
	appendBlocks(customized, custom)

	if _, err = db.Pool.Exec(ctx, string(sqlBytes)); err != nil {
		t.Fatal(err)
	}
	if got := bodyOf(seeded); got != wasSeeded {
		t.Fatalf("seeded footer must be removed:\n%s\nwant\n%s", got, wasSeeded)
	}
	if got := bodyOf(customized); got == wasCustom || !strings.Contains(got, "Наш офіс: Київ") {
		t.Fatalf("customized footer must stay: %s", got)
	}
	if got := bodyOf(untouched); got != wasUntouched {
		t.Fatalf("a template without the footer must not change: %s", got)
	}
}
