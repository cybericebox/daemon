package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/broadcastRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func btInput(name string) broadcastModel.TemplateInput {
	return broadcastModel.TemplateInput{Name: name, Content: broadcastModel.Content{
		Channels: []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail, notificationTypes.NotificationChannelInApp},
		Subject:  "Hello {{.user_name}}", Preheader: "Soon", EmailBody: json.RawMessage(`[{"type":"rich_text","html":"<p>Hi</p>"}]`),
		EmailStyling: json.RawMessage(`{"accent":"#123456"}`), InAppTitle: "Hello", InAppBody: "<p>Body</p>", InAppLink: "/news",
	}}
}

// Templates live in their scope: the platform and every Event see only their own, and a write in the wrong scope
// touches nothing.
func TestBroadcastTemplates_ScopesAreIsolated(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := broadcastRepo.NewTemplates(db.Queries)
	actor := mustSeedUser(t, db, "bt-author@test.test")
	event := mustSeedEventForParticipants(t, db, "btevent").ID
	other := mustSeedEventForParticipants(t, db, "btother").ID

	platformID, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if err := repo.CreateTemplate(ctx, platformID, nil, actor, btInput("Reminder")); err != nil {
		t.Fatal(err)
	}
	// The same name in another scope is fine.
	if err := repo.CreateTemplate(ctx, eventID, &event, actor, btInput("Reminder")); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetTemplate(ctx, eventID, &event)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Reminder" || got.ScopeEventID == nil || *got.ScopeEventID != event || got.Content.Subject != "Hello {{.user_name}}" ||
		got.Content.InAppLink != "/news" || got.UpdatedBy == nil || *got.UpdatedBy != actor || len(got.Content.Channels) != 2 {
		t.Fatalf("stored template = %+v", got)
	}
	var styling map[string]string
	if err = json.Unmarshal(got.Content.EmailStyling, &styling); err != nil || styling["accent"] != "#123456" {
		t.Fatalf("styling = %s", got.Content.EmailStyling)
	}

	platform, err := repo.ListTemplates(ctx, nil, 50)
	if err != nil || len(platform) != 1 || platform[0].ID != platformID {
		t.Fatalf("platform list = %+v, %v", platform, err)
	}
	if list, err := repo.ListTemplates(ctx, &other, 50); err != nil || len(list) != 0 {
		t.Fatalf("other event sees %+v, %v", list, err)
	}

	if _, err = repo.GetTemplate(ctx, eventID, &other); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("another event reads the template: %v", err)
	}
	if _, err = repo.GetTemplate(ctx, eventID, nil); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("the platform scope reads an event template: %v", err)
	}
	if err = repo.UpdateTemplate(ctx, eventID, &other, actor, btInput("Taken over")); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("update from another event = %v", err)
	}
	if err = repo.DeleteTemplate(ctx, platformID, &event); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("an event deletes a platform template: %v", err)
	}

	edited := btInput("Reminder, edited")
	edited.Content.InAppTitle = "Changed"
	if err = repo.UpdateTemplate(ctx, eventID, &event, actor, edited); err != nil {
		t.Fatal(err)
	}
	if got, err = repo.GetTemplate(ctx, eventID, &event); err != nil || got.Name != "Reminder, edited" || got.Content.InAppTitle != "Changed" || got.UpdatedAt.Before(got.CreatedAt) {
		t.Fatalf("updated = %+v, %v", got, err)
	}
	if err = repo.DeleteTemplate(ctx, platformID, nil); err != nil {
		t.Fatal(err)
	}
	if list, _ := repo.ListTemplates(ctx, nil, 50); len(list) != 0 {
		t.Fatalf("deleted template still listed: %+v", list)
	}
}

// One name per scope, regardless of case and surrounding spaces.
func TestBroadcastTemplates_NameIsUniqueInItsScope(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := broadcastRepo.NewTemplates(db.Queries)
	actor := mustSeedUser(t, db, "bt-unique@test.test")
	if err := repo.CreateTemplate(ctx, uuid.Must(uuid.NewV7()), nil, actor, btInput("Reminder")); err != nil {
		t.Fatal(err)
	}
	err := repo.CreateTemplate(ctx, uuid.Must(uuid.NewV7()), nil, actor, btInput("reminder "))
	if _, ok := repositoryTools.UniqueViolationError(err, notificationModel.ErrBroadcastTemplateNameTaken); !ok {
		t.Fatalf("duplicate name = %v, want a unique violation", err)
	}
}

// An Event's templates go with the Event.
func TestBroadcastTemplates_DeletedWithTheirEvent(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := broadcastRepo.NewTemplates(db.Queries)
	actor := mustSeedUser(t, db, "bt-cascade@test.test")
	event := mustSeedEventForParticipants(t, db, "btcascade").ID
	id := uuid.Must(uuid.NewV7())
	if err := repo.CreateTemplate(ctx, id, &event, actor, btInput("Reminder")); err != nil {
		t.Fatal(err)
	}
	rtExec(t, db, `DELETE FROM events WHERE id = $1`, event)
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM notification_broadcast_templates WHERE id = $1`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("template left after its event: %d, %v", n, err)
	}
}
