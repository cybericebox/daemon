package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// paiDispatch journals one dispatch with one target of a channel.
func paiDispatch(t *testing.T, db *testhelpers.TestDB, kind string, at time.Time, channel, status, transport, errText, fallback string) {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	rtExec(t, db, `INSERT INTO notification_dispatches (id, notification_type, recipient_user_id, status, created_at, updated_at) VALUES ($1, $2, $3, 'done', $4, $4)`,
		id, kind, uuid.Must(uuid.NewV7()), at)
	rtExec(t, db, `INSERT INTO notification_dispatch_targets (dispatch_id, channel, status, error, attempts, transport, recipient, fallback_error) VALUES ($1, $2, $3, $4, 1, $5, 'person@example.com', $6)`,
		id, channel, status, errText, transport, fallback)
}

func paiSeedMail(t *testing.T, db *testhelpers.TestDB) {
	t.Helper()
	day10, day11 := paiBase.Add(12*time.Hour), paiBase.Add(36*time.Hour)
	paiDispatch(t, db, "user.welcome", day10, "email", "done", "platform", "", "")
	paiDispatch(t, db, "user.welcome", day10.Add(time.Hour), "email", "error", "platform",
		"smtp 550 5.1.1 <john.doe@example.com>: Recipient address rejected 10.1.2.3:587", "")
	paiDispatch(t, db, "user.welcome", day10.Add(2*time.Hour), "email", "error", "platform",
		"smtp 550 5.1.1 <jane@example.org>: Recipient address rejected 10.9.9.9:587", "")
	paiDispatch(t, db, "event.reminder", day11, "email", "done", "event", "", "event smtp down")
	paiDispatch(t, db, "event.reminder", day11.Add(time.Hour), "email", "done", "", "", "")
	paiDispatch(t, db, "smtp_test", day11, "email", "error", "env", "connection refused", "")
	paiDispatch(t, db, "user.welcome", day11, "in_app", "done", "", "", "")
	paiDispatch(t, db, "user.welcome", day11.Add(time.Hour), "in_app", "error", "", "", "")
	paiDispatch(t, db, "event.reminder", day11.Add(2*time.Hour), "email", "deferred", "platform", "", "")
	paiDispatch(t, db, "user.welcome", paiBase.AddDate(0, 0, -5), "email", "done", "platform", "", "")
}

// Delivery health counts targets per day, channel, transport and type in one
// pass; the transport grouping and the failure reasons are email-only, test
// sends stay out unless asked for.
func TestPlatformAnalytics_MailSummary(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := platformAnalyticsRepo.New(db.Queries)
	paiSeedMail(t, db)
	from, to := paiBase, paiBase.Add(48*time.Hour)

	got, err := repo.MailSummary(ctx, from, to, platformAnalyticsRepo.MailFilter{})
	if err != nil {
		t.Fatal(err)
	}
	days := map[string]platformAnalyticsRepo.MailCount{}
	for _, d := range got.Days {
		days[d.Day.UTC().Format("2006-01-02")] = d.MailCount
	}
	if len(days) != 2 || days["2026-09-10"] != (platformAnalyticsRepo.MailCount{Sent: 1, Failed: 2}) ||
		days["2026-09-11"] != (platformAnalyticsRepo.MailCount{Sent: 3, Failed: 1, Deferred: 1, Fallbacks: 1}) {
		t.Fatalf("days = %+v", got.Days)
	}
	transports := map[string]platformAnalyticsRepo.MailCount{}
	for _, k := range got.Transports {
		transports[k.Key] = k.MailCount
	}
	if len(transports) != 3 || transports["platform"] != (platformAnalyticsRepo.MailCount{Sent: 1, Failed: 2, Deferred: 1}) ||
		transports["event"] != (platformAnalyticsRepo.MailCount{Sent: 1, Fallbacks: 1}) || transports["unknown"] != (platformAnalyticsRepo.MailCount{Sent: 1}) {
		t.Fatalf("transports = %+v", got.Transports)
	}
	types := map[string]platformAnalyticsRepo.MailCount{}
	for _, k := range got.Types {
		types[k.Key] = k.MailCount
	}
	if len(types) != 2 || types["user.welcome"] != (platformAnalyticsRepo.MailCount{Sent: 2, Failed: 3}) || types["event.reminder"].Sent != 2 {
		t.Fatalf("types = %+v", got.Types)
	}

	channels := map[string]platformAnalyticsRepo.MailCount{}
	for _, k := range got.Channels {
		channels[k.Key] = k.MailCount
	}
	if len(channels) != 2 || channels["email"] != (platformAnalyticsRepo.MailCount{Sent: 3, Failed: 2, Deferred: 1, Fallbacks: 1}) ||
		channels["in_app"] != (platformAnalyticsRepo.MailCount{Sent: 1, Failed: 1}) {
		t.Fatalf("channels = %+v", got.Channels)
	}

	withTests, err := repo.MailSummary(ctx, from, to, platformAnalyticsRepo.MailFilter{IncludeTests: true})
	if err != nil {
		t.Fatal(err)
	}
	var failed int64
	for _, d := range withTests.Days {
		failed += d.Failed
	}
	if failed != 4 || len(withTests.Types) != 3 {
		t.Fatalf("with tests: failed=%d types=%+v", failed, withTests.Types)
	}

	inApp, err := repo.MailSummary(ctx, from, to, platformAnalyticsRepo.MailFilter{Channel: "in_app"})
	if err != nil {
		t.Fatal(err)
	}
	var inAppSent, inAppFailed int64
	for _, d := range inApp.Days {
		inAppSent += d.Sent
		inAppFailed += d.Failed
	}
	if inAppSent != 1 || inAppFailed != 1 || len(inApp.Transports) != 0 || len(inApp.Channels) != 1 || inApp.Channels[0].Key != "in_app" || len(inApp.Types) != 1 {
		t.Fatalf("in_app filter = %+v", inApp)
	}
	email, err := repo.MailSummary(ctx, from, to, platformAnalyticsRepo.MailFilter{Channel: "email"})
	if err != nil || len(email.Channels) != 1 || email.Channels[0].Key != "email" || len(email.Transports) != 3 {
		t.Fatalf("email filter = %+v err=%v", email, err)
	}
	// A transport belongs to email: it leaves the in-app deliveries out even with no channel filter.
	byPlatform, err := repo.MailSummary(ctx, from, to, platformAnalyticsRepo.MailFilter{Transport: "platform"})
	if err != nil || len(byPlatform.Channels) != 1 || byPlatform.Channels[0].Key != "email" {
		t.Fatalf("transport filter must exclude in_app: %+v err=%v", byPlatform, err)
	}

	byTransport, err := repo.MailSummary(ctx, from, to, platformAnalyticsRepo.MailFilter{Transport: "event"})
	if err != nil || len(byTransport.Transports) != 1 || byTransport.Transports[0].Sent != 1 {
		t.Fatalf("transport filter = %+v err=%v", byTransport, err)
	}
	byType, err := repo.MailSummary(ctx, from, to, platformAnalyticsRepo.MailFilter{Type: "event.reminder"})
	if err != nil || len(byType.Types) != 1 || byType.Types[0].Sent != 2 {
		t.Fatalf("type filter = %+v err=%v", byType, err)
	}

	empty, err := repo.MailSummary(ctx, from.AddDate(0, -3, 0), to.AddDate(0, -3, 0), platformAnalyticsRepo.MailFilter{})
	if err != nil || len(empty.Days)+len(empty.Transports)+len(empty.Channels)+len(empty.Types) != 0 {
		t.Fatalf("empty = %+v err=%v", empty, err)
	}
}

// Errors group by the normalized text and never carry an address.
func TestPlatformAnalytics_MailErrors(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := platformAnalyticsRepo.New(db.Queries)
	paiSeedMail(t, db)
	from, to := paiBase, paiBase.Add(48*time.Hour)

	got, err := repo.MailErrors(ctx, from, to, platformAnalyticsRepo.MailFilter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Total != 2 || got[0].Code != "550 5.1.1" {
		t.Fatalf("errors = %+v", got)
	}
	for _, leak := range []string{"@", "john", "jane", "10.1.2.3", "10.9.9.9", "587"} {
		if strings.Contains(got[0].Message, leak) {
			t.Fatalf("message %q leaks %q", got[0].Message, leak)
		}
	}
	if !strings.Contains(got[0].Message, "<address>") || !strings.Contains(got[0].Message, "<ip>") {
		t.Fatalf("message = %q", got[0].Message)
	}

	withTests, err := repo.MailErrors(ctx, from, to, platformAnalyticsRepo.MailFilter{IncludeTests: true}, 10)
	if err != nil || len(withTests) != 2 || withTests[0].Total != 2 || withTests[1].Message != "connection refused" {
		t.Fatalf("with tests = %+v err=%v", withTests, err)
	}
	top1, err := repo.MailErrors(ctx, from, to, platformAnalyticsRepo.MailFilter{IncludeTests: true}, 1)
	if err != nil || len(top1) != 1 {
		t.Fatalf("limit = %+v err=%v", top1, err)
	}
}

// The filter options list what the period has, test sends only on request.
func TestPlatformAnalytics_MailOptions(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := platformAnalyticsRepo.New(db.Queries)
	paiSeedMail(t, db)
	from, to := paiBase, paiBase.Add(48*time.Hour)

	got, err := repo.MailOptions(ctx, from, to, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Transports, ",") != "event,platform,unknown" || strings.Join(got.Types, ",") != "event.reminder,user.welcome" {
		t.Fatalf("options = %+v", got)
	}
	inAppOptions, err := repo.MailOptions(ctx, from, to, "in_app", false)
	if err != nil || len(inAppOptions.Transports) != 0 || strings.Join(inAppOptions.Types, ",") != "user.welcome" {
		t.Fatalf("in_app options = %+v err=%v", inAppOptions, err)
	}
	withTests, err := repo.MailOptions(ctx, from, to, "", true)
	if err != nil || strings.Join(withTests.Transports, ",") != "env,event,platform,unknown" || strings.Join(withTests.Types, ",") != "event.reminder,smtp_test,user.welcome" {
		t.Fatalf("options with tests = %+v err=%v", withTests, err)
	}
}
