package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestSiteBannerTargetingSQL(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "targetevent")
	other := mustSeedEventForParticipants(t, db, "othertargetevent")
	insert := func(text, mode string, sites []string, events []uuid.UUID) {
		t.Helper()
		rtExec(t, db, `INSERT INTO site_banners(id,text,level,audience,is_active,target_mode,target_sites,target_event_ids) VALUES($1,$2,'info','everyone',true,$3,$4,$5)`, uuid.Must(uuid.NewV7()), text, mode, sites, events)
	}
	insert("all", "all", []string{}, []uuid.UUID{})
	insert("services-and-event", "selected", []string{"main", "admin"}, []uuid.UUID{event.ID})
	insert("all-events", "selected", []string{"event"}, []uuid.UUID{})
	insert("other-event", "selected", []string{}, []uuid.UUID{other.ID})
	insert("unknown-event", "selected", []string{}, []uuid.UUID{uuid.Must(uuid.NewV7())})
	for _, tc := range []struct {
		name, site, event string
		want              []string
	}{
		{name: "legacy", want: []string{"all"}},
		{name: "main", site: "main", want: []string{"all", "services-and-event"}},
		{name: "id", site: "id", want: []string{"all"}},
		{name: "admin", site: "admin", want: []string{"all", "services-and-event"}},
		{name: "exercises", site: "exercises", want: []string{"all"}},
		{name: "exact event", site: "event", event: event.ID.String(), want: []string{"all", "services-and-event", "all-events"}},
		{name: "other event", site: "event", event: other.ID.String(), want: []string{"all", "all-events", "other-event"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := db.Queries.ListVisibleSiteBanners(ctx, postgres.ListVisibleSiteBannersParams{SiteFilter: tc.site, EventFilter: tc.event})
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, row := range rows {
				got[row.Text] = true
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			for _, want := range tc.want {
				if !got[want] {
					t.Fatalf("missing %q: %v", want, got)
				}
			}
		})
	}
}

func TestSiteBannerTargetingUpdatePreservesCurrentRestriction(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	target := uuid.Must(uuid.NewV7())
	rtExec(t, db, `INSERT INTO site_banners(id,text,level,audience,is_active,target_mode,target_sites,target_event_ids) VALUES($1,'initial','info','everyone',true,'selected',ARRAY['main'],$2)`, id, []uuid.UUID{target})
	// Another administrator narrows the banner after an older client loaded it.
	rtExec(t, db, `UPDATE site_banners SET target_sites=ARRAY['admin'] WHERE id=$1`, id)
	p := postgres.UpdateSiteBannerParams{ID: id, Text: "content edited by old client", Level: "info", Audience: "everyone", IsActive: true, TargetMode: "all", TargetSites: []string{}, TargetEventIds: []uuid.UUID{}}
	b, err := db.Queries.UpdateSiteBanner(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if b.TargetMode != "selected" || len(b.TargetSites) != 1 || b.TargetSites[0] != "admin" || len(b.TargetEventIds) != 1 || b.TargetEventIds[0] != target {
		t.Fatalf("omitted update broadened targeting: %+v", b)
	}
	p.UpdateTargeting = true
	b, err = db.Queries.UpdateSiteBanner(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if b.TargetMode != "all" || len(b.TargetSites) != 0 || len(b.TargetEventIds) != 0 {
		t.Fatalf("explicit all did not clear: %+v", b)
	}
}

func TestSiteBannerOrderingAndTargetingRespectWindowAndAudience(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "bannerorder")
	participant := mustSeedUser(t, db, "target-participant@test.test")
	manager := mustSeedUser(t, db, "target-manager@test.test")
	outsider := mustSeedUser(t, db, "target-outsider@test.test")
	rtExec(t, db, `INSERT INTO event_participants(event_id,user_id,status,created_at) VALUES($1,$2,2,$3)`, event.ID, participant, rtNow)
	rtExec(t, db, `INSERT INTO event_managers(event_id,user_id,role,created_at) VALUES($1,$2,1,$3)`, event.ID, manager, rtNow)
	add := func(text, level, audience string, scope *uuid.UUID, offset int) {
		t.Helper()
		rtExec(t, db, `INSERT INTO site_banners(id,scope_event_id,text,level,audience,is_active,created_at,target_mode,target_sites) VALUES($1,$2,$3,$4,$5,true,$6,$7,$8)`, uuid.Must(uuid.NewV7()), scope, text, level, audience, rtNow.Add(time.Duration(offset)*time.Minute), map[bool]string{true: "selected", false: "all"}[scope == nil], map[bool][]string{true: {"event"}, false: {}}[scope == nil])
	}
	add("global-warning-old", "warning", "everyone", nil, 0)
	add("event-warning-new", "warning", "everyone", &event.ID, 5)
	add("global-warning-new", "warning", "everyone", nil, 1)
	add("event-critical", "critical", "everyone", &event.ID, 2)
	add("global-info", "info", "everyone", nil, 3)
	add("signed-in", "info", "signed_in", nil, 4)
	add("participants", "info", "participants", &event.ID, 6)
	add("off", "critical", "everyone", nil, 7)
	rtExec(t, db, `UPDATE site_banners SET is_active=false WHERE text='off'`)
	add("expired", "critical", "everyone", nil, 8)
	rtExec(t, db, `UPDATE site_banners SET active_to=now()-interval '1 hour' WHERE text='expired'`)
	add("future", "critical", "everyone", nil, 9)
	rtExec(t, db, `UPDATE site_banners SET active_from=now()+interval '1 hour' WHERE text='future'`)
	for _, tc := range []struct {
		name string
		user *uuid.UUID
		want []string
	}{
		{"anonymous", nil, []string{"event-critical", "global-warning-new", "global-warning-old", "event-warning-new", "global-info"}},
		{"outsider", &outsider, []string{"event-critical", "global-warning-new", "global-warning-old", "event-warning-new", "signed-in", "global-info"}},
		{"participant", &participant, []string{"event-critical", "global-warning-new", "global-warning-old", "event-warning-new", "signed-in", "global-info", "participants"}},
		{"manager", &manager, []string{"event-critical", "global-warning-new", "global-warning-old", "event-warning-new", "signed-in", "global-info", "participants"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := postgres.ListVisibleSiteBannersParams{SiteFilter: "event", EventFilter: event.ID.String()}
			if tc.user != nil {
				p.UserID = uuid.NullUUID{UUID: *tc.user, Valid: true}
			}
			rows, err := db.Queries.ListVisibleSiteBanners(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, row := range rows {
				got = append(got, row.Text)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestSiteBannerTargetingMigrationCompatibility(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	apply := func(name string) {
		t.Helper()
		sql, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	apply("0173_site_banner_targeting.down.sql")
	id := uuid.Must(uuid.NewV7())
	rtExec(t, db, `INSERT INTO site_banners(id,text,level,audience,is_active) VALUES($1,'legacy','info','everyone',true)`, id)
	apply("0173_site_banner_targeting.up.sql")
	b, err := db.Queries.GetSiteBanner(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if b.TargetMode != "all" || len(b.TargetSites) != 0 || len(b.TargetEventIds) != 0 {
		t.Fatalf("legacy migration changed audience: %+v", b)
	}
	event := mustSeedEventForParticipants(t, db, "targetconstraint")
	if _, err = db.Pool.Exec(ctx, `INSERT INTO site_banners(id,scope_event_id,text,level,audience,target_mode,target_sites) VALUES($1,$2,'bad','info','everyone','selected',ARRAY['main'])`, uuid.Must(uuid.NewV7()), event.ID); err == nil {
		t.Fatal("database allowed event scope broadening")
	}
}
