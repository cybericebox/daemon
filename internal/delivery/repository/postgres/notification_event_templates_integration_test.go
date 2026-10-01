package postgres_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

var eventTplNow = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

func mustCreateTemplateEvent(t *testing.T, db *testhelpers.TestDB, tag string) uuid.UUID {
	t.Helper()
	return mustCreateEvent(t, eventRepo.New(db.Queries), tag, tag, eventTplNow, eventTplNow.Add(24*time.Hour), uuid.Nil, eventTplNow).ID
}

func mustCreateEmailDraft(t *testing.T, db *testhelpers.TestDB, typ string, scope *uuid.UUID) uuid.UUID {
	t.Helper()
	return mustCreateEmailRow(t, db, typ, "draft", scope)
}

func mustCreateEmailRow(t *testing.T, db *testhelpers.TestDB, typ, status string, scope *uuid.UUID) uuid.UUID {
	t.Helper()
	params := postgres.CreateEmailTemplateParams{
		ID: uuid.Must(uuid.NewV7()), NotificationType: typ, Status: status,
		Subject: "S", Body: []byte(`[]`), Styling: []byte(`{}`),
	}
	if scope != nil {
		params.ScopeEventID = uuid.NullUUID{UUID: *scope, Valid: true}
	}
	row, err := db.Queries.CreateEmailTemplate(context.Background(), params)
	if err != nil {
		t.Fatalf("CreateEmailTemplate: %v", err)
	}
	return row.ID
}

func TestDeleteEventEmailTemplatesOfType_RemovesWholeEventFamilyOnly(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	eventID := mustCreateTemplateEvent(t, db, "tplresetemail")
	otherEventID := mustCreateTemplateEvent(t, db, "tplresetemailother")
	const typ = "participant.invitation.accepted"

	// Event family: unpublished + published + draft.
	first := mustCreateEmailRow(t, db, typ, "unpublished", &eventID)
	second := mustCreateEmailRow(t, db, typ, "published", &eventID)
	draft := mustCreateEmailDraft(t, db, typ, &eventID)
	// Untouched: another type of the Event, and another Event's row.
	otherType := mustCreateEmailDraft(t, db, "participant.team_joined", &eventID)
	otherEvent := mustCreateEmailDraft(t, db, typ, &otherEventID)

	ids, err := db.Queries.DeleteEventEmailTemplatesOfType(ctx, postgres.DeleteEventEmailTemplatesOfTypeParams{
		ScopeEventID: eventID, NotificationType: typ,
	})
	if err != nil {
		t.Fatalf("DeleteEventEmailTemplatesOfType: %v", err)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a.Bytes(), b.Bytes()) })
	want := []uuid.UUID{first, second, draft}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return slices.Compare(a.Bytes(), b.Bytes()) })
	if !slices.Equal(ids, want) {
		t.Fatalf("deleted ids = %v, want %v", ids, want)
	}

	left, err := db.Queries.ListEmailTemplates(ctx, postgres.ListEmailTemplatesParams{
		TypeFilter: typ, ScopeEventID: uuid.NullUUID{UUID: eventID, Valid: true},
	})
	if err != nil || len(left) != 0 {
		t.Fatalf("event rows of type after reset: %v err=%v", left, err)
	}
	for _, id := range []uuid.UUID{otherType, otherEvent} {
		if _, err := db.Queries.GetEmailTemplate(ctx, id); err != nil {
			t.Fatalf("row %s must survive: %v", id, err)
		}
	}
	// The platform published row the Event now falls back to is untouched.
	if _, err := db.Queries.GetPublishedEmailTemplate(ctx, postgres.GetPublishedEmailTemplateParams{NotificationType: typ}); err != nil {
		t.Fatalf("platform published row must survive: %v", err)
	}

	// Idempotent: nothing left to delete.
	ids, err = db.Queries.DeleteEventEmailTemplatesOfType(ctx, postgres.DeleteEventEmailTemplatesOfTypeParams{
		ScopeEventID: eventID, NotificationType: typ,
	})
	if err != nil || len(ids) != 0 {
		t.Fatalf("second reset: ids=%v err=%v", ids, err)
	}
}

func TestDeleteEventInAppTemplatesOfType_RemovesWholeEventFamilyOnly(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	eventID := mustCreateTemplateEvent(t, db, "tplresetinapp")
	otherEventID := mustCreateTemplateEvent(t, db, "tplresetinappother")
	const typ = "participant.invitation.accepted"

	create := func(scope uuid.UUID) uuid.UUID {
		row, err := db.Queries.CreateInAppTemplate(ctx, postgres.CreateInAppTemplateParams{
			ID: uuid.Must(uuid.NewV7()), NotificationType: typ, Status: "draft", Title: "T", Body: "B",
			Tone: "neutral", Surface: "inbox", Actions: []byte(`[]`),
			ScopeEventID: uuid.NullUUID{UUID: scope, Valid: true},
		})
		if err != nil {
			t.Fatalf("CreateInAppTemplate: %v", err)
		}
		return row.ID
	}
	published := create(eventID)
	if _, err := db.Queries.PublishInAppTemplate(ctx, postgres.PublishInAppTemplateParams{ID: published}); err != nil {
		t.Fatalf("PublishInAppTemplate: %v", err)
	}
	create(eventID) // draft
	other := create(otherEventID)

	affected, err := db.Queries.DeleteEventInAppTemplatesOfType(ctx, postgres.DeleteEventInAppTemplatesOfTypeParams{
		ScopeEventID: eventID, NotificationType: typ,
	})
	if err != nil || affected != 2 {
		t.Fatalf("DeleteEventInAppTemplatesOfType: affected=%d err=%v", affected, err)
	}
	if _, err := db.Queries.GetInAppTemplate(ctx, other); err != nil {
		t.Fatalf("other event row must survive: %v", err)
	}
}

func TestEmailTemplateFileUsableByEvent(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	eventID := mustCreateTemplateEvent(t, db, "tplimages")
	otherEventID := mustCreateTemplateEvent(t, db, "tplimagesother")

	mkFile := func(hash string) uuid.UUID {
		row, err := db.Queries.CreateFile(ctx, postgres.CreateFileParams{
			ID: uuid.Must(uuid.NewV7()), Name: "f", ContentType: "image/png",
			SizeBytes: 3, ContentHash: hash, CreatedAt: eventTplNow,
		})
		if err != nil {
			t.Fatalf("CreateFile: %v", err)
		}
		return row.ID
	}
	ref := func(refType string, refID, fileID uuid.UUID) {
		if err := db.Queries.ReplaceFileReferences(ctx, postgres.ReplaceFileReferencesParams{
			RefType: refType, RefID: refID, FileIds: []uuid.UUID{fileID},
		}); err != nil {
			t.Fatalf("ReplaceFileReferences: %v", err)
		}
	}

	platformTpl := mustCreateEmailDraft(t, db, "user.password_reset", nil)
	ownTpl := mustCreateEmailDraft(t, db, "participant.invitation.accepted", &eventID)
	otherTpl := mustCreateEmailDraft(t, db, "participant.invitation.accepted", &otherEventID)

	platformFile, presetFile, ownFile, otherFile, avatarFile, bareFile :=
		mkFile("p"), mkFile("r"), mkFile("o"), mkFile("x"), mkFile("a"), mkFile("n")
	ref(mediaModel.RefTypeEmailTemplate, platformTpl, platformFile)
	ref(mediaModel.RefTypeEmailBlockPreset, uuid.Must(uuid.NewV7()), presetFile)
	ref(mediaModel.RefTypeEmailTemplate, ownTpl, ownFile)
	ref(mediaModel.RefTypeEmailTemplate, otherTpl, otherFile)
	ref("user_avatar", uuid.Must(uuid.NewV7()), avatarFile)

	cases := []struct {
		name string
		file uuid.UUID
		want bool
	}{
		{"platform template", platformFile, true},
		{"block preset", presetFile, true},
		{"own event template", ownFile, true},
		{"other event template", otherFile, false},
		{"unrelated owner", avatarFile, false},
		{"unreferenced", bareFile, false},
	}
	for _, tc := range cases {
		got, err := db.Queries.EmailTemplateFileUsableByEvent(ctx, postgres.EmailTemplateFileUsableByEventParams{
			TemplateRefType: mediaModel.RefTypeEmailTemplate,
			PresetRefType:   mediaModel.RefTypeEmailBlockPreset,
			FileID:          tc.file,
			EventID:         eventID,
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: usable = %v, want %v", tc.name, got, tc.want)
		}
	}
}
