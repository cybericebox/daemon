package eventAnalytics_test

import (
	"context"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"strconv"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventActivityRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventManagerRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventActivityModel "github.com/cybericebox/daemon/internal/model/eventActivity"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// The rollup job and the overview against the real schema: a finished
// infrastructure event with task opens and VPN telemetry is rolled up, then
// finalized and left alone; the overview reads the result.
func TestEventAnalytics_JobAndOverviewEndToEnd(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	start := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	finish := start.Add(3 * time.Hour)
	clock := finish.Add(time.Hour)

	user, err := userRepo.New(db.Queries).Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "analytics-e2e@test.test", start))
	if err != nil {
		t.Fatal(err)
	}
	e, err := eventModel.NewEvent("analyticse2e", "Analytics", start.AddDate(0, 0, -1), start.AddDate(0, 1, 0), user.ID, start.AddDate(0, 0, -1))
	if err != nil {
		t.Fatal(err)
	}
	event, err := eventRepo.New(db.Queries).Create(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE events SET lifecycle_configured = true, infrastructure_allowed = true, publish_at = $2, start_at = $3, finish_at = $4, withdraw_at = $5 WHERE id = $1`,
		event.ID, start.AddDate(0, 0, -1), start, finish, start.AddDate(0, 1, 0))
	if _, err = eventConfigRepo.New(db.Queries).Create(ctx, eventConfigModel.NewEventConfig(event.ID, start)); err != nil {
		t.Fatal(err)
	}
	team, err := eventTeamModel.New(event.ID, user.ID, "Blue", "analytics-e2e-code", start)
	if err != nil {
		t.Fatal(err)
	}
	if team, err = eventTeamRepo.New(db.Queries).Create(ctx, team); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role) VALUES ($1, $2, 2, $3, $4, 0)`, event.ID, user.ID, start, team.ID)
	challenge := uuid.Must(uuid.NewV7())
	if err = eventActivityRepo.New(db.Queries).Append(ctx, eventActivityModel.TaskOpened(event.ID, user.ID, team.ID, challenge, start.Add(3*time.Minute))); err != nil {
		t.Fatal(err)
	}
	handshake := start.Add(20 * time.Minute)
	for i, rx := range []int64{100, 900} {
		at := handshake.Add(time.Duration(i) * 2 * time.Minute)
		payload := `{"clients":[{"name":"` + labBindingModel.ParticipantClientName(user.ID) + `","status":{"statistics":{"lastHandshakeUnix":"` +
			strconv.FormatInt(at.Unix(), 10) + `","rxBytes":"` + strconv.FormatInt(rx, 10) + `","txBytes":"10"}}}]}`
		exec(`INSERT INTO event_lab_observations (id, event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at, received_at, schema_version, snapshot, payload)
VALUES ($1, $2, $3, 'group', 'agent', $4, $5, $5, 1, false, $6)`, uuid.Must(uuid.NewV7()), event.ID, team.ID, i+1, at.Add(10*time.Second), payload)
	}

	uc := eventAnalytics.New(eventAnalytics.Dependencies{
		Store: eventAnalyticsRepo.New(db.Queries), Events: eventRepo.New(db.Queries),
		Configs: eventConfigRepo.New(db.Queries), Memberships: eventManagerRepo.New(db.Queries),
		Now: func() time.Time { return clock },
	})
	for pass := 0; pass < 2; pass++ {
		if err = uc.RefreshEventAnalytics(ctx); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	var finalized, vpnFinalized bool
	if err = db.Pool.QueryRow(ctx, `SELECT buckets_finalized_at IS NOT NULL, vpn_finalized_at IS NOT NULL FROM event_analytics_rollups WHERE event_id = $1`, event.ID).
		Scan(&finalized, &vpnFinalized); err != nil || !finalized || !vpnFinalized {
		t.Fatalf("rollup state: buckets=%v vpn=%v err=%v", finalized, vpnFinalized, err)
	}
	var sessions int
	var rx int64
	if err = db.Pool.QueryRow(ctx, `SELECT count(*), COALESCE(sum(rx_max - rx_min), 0) FROM event_vpn_sessions WHERE event_id = $1 AND user_id = $2`, event.ID, user.ID).
		Scan(&sessions, &rx); err != nil || sessions != 1 || rx != 800 {
		t.Fatalf("vpn sessions = %d rx = %d err=%v", sessions, rx, err)
	}

	if err = uc.RequireEventAnalytics(ctx, event.ID, rbac.Claims{UserID: user.ID, Role: rbac.RoleUser}, eventAnalytics.LevelSections); err == nil {
		t.Fatal("a participant without an event role has no analytics access")
	}
	v, err := uc.GetEventAnalyticsOverview(ctx, event.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Final || v.RefreshedAt == nil || v.Participants.Approved != 1 || v.Teams.Total != 1 {
		t.Fatalf("overview: %+v", v)
	}
	if len(v.Series) != 36 || v.Series[0].Opens != 1 || !v.Series[0].At.Equal(start) {
		t.Fatalf("series: %d points, first %+v", len(v.Series), v.Series[0])
	}
}
