package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

var subsNow = time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)

func mustCreateSubscriptionEvent(t *testing.T, db *testhelpers.TestDB, tag string) uuid.UUID {
	t.Helper()
	return mustCreateEvent(t, eventRepo.New(db.Queries), tag, tag, subsNow, subsNow.Add(24*time.Hour), uuid.Nil, subsNow).ID
}

func TestNotificationDefaultsAfterMailMigration(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	var total, enabled int
	err := db.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE enabled)
		FROM platform_signal_notification_defaults WHERE signal_type LIKE 'participant.%'`).Scan(&total, &enabled)
	if err != nil {
		t.Fatal(err)
	}
	// participant.enrolled has no notification of its own (0078); declined
	// left the Event scope and start reminder / finished were added (0083).
	// Enabled by default (0083): application submitted/approved/rejected
	// (M4), invitation revoked/expired, start reminder, finished; results
	// published in-app only (0092).
	if total != 24 || enabled != 15 {
		t.Fatalf("expected 24 participant defaults (12 signals x 2 channels), 15 enabled, got total=%d enabled=%d", total, enabled)
	}
	var enrolled int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM platform_signal_notification_defaults
		WHERE signal_type = 'participant.enrolled'`).Scan(&enrolled); err != nil {
		t.Fatal(err)
	}
	if enrolled != 0 {
		t.Fatalf("expected no participant.enrolled defaults, got %d", enrolled)
	}
}

func TestEffectiveEnabledSubscriptions_InheritPlatformDefaultWithoutOverride(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	eventID := mustCreateSubscriptionEvent(t, db, "inherit")
	if _, err := db.Queries.UpsertPlatformSignalNotificationDefault(ctx, postgres.UpsertPlatformSignalNotificationDefaultParams{
		SignalType: "participant.open_registration.completed", Channel: "email", Enabled: true, Audience: []byte(`{"kind":"all_participants"}`),
	}); err != nil {
		t.Fatalf("upsert platform default: %v", err)
	}

	rows, err := db.Queries.ListEffectiveEnabledSignalNotificationSubscriptions(ctx, postgres.ListEffectiveEnabledSignalNotificationSubscriptionsParams{
		ScopeEventID: eventID, SignalType: "participant.open_registration.completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Channel != "email" || string(rows[0].Audience) != `{"kind": "all_participants"}` {
		t.Fatalf("expected inherited enabled email default, got %+v", rows)
	}
}

func TestEffectiveEnabledSubscriptions_EventOverrideDisablesPlatformDefault(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	eventID := mustCreateSubscriptionEvent(t, db, "override")
	otherID := mustCreateSubscriptionEvent(t, db, "untouched")
	if _, err := db.Queries.UpsertPlatformSignalNotificationDefault(ctx, postgres.UpsertPlatformSignalNotificationDefaultParams{
		SignalType: "participant.open_registration.completed", Channel: "in_app", Enabled: true, Audience: []byte(`{"kind":"signal_subject"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Queries.UpsertEventSignalNotificationSubscription(ctx, postgres.UpsertEventSignalNotificationSubscriptionParams{
		ScopeEventID: eventID, SignalType: "participant.open_registration.completed", Channel: "in_app", Enabled: false, Audience: []byte(`{"kind":"signal_subject"}`),
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Queries.ListEffectiveEnabledSignalNotificationSubscriptions(ctx, postgres.ListEffectiveEnabledSignalNotificationSubscriptionsParams{
		ScopeEventID: eventID, SignalType: "participant.open_registration.completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("event override enabled=false must suppress the platform default, got %+v", rows)
	}
	rows, err = db.Queries.ListEffectiveEnabledSignalNotificationSubscriptions(ctx, postgres.ListEffectiveEnabledSignalNotificationSubscriptionsParams{
		ScopeEventID: otherID, SignalType: "participant.open_registration.completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Channel != "in_app" {
		t.Fatalf("another event's override must not leak, got %+v", rows)
	}
}

func TestEffectiveEnabledSubscriptions_ManagerAssignmentFromPlatformDefaultOnly(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	eventID := mustCreateSubscriptionEvent(t, db, "manager")
	var eventRows int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_signal_notification_subscriptions WHERE scope_event_id = $1`, eventID).Scan(&eventRows); err != nil {
		t.Fatal(err)
	}
	if eventRows != 0 {
		t.Fatalf("event creation must not copy subscriptions, got %d rows", eventRows)
	}

	rows, err := db.Queries.ListEffectiveEnabledSignalNotificationSubscriptions(ctx, postgres.ListEffectiveEnabledSignalNotificationSubscriptionsParams{
		ScopeEventID: eventID, SignalType: "event.manager.assigned",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Channel != "in_app" {
		t.Fatalf("manager assignment must dispatch from the platform default, got %+v", rows)
	}
}

func TestEffectiveEventSubscriptions_ReportSourceAndResetRestoresInheritance(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	eventID := mustCreateSubscriptionEvent(t, db, "source")
	if _, err := db.Queries.UpsertEventSignalNotificationSubscription(ctx, postgres.UpsertEventSignalNotificationSubscriptionParams{
		ScopeEventID: eventID, SignalType: "participant.open_registration.completed", Channel: "email", Enabled: true, Audience: []byte(`{"kind":"all_captains"}`),
	}); err != nil {
		t.Fatal(err)
	}

	find := func(rows []postgres.ListEffectiveEventSignalNotificationSubscriptionsRow, signal, channel string) (postgres.ListEffectiveEventSignalNotificationSubscriptionsRow, bool) {
		for _, row := range rows {
			if row.SignalType == signal && row.Channel == channel {
				return row, true
			}
		}
		return postgres.ListEffectiveEventSignalNotificationSubscriptionsRow{}, false
	}

	rows, err := db.Queries.ListEffectiveEventSignalNotificationSubscriptions(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	overridden, ok := find(rows, "participant.open_registration.completed", "email")
	if !ok || overridden.Source != "event" || !overridden.Enabled || string(overridden.Audience) != `{"kind": "all_captains"}` {
		t.Fatalf("overridden row: %+v ok=%v", overridden, ok)
	}
	inherited, ok := find(rows, "participant.open_registration.completed", "in_app")
	if !ok || inherited.Source != "platform" || inherited.Enabled {
		t.Fatalf("inherited row: %+v ok=%v", inherited, ok)
	}

	affected, err := db.Queries.DeleteEventSignalNotificationSubscription(ctx, postgres.DeleteEventSignalNotificationSubscriptionParams{
		ScopeEventID: eventID, SignalType: "participant.open_registration.completed", Channel: "email",
	})
	if err != nil || affected != 1 {
		t.Fatalf("reset: affected=%d err=%v", affected, err)
	}
	affected, err = db.Queries.DeleteEventSignalNotificationSubscription(ctx, postgres.DeleteEventSignalNotificationSubscriptionParams{
		ScopeEventID: eventID, SignalType: "participant.open_registration.completed", Channel: "email",
	})
	if err != nil || affected != 0 {
		t.Fatalf("second reset: affected=%d err=%v", affected, err)
	}
	rows, err = db.Queries.ListEffectiveEventSignalNotificationSubscriptions(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	restored, ok := find(rows, "participant.open_registration.completed", "email")
	if !ok || restored.Source != "platform" || restored.Enabled {
		t.Fatalf("reset must restore the platform default: %+v ok=%v", restored, ok)
	}
}

func TestPlatformSignalNotificationDefaults_ListAndUpsert(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	saved, err := db.Queries.UpsertPlatformSignalNotificationDefault(ctx, postgres.UpsertPlatformSignalNotificationDefaultParams{
		SignalType: "participant.invitation.sent", Channel: "email", Enabled: true, Audience: []byte(`{"kind":"signal_subject"}`),
	})
	if err != nil || !saved.Enabled {
		t.Fatalf("upsert: %+v %v", saved, err)
	}
	rows, err := db.Queries.ListPlatformSignalNotificationDefaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found int
	for _, row := range rows {
		if row.SignalType == "participant.invitation.sent" && row.Channel == "email" {
			found++
			if !row.Enabled {
				t.Fatalf("upsert must update the existing seeded row: %+v", row)
			}
		}
	}
	if found != 1 {
		t.Fatalf("expected exactly one (signal, channel) row, got %d", found)
	}
}
