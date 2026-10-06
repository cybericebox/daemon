package postgres_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// «Використання» reads: participants outside the moderators team, VPN sessions
// per participant, the live handshake from the monitoring state and the lab
// counters with the task name.
func TestEventAnalytics_UsageReads(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "anusage")
	client := labBindingModel.ParticipantClientName(f.user)

	users, err := repo.UsageUsers(ctx, f.event, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].UserID != f.user || users[0].TeamID != f.team || users[0].TeamName == "" {
		t.Fatalf("participants (the moderators team is left out): %+v", users)
	}
	other := uuid.Must(uuid.NewV7())
	if users, err = repo.UsageUsers(ctx, f.event, &other); err != nil || len(users) != 0 {
		t.Fatalf("team filter: %+v err=%v", users, err)
	}

	// Two sessions of the participant and one of a client that is not a participant.
	for _, s := range []struct {
		start, end time.Duration
		rx0, rx1   int64
		noUser     bool
	}{{0, 4 * time.Minute, 100, 900, false}, {30 * time.Minute, 40 * time.Minute, 1000, 1500, false}, {0, time.Minute, 1, 2, true}} {
		var user any = f.user
		name := client
		if s.noUser {
			user, name = nil, "moderator"
		}
		rtExec(t, db, `INSERT INTO event_vpn_sessions (id, event_id, team_id, user_id, client_name, started_at, ended_at, rx_min, rx_max, tx_min, tx_max)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 0, 10)`,
			uuid.Must(uuid.NewV7()), f.event, f.team, user, name, anStart.Add(s.start), anStart.Add(s.end), s.rx0, s.rx1)
	}
	period := eventAnalyticsModel.Period{From: anStart, To: anStart.Add(4 * time.Hour)}
	vpn, err := repo.UsageVPN(ctx, f.event, period)
	if err != nil {
		t.Fatal(err)
	}
	if len(vpn) != 1 || vpn[0].UserID != f.user || vpn[0].Sessions != 2 || vpn[0].Seconds != 4*60+10*60 || vpn[0].RxBytes != 800+500 {
		t.Fatalf("vpn usage: %+v", vpn)
	}
	sessions, err := repo.UsageSessions(ctx, f.event, period, 1)
	if err != nil || len(sessions) != 1 || !sessions[0].StartedAt.Equal(anStart.Add(30*time.Minute)) {
		t.Fatalf("the newest session only: %+v err=%v", sessions, err)
	}
	narrow := eventAnalyticsModel.Period{From: anStart.Add(20 * time.Minute), To: anStart.Add(time.Hour)}
	if vpn, err = repo.UsageVPN(ctx, f.event, narrow); err != nil || len(vpn) != 1 || vpn[0].Sessions != 1 {
		t.Fatalf("the period bounds the sessions: %+v err=%v", vpn, err)
	}

	// Live state: one peer of the participant, one that is not a participant, one never connected.
	handshake := anStart.Add(time.Hour).Unix()
	rtExec(t, db, `INSERT INTO lab_monitoring_current (event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at, updated_at, payload)
VALUES ($1, $2, 'g', 'a', 1, $3, $3, $4::jsonb)`, f.event, f.team, anStart,
		`{"clients":[{"name":"`+client+`","status":{"statistics":{"lastHandshakeUnix":`+strconv.FormatInt(handshake, 10)+`,"rxBytes":5}}},
{"name":"moderator","status":{"statistics":{"lastHandshakeUnix":1}}},
{"name":"`+labBindingModel.ParticipantClientName(uuid.Must(uuid.NewV7()))+`","status":{}}]}`)
	live, err := repo.UsageHandshakes(ctx, f.event)
	if err != nil || len(live) != 1 || live[0].UserID != f.user || live[0].Handshake.Unix() != handshake {
		t.Fatalf("live handshakes: %+v err=%v", live, err)
	}

	challenge := f.challenge
	for _, surface := range []string{"vpn", "proxy"} {
		rtExec(t, db, `INSERT INTO event_lab_touches (id, event_id, team_id, user_id, event_challenge_id, surface, attempts_count, first_seen_at, last_seen_at, bytes_in, bytes_out)
VALUES ($1, $2, $3, $4, $5, $6, 7, $7, $8, 100, 50)`, uuid.Must(uuid.NewV7()), f.event, f.team, f.user, challenge, surface, anStart, anStart.Add(time.Minute))
	}
	touches, err := repo.UsageTouches(ctx, f.event)
	if err != nil || len(touches) != 2 || touches[0].Surface != "proxy" || touches[1].Surface != "vpn" || touches[0].Attempts != 7 || touches[0].BytesIn != 100 || touches[0].ChallengeID != challenge {
		t.Fatalf("touches: %+v err=%v", touches, err)
	}
}
