package eventAnalytics_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventManagerRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// realisticPayload is a delta of a team's lab group as the lab agent sends it
// every ~5 s: the labs with their devices and the VPN clients of the group.
func realisticPayload(seq int, observed time.Time, clients []string, handshake time.Time, rx, tx int64) string {
	var cl []string
	for i, name := range clients {
		stats := `"statistics":{"rxBytes":"0","txBytes":"0","lastHandshakeUnix":"0"}`
		if i%2 == 0 {
			stats = fmt.Sprintf(`"statistics":{"rxBytes":"%d","txBytes":"%d","lastHandshakeUnix":"%d"}`, rx+int64(i), tx+int64(i), handshake.Unix())
		}
		cl = append(cl, fmt.Sprintf(`{"name":%q,"labGroupName":"grp","status":{"ready":true,"assignedIp":"10.0.0.%d",%s}}`, name, i+2, stats))
	}
	return fmt.Sprintf(`{"agentId":"agent","sequence":"%d","snapshot":false,"observedAtUnixMs":"%d",`+
		`"labs":[{"uid":"u","name":"web","labGroupName":"grp","status":{"phase":"Ready","ready":true,"vpnReady":true,`+
		`"devices":[{"name":"app","ready":true,"podPhase":"Running","cpuMillicores":"12","memoryBytes":"1048576","usageAvailable":true,`+
		`"scheduling":{"state":"Started","queuedUnixMs":"1","dispatchedUnixMs":"2","startedUnixMs":"3"}}]}}],`+
		`"groups":[{"name":"grp","status":{"phase":"Ready","vpnRegistered":true}}],"clients":[%s],`+
		`"policies":[{"name":"pol","status":{"state":"Applied","appliedAtUnixMs":"5"}}]}`,
		seq, observed.UnixMilli(), strings.Join(cl, ","))
}

// Regression: the VPN session rollup of a finished infrastructure event never
// moved its cursor and produced no sessions. A realistic multi-batch history
// must be folded in, the cursor must advance and the rollup must close.
func TestRefreshEventAnalytics_VPNRollupOnRealisticTelemetry(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	start := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	finish := start.Add(8 * time.Hour)
	clock := finish.Add(24 * time.Hour)

	user, err := userRepo.New(db.Queries).Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "vpn-regress@test.test", start))
	if err != nil {
		t.Fatal(err)
	}
	e, err := eventModel.NewEvent("vpnregress", "VPN", start.AddDate(0, 0, -1), start.AddDate(0, 1, 0), user.ID, start.AddDate(0, 0, -1))
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
	team, err := eventTeamModel.New(event.ID, user.ID, "Blue", "vpn-regress-code", start)
	if err != nil {
		t.Fatal(err)
	}
	if team, err = eventTeamRepo.New(db.Queries).Create(ctx, team); err != nil {
		t.Fatal(err)
	}

	clients := []string{labBindingModel.ParticipantClientName(user.ID)}
	for i := 0; i < 5; i++ {
		clients = append(clients, labBindingModel.ParticipantClientName(uuid.Must(uuid.NewV7())))
	}
	uc := eventAnalytics.New(eventAnalytics.Dependencies{
		Store: eventAnalyticsRepo.New(db.Queries), Events: eventRepo.New(db.Queries),
		Configs: eventConfigRepo.New(db.Queries), Memberships: eventManagerRepo.New(db.Queries),
		Now: func() time.Time { return clock },
	})
	// A pass that runs while the event is already past its finish but before
	// any telemetry exists (the finish was moved later afterwards, or the rows
	// arrived late) closes the rollups with nothing read. The telemetry that
	// arrives after that must still be picked up.
	if err = uc.RefreshEventAnalytics(ctx); err != nil {
		t.Fatalf("early pass: %v", err)
	}
	// 4500 observations every 5 s: more than two read batches.
	const total = 4500
	for i := 0; i < total; i++ {
		at := start.Add(time.Duration(i) * 5 * time.Second)
		rx := int64(i) * 1000
		if i > total/2 { // the peer was recreated: the counters restarted
			rx = int64(i-total/2) * 1000
		}
		payload := realisticPayload(i+1, at, clients, at.Add(-20*time.Second), rx, rx/10)
		exec(`INSERT INTO event_lab_observations (id, event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at, received_at, schema_version, snapshot, payload)
VALUES ($1, $2, $3, 'grp', 'agent', $4, $5, $6, 3, $7, $8)`,
			uuid.Must(uuid.NewV7()), event.ID, team.ID, i+1, at, at.Add(300*time.Millisecond), i == 0, payload)
	}

	for pass := 0; pass < 4; pass++ {
		if err = uc.RefreshEventAnalytics(ctx); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	var cursorSet, vpnFinalized bool
	if err = db.Pool.QueryRow(ctx, `SELECT vpn_cursor_id IS NOT NULL, vpn_finalized_at IS NOT NULL FROM event_analytics_rollups WHERE event_id = $1`, event.ID).
		Scan(&cursorSet, &vpnFinalized); err != nil || !cursorSet || !vpnFinalized {
		t.Fatalf("vpn cursor set=%v finalized=%v err=%v", cursorSet, vpnFinalized, err)
	}
	var sessions int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_vpn_sessions WHERE event_id = $1`, event.ID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	// The same pass fed the minute tables and the transitions journal.
	var labReady, deviceReady, connected, minutes, clientMinutes int
	if err = db.Pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE kind = 'lab_ready'), count(*) FILTER (WHERE kind = 'device_ready'),
       count(*) FILTER (WHERE kind = 'client_connected')
FROM event_lab_transitions WHERE event_id = $1`, event.ID).Scan(&labReady, &deviceReady, &connected); err != nil {
		t.Fatal(err)
	}
	if labReady != 1 || deviceReady != 1 || connected != 3 {
		t.Fatalf("transitions: lab_ready=%d device_ready=%d client_connected=%d", labReady, deviceReady, connected)
	}
	if err = db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM event_lab_minutes WHERE event_id = $1),
       (SELECT count(*) FROM event_vpn_client_minutes WHERE event_id = $1)`, event.ID).Scan(&minutes, &clientMinutes); err != nil || minutes != 375 || clientMinutes != 6*375 {
		t.Fatalf("minute rows: labs=%d clients=%d err=%v", minutes, clientMinutes, err)
	}
	var usageFinalized bool
	if err = db.Pool.QueryRow(ctx, `SELECT lab_usage_finalized_at IS NOT NULL FROM event_analytics_rollups WHERE event_id = $1`, event.ID).Scan(&usageFinalized); err != nil || !usageFinalized {
		t.Fatalf("lab usage rollup finalized=%v err=%v", usageFinalized, err)
	}
	// Three of the six clients have handshakes; the counter restart does not split sessions.
	if sessions != 3 {
		t.Fatalf("vpn sessions = %d, want 3 (strconv %s)", sessions, strconv.Itoa(sessions))
	}
}
