package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// mailEvent creates an Event with a configured lifecycle: published at
// start-2d, finish/withdraw optional.
func mailEvent(t *testing.T, db *testhelpers.TestDB, tag string, startAt time.Time, finishAt *time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	id := mustCreateEvent(t, eventRepo.New(db.Queries), tag, tag, now.Add(-72*time.Hour), now.Add(72*time.Hour), uuid.Nil, now).ID
	var finish, withdraw any
	if finishAt != nil {
		finish, withdraw = *finishAt, finishAt.Add(48*time.Hour)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE events SET lifecycle_configured = true, publish_at = $2, start_at = $3,
		finish_at = $4, withdraw_at = $5 WHERE id = $1`, id, startAt.Add(-48*time.Hour), startAt, finish, withdraw); err != nil {
		t.Fatalf("schedule %s: %v", tag, err)
	}
	return id
}

func mailUser(t *testing.T, db *testhelpers.TestDB, email string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := db.Queries.CreateUser(context.Background(), postgres.CreateUserParams{
		ID: id, Email: email, Role: string(rbac.RoleUser), Status: string(userModel.UserStatusActive),
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return id
}

func TestSMTPConfigs_PlatformProvidersAndPerEventRows(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	first := insertProvider(t, db, "Brevo", "smtp-relay.brevo.com", now)
	second := insertProvider(t, db, "SES", "email-smtp.eu-north-1.amazonaws.com", now)
	if first.Priority != 0 || second.Priority != 1 {
		t.Fatalf("a new provider goes last: %d, %d", first.Priority, second.Priority)
	}
	eventID := mailEvent(t, db, "smtpevent", now.Add(time.Hour), nil)
	if _, err := db.Queries.UpsertEventSMTPConfig(ctx, postgres.UpsertEventSMTPConfigParams{
		ID: uuid.Must(uuid.NewV7()), ScopeEventID: uuid.NullUUID{UUID: eventID, Valid: true},
		Host: "smtp.uni.edu", Port: 587, TlsMode: "starttls", UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Queries.GetEventSMTPConfig(ctx, uuid.NullUUID{UUID: eventID, Valid: true})
	if err != nil || got.Host != "smtp.uni.edu" {
		t.Fatalf("event row: %+v %v", got, err)
	}
	list, err := db.Queries.ListPlatformSMTPProviders(ctx)
	if err != nil || len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("the platform list holds only the platform providers, by priority: %+v %v", list, err)
	}
	if n, err := db.Queries.DeletePlatformSMTPProvider(ctx, first.ID); err != nil || n != 1 {
		t.Fatalf("delete: %d %v", n, err)
	}
	if _, err = db.Queries.GetEventSMTPConfig(ctx, uuid.NullUUID{UUID: eventID, Valid: true}); err != nil {
		t.Fatalf("deleting a platform provider must keep Event rows: %v", err)
	}
	if n, _ := db.Queries.DeletePlatformSMTPProvider(ctx, got.ID); n != 0 {
		t.Fatal("an Event row is not a platform provider and must not be deleted through the platform query")
	}
}

func TestEventMailNotices_DueOnceAndRearmedByNewStart(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	soon := mailEvent(t, db, "soon", now.Add(10*time.Hour), nil)
	later := mailEvent(t, db, "later", now.Add(8*24*time.Hour), nil)

	due, err := db.Queries.ListEventsDueStartReminder(ctx, now)
	if err != nil || len(due) != 1 || due[0].ID != soon || due[0].DaysBeforeStart != 7 {
		t.Fatalf("due reminders: %+v %v", due, err)
	}
	if err = db.Queries.MarkEventStartReminderSent(ctx, postgres.MarkEventStartReminderSentParams{
		EventID: soon, StartReminderSentFor: pgtype.Timestamptz{Time: due[0].StartAt, Valid: true}, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if due, _ = db.Queries.ListEventsDueStartReminder(ctx, now); len(due) != 0 {
		t.Fatalf("reminder must be sent once: %+v", due)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE events SET start_at = start_at + interval '1 hour' WHERE id = $1`, soon); err != nil {
		t.Fatal(err)
	}
	if due, _ = db.Queries.ListEventsDueStartReminder(ctx, now); len(due) != 1 {
		t.Fatalf("a moved start re-arms the reminder: %+v", due)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE events SET publish_at = $2 WHERE id = $1`, later, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	// The lead time is the days_before_start option of the email subscription.
	config := []byte(`{"days_before_start":9}`)
	if _, err = db.Queries.UpsertEventSignalNotificationSubscription(ctx, postgres.UpsertEventSignalNotificationSubscriptionParams{
		ScopeEventID: later, SignalType: "participant.event.start_reminder", Channel: "email", Enabled: true,
		Audience: []byte(`{"kind":"all_participants"}`), Config: config,
	}); err != nil {
		t.Fatal(err)
	}
	if due, _ = db.Queries.ListEventsDueStartReminder(ctx, now); len(due) != 2 {
		t.Fatalf("a longer lead time opens the window: %+v", due)
	}
	// Switching both channels off stops the reminder.
	for _, channel := range []string{"email", "in_app"} {
		if _, err = db.Queries.UpsertEventSignalNotificationSubscription(ctx, postgres.UpsertEventSignalNotificationSubscriptionParams{
			ScopeEventID: soon, SignalType: "participant.event.start_reminder", Channel: channel, Enabled: false,
			Audience: []byte(`{"kind":"all_participants"}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if due, _ = db.Queries.ListEventsDueStartReminder(ctx, now); len(due) != 1 || due[0].ID != later {
		t.Fatalf("reminder switched off: %+v", due)
	}

	finishedAt := now.Add(-time.Hour)
	finished := mailEvent(t, db, "finished", now.Add(-5*time.Hour), &finishedAt)
	oldFinish := now.Add(-72 * time.Hour)
	mailEvent(t, db, "oldfinish", now.Add(-80*time.Hour), &oldFinish)
	notices, err := db.Queries.ListEventsDueFinishedNotice(ctx, pgtype.Timestamptz{Time: now, Valid: true})
	if err != nil || len(notices) != 1 || notices[0].ID != finished || !notices[0].FinishedAt.Equal(finishedAt) {
		t.Fatalf("finished notices (old finishes are not backfilled): %+v %v", notices, err)
	}
	if err = db.Queries.MarkEventFinishedNotified(ctx, postgres.MarkEventFinishedNotifiedParams{
		EventID: finished, FinishedNotifiedFor: pgtype.Timestamptz{Time: finishedAt, Valid: true}, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if notices, _ = db.Queries.ListEventsDueFinishedNotice(ctx, pgtype.Timestamptz{Time: now, Valid: true}); len(notices) != 0 {
		t.Fatalf("finish notice must be sent once: %+v", notices)
	}
}

func TestInvitationExpiryCandidates_ClaimedOnce(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	started := mailEvent(t, db, "started", now.Add(-time.Hour), nil)
	upcoming := mailEvent(t, db, "upcoming", now.Add(time.Hour), nil)
	invitee := mailUser(t, db, "invitee@example.org")
	for _, eventID := range []uuid.UUID{started, upcoming} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO event_participants (event_id, user_id, status, invited, invited_to_team, created_at)
			VALUES ($1, $2, 1, true, true, now())`, eventID, invitee); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Queries.ListInvitationExpiryCandidates(ctx, now)
	if err != nil || len(rows) != 1 || rows[0].EventID != started || !rows[0].InvitedToTeam {
		t.Fatalf("candidates of started events only: %+v %v", rows, err)
	}
	claimed, err := db.Queries.MarkInvitationExpiredNotified(ctx, postgres.MarkInvitationExpiredNotifiedParams{
		EventID: started, UserID: invitee, InvitationExpiredNotifiedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil || claimed != 1 {
		t.Fatalf("claim: %d %v", claimed, err)
	}
	claimed, _ = db.Queries.MarkInvitationExpiredNotified(ctx, postgres.MarkInvitationExpiredNotifiedParams{
		EventID: started, UserID: invitee, InvitationExpiredNotifiedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if claimed != 0 {
		t.Fatal("second claim must affect nothing")
	}
	if rows, _ = db.Queries.ListInvitationExpiryCandidates(ctx, now); len(rows) != 0 {
		t.Fatalf("claimed invitation must leave the candidates: %+v", rows)
	}
}

func TestJournalAndInbox_EventScope(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	eventID := mailEvent(t, db, "journal", now.Add(time.Hour), nil)
	otherID := mailEvent(t, db, "other", now.Add(time.Hour), nil)
	user := mailUser(t, db, "p@example.org")

	eventDispatch, platformDispatch := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, d := range []struct {
		id    uuid.UUID
		scope uuid.NullUUID
	}{{eventDispatch, uuid.NullUUID{UUID: eventID, Valid: true}}, {platformDispatch, uuid.NullUUID{}}} {
		if _, err := db.Queries.CreateDispatch(ctx, postgres.CreateDispatchParams{
			ID: d.id, NotificationType: "participant.event.start_reminder", RecipientUserID: user, Status: "done", ScopeEventID: d.scope,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Queries.UpsertDispatchTarget(ctx, postgres.UpsertDispatchTargetParams{
		DispatchID: eventDispatch, Channel: "email", Status: "done", Attempts: 2, Transport: "platform",
		Recipient: "p@example.org", FallbackError: "535 auth",
	}); err != nil {
		t.Fatal(err)
	}
	list := func(p postgres.ListDispatchesParams) []postgres.ListDispatchesRow {
		t.Helper()
		p.LimitVal = 10
		rows, err := db.Queries.ListDispatches(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	if rows := list(postgres.ListDispatchesParams{EventFilter: eventID.String()}); len(rows) != 1 || rows[0].EventName != "journal" || rows[0].RecipientEmail != "p@example.org" {
		t.Fatalf("event journal: %+v", rows)
	}
	if rows := list(postgres.ListDispatchesParams{ChannelFilter: "email", TransportFilter: "platform", ResultFilter: "done"}); len(rows) != 1 || rows[0].ID != eventDispatch {
		t.Fatalf("target filters: %+v", rows)
	}
	if rows := list(postgres.ListDispatchesParams{TransportFilter: "env,platform"}); len(rows) != 1 || rows[0].ID != eventDispatch {
		t.Fatalf("a comma-separated transport filter matches any of its values: %+v", rows)
	}
	if rows := list(postgres.ListDispatchesParams{TransportFilter: "event"}); len(rows) != 0 {
		t.Fatalf("transport filter: %+v", rows)
	}
	if rows := list(postgres.ListDispatchesParams{}); len(rows) != 2 {
		t.Fatalf("no filter: %+v", rows)
	}
	targets, err := db.Queries.ListDispatchTargetsByDispatches(ctx, []uuid.UUID{eventDispatch, platformDispatch})
	if err != nil || len(targets) != 1 || targets[0].Attempts != 2 || targets[0].FallbackError != "535 auth" {
		t.Fatalf("targets: %+v %v", targets, err)
	}

	for _, scope := range []uuid.NullUUID{{UUID: eventID, Valid: true}, {UUID: otherID, Valid: true}, {}} {
		if err := db.Queries.CreateInApp(ctx, postgres.CreateInAppParams{
			ID: uuid.Must(uuid.NewV7()), UserID: user, Title: "t", Surface: "inbox", Actions: []byte(`[]`), Dismissible: true, ScopeEventID: scope,
		}); err != nil {
			t.Fatal(err)
		}
	}
	inbox := func(filter string) []postgres.ListInAppByUserRow {
		t.Helper()
		rows, err := db.Queries.ListInAppByUser(ctx, postgres.ListInAppByUserParams{
			UserID: user, EventFilter: filter, BeforeCreatedAt: now.Add(time.Hour), BeforeID: uuid.FromStringOrNil("ffffffff-ffff-ffff-ffff-ffffffffffff"),
		})
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	if rows := inbox(""); len(rows) != 3 {
		t.Fatalf("platform-wide inbox lists everything: %d", len(rows))
	}
	rows := inbox(eventID.String())
	if len(rows) != 2 {
		t.Fatalf("event inbox = this event + no-event items: %d", len(rows))
	}
	for _, row := range rows {
		if row.ScopeEventID.Valid && (row.ScopeEventID.UUID != eventID || row.EventName != "journal") {
			t.Fatalf("foreign event item leaked: %+v", row)
		}
	}
	platformOnly := inbox(uuid.Nil.String())
	if len(platformOnly) != 1 || platformOnly[0].ScopeEventID.Valid {
		t.Fatalf("platform-only inbox lists only no-event items: %+v", platformOnly)
	}
	if unread, err := db.Queries.CountUnreadInbox(ctx, postgres.CountUnreadInboxParams{UserID: user, EventFilter: uuid.Nil.String()}); err != nil || unread != 1 {
		t.Fatalf("unread in platform-only scope: %d %v", unread, err)
	}
	unread, err := db.Queries.CountUnreadInbox(ctx, postgres.CountUnreadInboxParams{UserID: user, EventFilter: otherID.String()})
	if err != nil || unread != 2 {
		t.Fatalf("unread in other event scope: %d %v", unread, err)
	}
	if err = db.Queries.MarkAllInAppReadByUser(ctx, postgres.MarkAllInAppReadByUserParams{UserID: user, EventFilter: eventID.String()}); err != nil {
		t.Fatal(err)
	}
	if unread, _ = db.Queries.CountUnreadInbox(ctx, postgres.CountUnreadInboxParams{UserID: user}); unread != 1 {
		t.Fatalf("read-all on an Event site leaves other Events unread: %d", unread)
	}
}
