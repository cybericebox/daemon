package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestMailIdentities_OnePlatformRowAndPerEventRows(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	first, err := db.Queries.UpsertPlatformMailIdentity(ctx, postgres.UpsertPlatformMailIdentityParams{
		ID: uuid.Must(uuid.NewV7()), FromName: "CyberICEBox", FromAddress: "notifications@mail.cybericebox.com",
		ReplyToName: "Support", ReplyToAddress: "support@cybericebox.com", UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.Queries.UpsertPlatformMailIdentity(ctx, postgres.UpsertPlatformMailIdentityParams{
		ID: uuid.Must(uuid.NewV7()), FromName: "CIB", UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.FromName != "CIB" || second.FromAddress != "" {
		t.Fatalf("platform upsert must replace the single row and keep its id: %+v vs %+v", first, second)
	}

	eventID := mailEvent(t, db, "identity", now.Add(time.Hour), nil)
	scope := uuid.NullUUID{UUID: eventID, Valid: true}
	if _, err = db.Queries.UpsertEventMailIdentity(ctx, postgres.UpsertEventMailIdentityParams{
		ID: uuid.Must(uuid.NewV7()), ScopeEventID: scope, ReplyToAddress: "org@uni.edu", UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Queries.GetEventMailIdentity(ctx, scope)
	if err != nil || got.ReplyToAddress != "org@uni.edu" {
		t.Fatalf("event row: %+v %v", got, err)
	}
	platform, err := db.Queries.GetPlatformMailIdentity(ctx)
	if err != nil || platform.ID != first.ID {
		t.Fatalf("platform row must not be the Event row: %+v %v", platform, err)
	}

	// The 64-character bound is enforced by the schema too.
	long := make([]rune, 65)
	for i := range long {
		long[i] = 'я'
	}
	if _, err = db.Queries.UpsertPlatformMailIdentity(ctx, postgres.UpsertPlatformMailIdentityParams{
		ID: uuid.Must(uuid.NewV7()), FromName: string(long), UpdatedAt: now,
	}); err == nil {
		t.Fatal("a 65-character sender name must be rejected")
	}
}

func TestPlatformMailFooter_SavedOnTheIdentityRowWithoutTouchingTheSender(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	by := uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}
	doc := []byte(`{"root":{"type":"root","children":[]}}`)

	// No identity yet: saving the footer creates the platform row.
	if err := db.Queries.SetPlatformMailFooter(ctx, postgres.SetPlatformMailFooterParams{
		ID: uuid.Must(uuid.NewV7()), FooterContent: doc, UpdatedBy: by, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Queries.UpsertPlatformMailIdentity(ctx, postgres.UpsertPlatformMailIdentityParams{
		ID: uuid.Must(uuid.NewV7()), FromName: "CIB", SendingDomain: "mail.example.com", UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	row, err := db.Queries.GetPlatformMailIdentity(ctx)
	if err != nil || row.FromName != "CIB" || row.SendingDomain != "mail.example.com" || len(row.FooterContent) == 0 {
		t.Fatalf("saving the sender must keep the footer: %+v %v", row, err)
	}

	if err = db.Queries.SetPlatformMailFooter(ctx, postgres.SetPlatformMailFooterParams{
		ID: uuid.Must(uuid.NewV7()), FooterContent: nil, UpdatedBy: by, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	row, err = db.Queries.GetPlatformMailIdentity(ctx)
	if err != nil || row.FromName != "CIB" || row.SendingDomain != "mail.example.com" || row.FooterContent != nil {
		t.Fatalf("saving the footer must keep the sender and the domain and allow reset: %+v %v", row, err)
	}
}

func TestPlatformMailFooter_SavingRetiresTheOlderTextFooter(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO mail_identities (id, scope_event_id, footer_text) VALUES ($1, NULL, '{site_url}')`, uuid.Must(uuid.NewV7())); err != nil {
		t.Fatal(err)
	}
	row, err := db.Queries.GetPlatformMailIdentity(ctx)
	if err != nil || row.FooterText != "{site_url}" || row.FooterContent != nil {
		t.Fatalf("a text footer must be readable next to the new column: %+v %v", row, err)
	}
	if err = db.Queries.SetPlatformMailFooter(ctx, postgres.SetPlatformMailFooterParams{
		ID: uuid.Must(uuid.NewV7()), FooterContent: []byte(`{"root":{"type":"root","children":[]}}`), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	row, err = db.Queries.GetPlatformMailIdentity(ctx)
	if err != nil || row.FooterText != "" || len(row.FooterContent) == 0 {
		t.Fatalf("the document replaces the text footer: %+v %v", row, err)
	}
}
