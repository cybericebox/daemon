package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The pulse reads the newest inbox item, the unread count and the stamp of the open personal banners of one scope.
func TestGetInboxPulse(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	user := mustSeedUser(t, db, "pulse@example.com")
	other := mustSeedUser(t, db, "pulse-other@example.com")
	event := mustSeedEventForParticipants(t, db, "pulseevent")
	otherEvent := mustSeedEventForParticipants(t, db, "pulseother")

	empty, err := db.Queries.GetInboxPulse(ctx, postgres.GetInboxPulseParams{UserID: user})
	if err != nil {
		t.Fatal(err)
	}
	if empty.LatestID != uuid.Nil || !empty.LatestCreatedAt.Equal(time.Unix(0, 0)) || empty.UnreadCount != 0 || empty.BannerCount != 0 {
		t.Fatalf("empty inbox: %+v", empty)
	}

	insert := func(userID uuid.UUID, surface string, scope *uuid.UUID, read, dismissed bool, at time.Time) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		var readAt, dismissedAt *time.Time
		if read {
			readAt = &at
		}
		if dismissed {
			dismissedAt = &at
		}
		rtExec(t, db, `INSERT INTO in_app_notifications (id, user_id, title, surface, scope_event_id, read_at, dismissed_at, created_at)
			VALUES ($1, $2, 'x', $3, $4, $5, $6, $7)`, id, userID, surface, scope, readAt, dismissedAt, at)
		return id
	}
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	insert(user, "inbox", nil, true, false, base)
	insert(user, "inbox", nil, false, false, base.Add(time.Minute))
	eventUnread := insert(user, "inbox", &event.ID, false, false, base.Add(2*time.Minute))
	otherEventItem := insert(user, "inbox", &otherEvent.ID, false, false, base.Add(3*time.Minute))
	insert(other, "inbox", nil, false, false, base.Add(4*time.Minute))
	banner := base.Add(5 * time.Minute)
	insert(user, "banner", &event.ID, false, false, banner)
	insert(user, "banner", nil, false, true, base.Add(6*time.Minute))

	for _, tc := range []struct {
		name       string
		filter     string
		latest     uuid.UUID
		unread     int64
		banners    int64
		bannerLast time.Time
	}{
		{name: "every scope", filter: "", latest: otherEventItem, unread: 3, banners: 1, bannerLast: banner},
		{name: "event site", filter: event.ID.String(), latest: eventUnread, unread: 2, banners: 1, bannerLast: banner},
		{name: "other event site", filter: otherEvent.ID.String(), latest: otherEventItem, unread: 2, banners: 0, bannerLast: time.Unix(0, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := db.Queries.GetInboxPulse(ctx, postgres.GetInboxPulseParams{UserID: user, EventFilter: tc.filter})
			if err != nil {
				t.Fatal(err)
			}
			if got.LatestID != tc.latest || got.UnreadCount != tc.unread || got.BannerCount != tc.banners || !got.BannerLatestAt.Equal(tc.bannerLast) {
				t.Fatalf("got %+v, want latest %s unread %d banners %d at %s", got, tc.latest, tc.unread, tc.banners, tc.bannerLast)
			}
		})
	}
}

// The banner snapshot holds every enabled banner that has not ended, the ones that start later included.
func TestListActiveSiteBanners(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	insert := func(text string, active bool, from, to *time.Time) {
		t.Helper()
		rtExec(t, db, `INSERT INTO site_banners(id,text,level,audience,is_active,active_from,active_to) VALUES($1,$2,'info','everyone',$3,$4,$5)`,
			uuid.Must(uuid.NewV7()), text, active, from, to)
	}
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	insert("open", true, nil, nil)
	insert("running", true, &past, &future)
	insert("upcoming", true, &future, nil)
	insert("ended", true, nil, &past)
	insert("disabled", false, nil, nil)

	rows, err := db.Queries.ListActiveSiteBanners(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, row := range rows {
		got[row.Text] = true
	}
	want := []string{"open", "running", "upcoming"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("missing %q: %v", w, got)
		}
	}
}
