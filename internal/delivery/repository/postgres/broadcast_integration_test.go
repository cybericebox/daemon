package postgres_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/broadcastRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/dispatchRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/siteBannerRepo"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// activate makes a seeded user a deliverable recipient (active account).
func bcActivate(t *testing.T, db *testhelpers.TestDB, ids ...uuid.UUID) {
	t.Helper()
	for _, id := range ids {
		rtExec(t, db, `UPDATE users SET status = 'active' WHERE id = $1`, id)
	}
}

func bcIDs(rs []broadcastModel.Recipient) map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	for _, r := range rs {
		out[r.ID] = true
	}
	return out
}

func TestBroadcastAudience_PlatformScope(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := broadcastRepo.New(db.Queries)
	a := mustSeedUser(t, db, "bc-plat-a@test.test")
	b := mustSeedUser(t, db, "bc-plat-b@test.test")
	blocked := mustSeedUser(t, db, "bc-plat-blocked@test.test")
	bcActivate(t, db, a, b)
	rtExec(t, db, `UPDATE users SET role = 'admin' WHERE id = $1`, b)

	all, err := repo.Audience(ctx, nil, broadcastModel.Audience{Kind: broadcastModel.KindAll})
	if err != nil {
		t.Fatal(err)
	}
	got := bcIDs(all)
	if !got[a] || !got[b] || got[blocked] {
		t.Fatalf("all = %v, want a and b without the inactive account", got)
	}

	roles, err := repo.Audience(ctx, nil, broadcastModel.Audience{Kind: broadcastModel.KindRoles, Roles: []string{"admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if got = bcIDs(roles); !got[b] || got[a] {
		t.Fatalf("roles = %v, want only b", got)
	}

	users, err := repo.Audience(ctx, nil, broadcastModel.Audience{Kind: broadcastModel.KindUsers, UserIDs: []uuid.UUID{a, blocked}})
	if err != nil {
		t.Fatal(err)
	}
	if got = bcIDs(users); !got[a] || got[blocked] || len(got) != 1 {
		t.Fatalf("users = %v, want only a (blocked is inactive)", got)
	}
}

func TestBroadcastAudience_EventScope(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := broadcastRepo.New(db.Queries)
	event := mustSeedEventForParticipants(t, db, "bcevent")
	other := mustSeedEventForParticipants(t, db, "bcother")

	captain := mustSeedUser(t, db, "bc-captain@test.test")
	member := mustSeedUser(t, db, "bc-member@test.test")
	pending := mustSeedUser(t, db, "bc-pending@test.test")
	rejected := mustSeedUser(t, db, "bc-rejected@test.test")
	staff := mustSeedUser(t, db, "bc-staff@test.test")
	stranger := mustSeedUser(t, db, "bc-stranger@test.test")
	bcActivate(t, db, captain, member, pending, rejected, staff, stranger)

	team := eaTeam(t, db, event.ID, captain, "Blue")
	otherTeam := eaTeam(t, db, other.ID, captain, "Elsewhere")
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role) VALUES ($1, $2, 2, $3, $4, 0)`, event.ID, captain, rtNow, team)
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role) VALUES ($1, $2, 2, $3, $4, 1)`, event.ID, member, rtNow, team)
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at) VALUES ($1, $2, 1, $3)`, event.ID, pending, rtNow)
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at) VALUES ($1, $2, 3, $3)`, event.ID, rejected, rtNow)
	rtExec(t, db, `INSERT INTO event_managers (event_id, user_id, role, created_at) VALUES ($1, $2, 1, $3)`, event.ID, staff, rtNow)

	cases := []struct {
		name string
		aud  broadcastModel.Audience
		want []uuid.UUID
	}{
		{"all participants", broadcastModel.Audience{Kind: broadcastModel.KindAllParticipants}, []uuid.UUID{captain, member, pending}},
		{"approved", broadcastModel.Audience{Kind: broadcastModel.KindApproved}, []uuid.UUID{captain, member}},
		{"pending", broadcastModel.Audience{Kind: broadcastModel.KindPending}, []uuid.UUID{pending}},
		{"captains", broadcastModel.Audience{Kind: broadcastModel.KindCaptains}, []uuid.UUID{captain}},
		{"teams", broadcastModel.Audience{Kind: broadcastModel.KindTeams, TeamIDs: []uuid.UUID{team}}, []uuid.UUID{captain, member}},
		{"a team of another event", broadcastModel.Audience{Kind: broadcastModel.KindTeams, TeamIDs: []uuid.UUID{otherTeam}}, nil},
		{"selected participants", broadcastModel.Audience{Kind: broadcastModel.KindParticipants, UserIDs: []uuid.UUID{member, stranger, rejected}}, []uuid.UUID{member}},
		{"staff", broadcastModel.Audience{Kind: broadcastModel.KindStaff}, []uuid.UUID{staff}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs, err := repo.Audience(ctx, &event.ID, c.aud)
			if err != nil {
				t.Fatal(err)
			}
			got := bcIDs(rs)
			if len(got) != len(c.want) {
				t.Fatalf("recipients = %v, want %v", got, c.want)
			}
			for _, id := range c.want {
				if !got[id] {
					t.Fatalf("recipients = %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestBroadcast_DispatchesLinkAndCounters(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := broadcastRepo.New(db.Queries)
	dispatches := dispatchRepo.New(db.Queries)
	a := mustSeedUser(t, db, "bc-link-a@test.test")
	b := mustSeedUser(t, db, "bc-link-b@test.test")

	bc := broadcastModel.Broadcast{
		ID: uuid.Must(uuid.NewV7()),
		Content: broadcastModel.Content{
			Channels: []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail},
			Subject:  "Hello", EmailBody: json.RawMessage(`[{"type":"paragraph"}]`),
		},
		Audience:       broadcastModel.Audience{Kind: broadcastModel.KindAll},
		RecipientCount: 2,
	}
	if err := repo.Create(ctx, bc); err != nil {
		t.Fatal(err)
	}
	da, db2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if err := dispatches.CreateForBroadcast(ctx, da, "broadcast", a, nil, &bc.ID); err != nil {
		t.Fatal(err)
	}
	if err := dispatches.CreateForBroadcast(ctx, db2, "broadcast", b, nil, &bc.ID); err != nil {
		t.Fatal(err)
	}
	// One recipient per broadcast: a second dispatch for the same recipient is refused.
	if err := dispatches.CreateForBroadcast(ctx, uuid.Must(uuid.NewV7()), "broadcast", a, nil, &bc.ID); err == nil {
		t.Fatal("duplicate recipient dispatch was accepted")
	}
	// a delivered, b failed.
	rtExec(t, db, `UPDATE notification_dispatches SET status = 'done' WHERE id = ANY($1)`, []uuid.UUID{da, db2})
	rtExec(t, db, `INSERT INTO notification_dispatch_targets (dispatch_id, channel, status) VALUES ($1, 'email', 'done'), ($2, 'email', 'error')`, da, db2)
	rtExec(t, db, `UPDATE notification_dispatch_targets SET error = 'mailbox full' WHERE dispatch_id = $1`, db2)

	got, err := repo.Get(ctx, bc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SentCount != 1 || got.FailedCount != 1 || got.Status != broadcastModel.StatusSending {
		t.Fatalf("counters = sent %d failed %d status %s", got.SentCount, got.FailedCount, got.Status)
	}
	queued, err := repo.QueuedUserIDs(ctx, bc.ID)
	if err != nil || !queued[a] || !queued[b] {
		t.Fatalf("queued = %v err %v", queued, err)
	}
	deliveries, err := repo.Deliveries(ctx, bc.ID, 10, 0)
	if err != nil || len(deliveries) != 2 || deliveries[0].TargetStatus != "error" || deliveries[0].Error != "mailbox full" {
		t.Fatalf("deliveries = %+v err %v (failures must come first)", deliveries, err)
	}
	info, err := dispatches.Get(ctx, da)
	if err != nil || info.BroadcastID == nil || *info.BroadcastID != bc.ID {
		t.Fatalf("journal row lost its broadcast link: %+v err %v", info, err)
	}
	if err = repo.Finish(ctx, bc.ID, broadcastModel.StatusDone, 2); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.Get(ctx, bc.ID); got.Status != broadcastModel.StatusDone || got.FinishedAt == nil {
		t.Fatalf("finish = %+v", got)
	}
}

func TestSiteBanner_VisibilityRules(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := siteBannerRepo.New(db.Queries)
	event := mustSeedEventForParticipants(t, db, "bannerevent")
	other := mustSeedEventForParticipants(t, db, "bannerother")
	participant := mustSeedUser(t, db, "banner-participant@test.test")
	outsider := mustSeedUser(t, db, "banner-outsider@test.test")
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at) VALUES ($1, $2, 2, $3)`, event.ID, participant, rtNow)

	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	mk := func(scope *uuid.UUID, text string, in siteBannerModel.Input) siteBannerModel.Banner {
		t.Helper()
		in.Text, in.Level, in.IsActive = text, siteBannerModel.LevelInfo, true
		if in.Audience == "" {
			in.Audience = siteBannerModel.AudienceEveryone
		}
		b, err := repo.Create(ctx, siteBannerModel.New(scope, nil, in, now))
		if err != nil {
			t.Fatalf("create %q: %v", text, err)
		}
		return b
	}
	mk(nil, "platform-everyone", siteBannerModel.Input{})
	mk(nil, "platform-signed-in", siteBannerModel.Input{Audience: siteBannerModel.AudienceSignedIn})
	mk(nil, "platform-expired", siteBannerModel.Input{ActiveTo: &past})
	mk(nil, "platform-scheduled", siteBannerModel.Input{ActiveFrom: &future})
	off := mk(nil, "platform-off", siteBannerModel.Input{})
	if _, err := repo.Update(ctx, off.ID, siteBannerModel.Input{Text: "platform-off", Level: siteBannerModel.LevelInfo, Audience: siteBannerModel.AudienceEveryone, IsActive: false}); err != nil {
		t.Fatal(err)
	}
	mk(&event.ID, "event-everyone", siteBannerModel.Input{})
	mk(&event.ID, "event-participants", siteBannerModel.Input{Audience: siteBannerModel.AudienceParticipants})
	mk(&other.ID, "other-event", siteBannerModel.Input{})

	texts := func(eventID string, user *uuid.UUID) map[string]bool {
		t.Helper()
		rows, err := repo.Visible(ctx, eventID, user)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, r := range rows {
			out[r.Text] = true
		}
		return out
	}
	eq := func(name string, got map[string]bool, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
		for _, w := range want {
			if !got[w] {
				t.Fatalf("%s = %v, want %v", name, got, want)
			}
		}
	}
	eq("anonymous, no event", texts("", nil), "platform-everyone")
	eq("signed in, no event", texts("", &outsider), "platform-everyone", "platform-signed-in")
	eq("anonymous on the event site", texts(event.ID.String(), nil), "platform-everyone", "event-everyone")
	eq("outsider on the event site", texts(event.ID.String(), &outsider), "platform-everyone", "platform-signed-in", "event-everyone")
	eq("participant on the event site", texts(event.ID.String(), &participant), "platform-everyone", "platform-signed-in", "event-everyone", "event-participants")

	platformList, err := repo.List(ctx, "platform")
	if err != nil || len(platformList) != 5 {
		t.Fatalf("platform management list = %d err %v, want all 5 including inactive and expired", len(platformList), err)
	}
	eventList, err := repo.List(ctx, event.ID.String())
	if err != nil || len(eventList) != 2 {
		t.Fatalf("event management list = %d err %v", len(eventList), err)
	}
}

// The broadcast and banner migrations roll back and apply again cleanly.
func TestBroadcastMigrations_DownThenUp(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	run := func(name string) {
		t.Helper()
		sql, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Pool.Exec(context.Background(), string(sql)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	run("0130_site_banners.down.sql")
	run("0129_notification_broadcasts.down.sql")
	var n int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns WHERE table_name = 'notification_dispatches' AND column_name = 'broadcast_id'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("broadcast_id after down = %d err %v", n, err)
	}
	run("0129_notification_broadcasts.up.sql")
	run("0130_site_banners.up.sql")
}
