package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cybericebox/daemon/internal/testhelpers"
)

// TestLabFailedTemplates_UseLaboratoryWording: the seeded platform texts of
// event.lab.failed say «Лабораторія не працює» and never mention a «стенд».
func TestLabFailedTemplates_UseLaboratoryWording(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	var title, body string
	if err := db.Pool.QueryRow(ctx, `SELECT title, body FROM notification_in_app_templates
		WHERE notification_type = 'event.lab.failed' AND scope_event_id IS NULL AND status = 'published'`).Scan(&title, &body); err != nil {
		t.Fatalf("in-app template: %v", err)
	}
	if title != "Лабораторія не працює" || !strings.Contains(body, "«Лабораторії»") {
		t.Fatalf("in-app wording: %q / %q", title, body)
	}
	var subject, preheader, emailBody string
	if err := db.Pool.QueryRow(ctx, `SELECT subject, preheader, body::text FROM notification_email_templates
		WHERE notification_type = 'event.lab.failed' AND scope_event_id IS NULL AND status = 'published'`).Scan(&subject, &preheader, &emailBody); err != nil {
		t.Fatalf("email template: %v", err)
	}
	if !strings.HasPrefix(subject, "Лабораторія не працює") || !strings.Contains(emailBody, "Лабораторія не працює") {
		t.Fatalf("email wording: %q / %q", subject, emailBody)
	}
	for _, text := range []string{title, body, subject, preheader, emailBody} {
		if strings.Contains(strings.ToLower(text), "стенд") {
			t.Fatalf("stand wording left: %q", text)
		}
	}
}
