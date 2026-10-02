package startupdefaults

import (
	"context"
	"testing"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The email_changed type has no migration: its setting and default template are created at start,
// once, and an admin's edit survives the next start.
func TestSeedNotificationDefaultsIsIdempotent(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := Seed(ctx, db.Queries); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	setting, err := db.Queries.GetNotificationSetting(ctx, postgres.GetNotificationSettingParams{NotificationType: "email_changed", Channel: "email"})
	if err != nil || !setting.Enabled || setting.UserCanChange {
		t.Fatalf("setting %+v, %v: a security notice is always on and not user-changeable", setting, err)
	}
	var n int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM notification_email_templates WHERE notification_type = 'email_changed'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("templates = %d, %v; want exactly one", n, err)
	}
	tpl, err := db.Queries.GetPublishedEmailTemplate(ctx, postgres.GetPublishedEmailTemplateParams{NotificationType: "email_changed"})
	if err != nil || tpl.Subject == "" || len(tpl.Body) == 0 {
		t.Fatalf("published template %+v, %v", tpl, err)
	}

	// An admin edit (new published version) is left alone.
	if _, err = db.Pool.Exec(ctx, `UPDATE notification_email_templates SET subject = 'Мій текст' WHERE notification_type = 'email_changed'`); err != nil {
		t.Fatal(err)
	}
	if err = Seed(ctx, db.Queries); err != nil {
		t.Fatal(err)
	}
	if tpl, _ = db.Queries.GetPublishedEmailTemplate(ctx, postgres.GetPublishedEmailTemplateParams{NotificationType: "email_changed"}); tpl.Subject != "Мій текст" {
		t.Fatalf("an edited template was overwritten: %q", tpl.Subject)
	}
}
