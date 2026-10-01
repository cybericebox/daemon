package postgres_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// Republishing a template (draft -> publish -> new draft -> publish) must not
// raise 23505 on the partial unique "one published per (type, scope)" index.
// The publish CTE demotes the currently-published row in a data-modifying CTE
// ("demoted") that the promoting UPDATE must depend on, or Postgres has no
// ordering guarantee between the two writable CTEs and can see two published
// rows at once even though the end state is valid.

func mustCreateInAppDraft(t *testing.T, db *testhelpers.TestDB, typ string, scope *uuid.UUID) uuid.UUID {
	t.Helper()
	params := postgres.CreateInAppTemplateParams{
		ID: uuid.Must(uuid.NewV7()), NotificationType: typ, Status: "draft",
		Title: "T", Body: "B", Tone: "neutral", Surface: "inbox", Actions: []byte(`[]`),
	}
	if scope != nil {
		params.ScopeEventID = uuid.NullUUID{UUID: *scope, Valid: true}
	}
	row, err := db.Queries.CreateInAppTemplate(context.Background(), params)
	if err != nil {
		t.Fatalf("CreateInAppTemplate: %v", err)
	}
	return row.ID
}

func scopeNullUUID(scope *uuid.UUID) uuid.NullUUID {
	if scope == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *scope, Valid: true}
}

func testRepublishEmailTemplate(t *testing.T, db *testhelpers.TestDB, scope *uuid.UUID) {
	ctx := context.Background()
	const typ = "test.republish_email"

	firstDraft := mustCreateEmailDraft(t, db, typ, scope)
	firstPublished, err := db.Queries.PublishEmailTemplate(ctx, postgres.PublishEmailTemplateParams{ID: firstDraft})
	if err != nil {
		t.Fatalf("first PublishEmailTemplate: %v", err)
	}

	secondDraft := mustCreateEmailDraft(t, db, typ, scope)
	secondPublished, err := db.Queries.PublishEmailTemplate(ctx, postgres.PublishEmailTemplateParams{ID: secondDraft})
	if err != nil {
		t.Fatalf("second PublishEmailTemplate (republish): %v", err)
	}
	if secondPublished.Status != "published" {
		t.Fatalf("second publish status = %q, want published", secondPublished.Status)
	}

	rows, err := db.Queries.ListEmailTemplates(ctx, postgres.ListEmailTemplatesParams{
		TypeFilter: typ, ScopeEventID: scopeNullUUID(scope),
	})
	if err != nil {
		t.Fatalf("ListEmailTemplates: %v", err)
	}
	publishedCount := 0
	var firstStatus string
	for _, r := range rows {
		if r.Status == "published" {
			publishedCount++
		}
		if r.ID == firstPublished.ID {
			firstStatus = r.Status
		}
	}
	if publishedCount != 1 {
		t.Fatalf("published rows = %d, want 1 (rows=%+v)", publishedCount, rows)
	}
	if firstStatus != "unpublished" {
		t.Fatalf("first published row status = %q, want unpublished", firstStatus)
	}
}

func TestPublishEmailTemplate_Republish_Platform(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	testRepublishEmailTemplate(t, db, nil)
}

func TestPublishEmailTemplate_Republish_Event(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	eventID := mustCreateTemplateEvent(t, db, "tplrepubemail")
	testRepublishEmailTemplate(t, db, &eventID)
}

func testRepublishInAppTemplate(t *testing.T, db *testhelpers.TestDB, scope *uuid.UUID) {
	ctx := context.Background()
	const typ = "test.republish_inapp"

	firstDraft := mustCreateInAppDraft(t, db, typ, scope)
	firstPublished, err := db.Queries.PublishInAppTemplate(ctx, postgres.PublishInAppTemplateParams{ID: firstDraft})
	if err != nil {
		t.Fatalf("first PublishInAppTemplate: %v", err)
	}

	secondDraft := mustCreateInAppDraft(t, db, typ, scope)
	secondPublished, err := db.Queries.PublishInAppTemplate(ctx, postgres.PublishInAppTemplateParams{ID: secondDraft})
	if err != nil {
		t.Fatalf("second PublishInAppTemplate (republish): %v", err)
	}
	if secondPublished.Status != "published" {
		t.Fatalf("second publish status = %q, want published", secondPublished.Status)
	}

	rows, err := db.Queries.ListInAppTemplates(ctx, postgres.ListInAppTemplatesParams{
		TypeFilter: typ, ScopeEventID: scopeNullUUID(scope),
	})
	if err != nil {
		t.Fatalf("ListInAppTemplates: %v", err)
	}
	publishedCount := 0
	var firstStatus string
	for _, r := range rows {
		if r.Status == "published" {
			publishedCount++
		}
		if r.ID == firstPublished.ID {
			firstStatus = r.Status
		}
	}
	if publishedCount != 1 {
		t.Fatalf("published rows = %d, want 1 (rows=%+v)", publishedCount, rows)
	}
	if firstStatus != "unpublished" {
		t.Fatalf("first published row status = %q, want unpublished", firstStatus)
	}
}

func TestPublishInAppTemplate_Republish_Platform(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	testRepublishInAppTemplate(t, db, nil)
}

func TestPublishInAppTemplate_Republish_Event(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	eventID := mustCreateTemplateEvent(t, db, "tplrepubinapp")
	testRepublishInAppTemplate(t, db, &eventID)
}
