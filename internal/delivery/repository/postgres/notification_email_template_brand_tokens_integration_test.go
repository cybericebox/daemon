package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// TestEmailTemplateBrandTokensMigrationSeedsLogoAndTokens migrates a fresh
// database to head (through 0068) and asserts that the seeded platform
// (scope_event_id IS NULL) published "participant.invitation.accepted" email template
// leads with a logo block and uses the shared brand colour tokens instead of
// the hardcoded hexes 0062 originally seeded.
func TestEmailTemplateBrandTokensMigrationSeedsLogoAndTokens(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()

	tpl, err := db.Queries.GetPublishedEmailTemplate(ctx, postgres.GetPublishedEmailTemplateParams{
		NotificationType: "participant.invitation.accepted",
		ScopeEventID:     uuid.NullUUID{Valid: false},
	})
	if err != nil {
		t.Fatalf("GetPublishedEmailTemplate: %v", err)
	}
	if tpl.ScopeEventID.Valid {
		t.Fatalf("expected the platform template (scope_event_id NULL), got scope_event_id=%v", tpl.ScopeEventID.UUID)
	}

	var body []map[string]any
	if err := json.Unmarshal(tpl.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("body is empty, expected a leading logo block")
	}
	if body[0]["type"] != "logo" {
		t.Fatalf("expected body[0].type = \"logo\", got %+v", body[0])
	}
	if body[0]["align"] != "left" {
		t.Fatalf("expected logo align = \"left\", got %+v", body[0]["align"])
	}
	if width, ok := body[0]["width_px"].(float64); !ok || width != 44 {
		t.Fatalf("expected logo width_px = 44, got %+v", body[0]["width_px"])
	}
	// Idempotence guard: only one logo block should ever be present.
	logoCount := 0
	for _, b := range body {
		if b["type"] == "logo" {
			logoCount++
		}
	}
	if logoCount != 1 {
		t.Fatalf("expected exactly one logo block, found %d", logoCount)
	}

	var styling map[string]string
	if err := json.Unmarshal(tpl.Styling, &styling); err != nil {
		t.Fatalf("unmarshal styling: %v", err)
	}
	// The redesign leaves styling empty: the renderer defaults apply (brand
	// tokens for the button), and no hardcoded brand hex is stored.
	for key, want := range map[string]string{"cta_bg_color": "theme:accent", "cta_text_color": "theme:on_accent"} {
		if v := styling[key]; v != "" && v != want {
			t.Fatalf("expected %s empty or %q, got %q", key, want, v)
		}
	}
}

// TestEmailTemplateBrandTokensMigrationCoversAllPlatformTemplates asserts the
// reseed touched every platform draft/published email template, not just
// participant.invitation.accepted: no remaining old brand hex, and exactly one leading
// logo block each.
func TestEmailTemplateBrandTokensMigrationCoversAllPlatformTemplates(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()

	rows, err := db.Queries.ListEmailTemplates(ctx, postgres.ListEmailTemplatesParams{
		ScopeEventID: uuid.NullUUID{Valid: false},
	})
	if err != nil {
		t.Fatalf("ListEmailTemplates: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one seeded platform email template")
	}

	for _, tpl := range rows {
		var styling map[string]string
		if err := json.Unmarshal(tpl.Styling, &styling); err != nil {
			t.Fatalf("%s: unmarshal styling: %v", tpl.NotificationType, err)
		}
		for _, key := range []string{"heading_color", "cta_bg_color", "cta_text_color"} {
			v := styling[key]
			switch v {
			case "#221b54", "#292841", "#211a52", "#ffffff", "#FFFFFF":
				t.Fatalf("%s: styling[%s] still holds an old hex: %q", tpl.NotificationType, key, v)
			}
		}

		var body []map[string]any
		if err := json.Unmarshal(tpl.Body, &body); err != nil {
			t.Fatalf("%s: unmarshal body: %v", tpl.NotificationType, err)
		}
		if len(body) == 0 || body[0]["type"] != "logo" {
			t.Fatalf("%s: expected a leading logo block, got body=%+v", tpl.NotificationType, body)
		}
	}
}
