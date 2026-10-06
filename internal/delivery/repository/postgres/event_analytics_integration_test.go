package postgres_test

import (
	"context"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"strconv"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventActivityRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventActivityModel "github.com/cybericebox/daemon/internal/model/eventActivity"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// anStart is a bucket boundary; the seeded event runs 10:00–14:00.
var anStart = time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)

type anFixture struct {
	event, team, moderators, challenge, teamChallenge, modChallenge, user uuid.UUID
}

// anSeed: an event running from anStart with one team (one member) and the
// moderators team, one published challenge on both boards.
func anSeed(t *testing.T, db *testhelpers.TestDB, tag string) anFixture {
	t.Helper()
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, tag)
	rtExec(t, db, `UPDATE events SET lifecycle_configured = true, publish_at = $2, start_at = $3, finish_at = $4, withdraw_at = $5 WHERE id = $1`,
		event.ID, anStart.Add(-24*time.Hour), anStart, anStart.Add(4*time.Hour), anStart.AddDate(0, 1, 0))
	user := mustSeedUser(t, db, tag+"-member@test.test")
	moderator := mustSeedUser(t, db, tag+"-moderator@test.test")
	team := eaTeam(t, db, event.ID, user, "Blue")
	moderators := eaTeam(t, db, event.ID, moderator, "Moderators")
	rtExec(t, db, `UPDATE event_teams SET moderators = true, hidden = true WHERE id = $1`, moderators)
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role) VALUES ($1, $2, 2, $3, $4, 0)`, event.ID, user, anStart, team)
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)
	board := func(teamID uuid.UUID) uuid.UUID {
		tc, err := db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
			ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: teamID, EventChallengeID: challenge.ID,
			Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", Readiness: 2, CreatedAt: anStart,
		})
		if err != nil {
			t.Fatal(err)
		}
		return tc.ID
	}
	return anFixture{event: event.ID, team: team, moderators: moderators, challenge: challenge.ID, teamChallenge: board(team), modChallenge: board(moderators), user: user}
}

func anAttempt(t *testing.T, db *testhelpers.TestDB, f anFixture, teamID, teamChallengeID uuid.UUID, correct bool, at time.Time) uuid.UUID {
	t.Helper()
	a, err := db.Queries.CreateChallengeAttempt(context.Background(), postgres.CreateChallengeAttemptParams{
		ID: uuid.Must(uuid.NewV7()), EventID: f.event, EventTeamID: teamID, TeamChallengeID: teamChallengeID,
		UserID: f.user, Answer: "ICE{?}", Correct: correct, ReceivedAt: at, CreatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

type anBucket struct {
	at                               time.Time
	attempts, correct, solves, opens int32
}

func anBuckets(t *testing.T, db *testhelpers.TestDB, eventID uuid.UUID) []anBucket {
	t.Helper()
	rows, err := db.Pool.Query(context.Background(), `
SELECT bucket_at, attempts, correct, solves, opens FROM event_activity_buckets WHERE event_id = $1 ORDER BY bucket_at`, eventID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []anBucket
	for rows.Next() {
		var b anBucket
		if err = rows.Scan(&b.at, &b.attempts, &b.correct, &b.solves, &b.opens); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

// Buckets count attempts (effective correctness after decisions), solves and
// task opens per 5 minutes; the moderators team is left out; a rebuild
// follows the sources and is idempotent.
func TestEventAnalytics_RefreshBuckets(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "anbuckets")

	anAttempt(t, db, f, f.team, f.teamChallenge, false, anStart.Add(time.Minute))
	rejected := anAttempt(t, db, f, f.team, f.teamChallenge, true, anStart.Add(2*time.Minute))
	anAttempt(t, db, f, f.team, f.teamChallenge, true, anStart.Add(7*time.Minute))
	anAttempt(t, db, f, f.moderators, f.modChallenge, true, anStart.Add(time.Minute))
	if _, err := db.Queries.CreateChallengeAttemptDecision(ctx, postgres.CreateChallengeAttemptDecisionParams{
		ID: uuid.Must(uuid.NewV7()), ChallengeAttemptID: rejected, Decision: 2, Reason: "shared flag", DecidedBy: f.user, DecidedAt: anStart.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: f.teamChallenge, SolvedAt: anStart.Add(7 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	opens := eventActivityRepo.New(db.Queries)
	if err := opens.Append(ctx, eventActivityModel.TaskOpened(f.event, f.user, f.team, f.challenge, anStart.Add(30*time.Second))); err != nil {
		t.Fatal(err)
	}

	for pass := 0; pass < 2; pass++ {
		if err := repo.RefreshBuckets(ctx, f.event); err != nil {
			t.Fatal(err)
		}
		got := anBuckets(t, db, f.event)
		want := []anBucket{{anStart, 2, 0, 0, 1}, {anStart.Add(5 * time.Minute), 1, 1, 1, 0}}
		if len(got) != len(want) {
			t.Fatalf("pass %d buckets = %+v", pass, got)
		}
		for i := range want {
			if !got[i].at.Equal(want[i].at) || got[i].attempts != want[i].attempts || got[i].correct != want[i].correct ||
				got[i].solves != want[i].solves || got[i].opens != want[i].opens {
				t.Fatalf("pass %d bucket %d = %+v, want %+v", pass, i, got[i], want[i])
			}
		}
	}

	// A source that goes away takes its bucket with it.
	rtExec(t, db, `DELETE FROM event_activity WHERE event_id = $1`, f.event)
	rtExec(t, db, `DELETE FROM challenge_attempts WHERE event_id = $1 AND received_at < $2`, f.event, anStart.Add(5*time.Minute))
	if err := repo.RefreshBuckets(ctx, f.event); err != nil {
		t.Fatal(err)
	}
	if got := anBuckets(t, db, f.event); len(got) != 1 || !got[0].at.Equal(anStart.Add(5*time.Minute)) {
		t.Fatalf("buckets after the sources shrank: %+v", got)
	}

	series, err := repo.Series(ctx, f.event, eventAnalyticsModel.Period{From: anStart, To: anStart.Add(time.Hour)})
	if err != nil || len(series) != 1 || series[0].Attempts != 1 || series[0].Solves != 1 {
		t.Fatalf("series = %+v err=%v", series, err)
	}
}

// Candidates: started events with work left; a finalized event at its
// results revision drops out until the results change again.
func TestEventAnalytics_RollupCandidates(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "ancandidates")
	upcoming := mustSeedEventForParticipants(t, db, "anupcoming")
	rtExec(t, db, `UPDATE events SET lifecycle_configured = true, publish_at = $2, start_at = $3 WHERE id = $1`, upcoming.ID, anStart, anStart.AddDate(0, 0, 1))
	now := anStart.Add(5 * time.Hour)

	ids := func() map[uuid.UUID]eventAnalyticsRepo.RollupCandidate {
		list, err := repo.RollupCandidates(ctx, now, 10)
		if err != nil {
			t.Fatal(err)
		}
		out := map[uuid.UUID]eventAnalyticsRepo.RollupCandidate{}
		for _, c := range list {
			out[c.EventID] = c
		}
		return out
	}
	got := ids()
	c, ok := got[f.event]
	if !ok || len(got) != 1 || c.FinishedAt == nil || !c.FinishedAt.Equal(anStart.Add(4*time.Hour)) || c.BucketsFinalized {
		t.Fatalf("candidates = %+v", got)
	}
	if err := repo.MarkBucketsRefreshed(ctx, f.event, now, c.Revision, &now); err != nil {
		t.Fatal(err)
	}
	if _, still := ids()[f.event]; still {
		t.Fatal("a finalized event at its revision is done")
	}
	rtExec(t, db, `INSERT INTO event_result_revisions (event_id, revision) VALUES ($1, 5)
ON CONFLICT (event_id) DO UPDATE SET revision = event_result_revisions.revision + 5`, f.event)
	if c, again := ids()[f.event]; !again || !c.BucketsFinalized || c.Revision == c.BucketsRevision {
		t.Fatalf("a results change brings the event back: %+v", c)
	}
	state, err := repo.RollupState(ctx, f.event)
	if err != nil || state.RefreshedAt == nil || state.FinalizedAt == nil {
		t.Fatalf("state = %+v err=%v", state, err)
	}
	if state, err = repo.RollupState(ctx, upcoming.ID); err != nil || state.RefreshedAt != nil {
		t.Fatalf("state of an event never rolled up = %+v err=%v", state, err)
	}
}

// The VPN samples come from the protojson monitoring payloads, in arrival
// order behind the cursor; observations without clients still move it.
func TestEventAnalytics_VPNSamplesAndSessions(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "anvpn")
	client := labBindingModel.ParticipantClientName(f.user)
	handshake := anStart.Add(10 * time.Minute)
	observe := func(seq int, receivedAt time.Time, payload string) {
		rtExec(t, db, `
INSERT INTO event_lab_observations (id, event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at, received_at, schema_version, snapshot, payload)
VALUES ($1, $2, $3, 'group', 'agent', $4, $5, $5, 1, false, $6)`, uuid.Must(uuid.NewV7()), f.event, f.team, seq, receivedAt, payload)
	}
	observe(1, anStart.Add(11*time.Minute), `{"clients":[{"name":"`+client+`","labGroupName":"group","status":{"statistics":{"lastHandshakeUnix":"`+strconv.FormatInt(handshake.Unix(), 10)+`","rxBytes":"1000","txBytes":"200"}}},{"name":"idle","status":{}}]}`)
	observe(2, anStart.Add(12*time.Minute), `{"labs":[{"name":"web"}]}`)
	observe(3, anStart.Add(13*time.Minute), `{"clients":[{"name":"`+client+`","status":{"statistics":{"lastHandshakeUnix":"`+strconv.FormatInt(handshake.Add(2*time.Minute).Unix(), 10)+`","rxBytes":"5000","txBytes":"900"}}}]}`)

	samples, cursor, read, err := repo.VPNSamples(ctx, f.event, eventAnalyticsRepo.VPNCursor{}, 2)
	if err != nil || read != 2 || len(samples) != 1 {
		t.Fatalf("first batch: samples=%+v read=%d err=%v", samples, read, err)
	}
	if s := samples[0]; s.Client != client || s.TeamID != f.team || !s.Handshake.Equal(handshake) || s.Rx != 1000 || s.Tx != 200 {
		t.Fatalf("sample = %+v", s)
	}
	if !cursor.ReceivedAt.Equal(anStart.Add(12 * time.Minute)) {
		t.Fatalf("cursor after two observations = %+v", cursor)
	}
	rest, cursor, read, err := repo.VPNSamples(ctx, f.event, cursor, 2)
	if err != nil || read != 1 || len(rest) != 1 || rest[0].Rx != 5000 {
		t.Fatalf("second batch: %+v read=%d err=%v", rest, read, err)
	}
	if _, _, read, _ = repo.VPNSamples(ctx, f.event, cursor, 2); read != 0 {
		t.Fatal("nothing after the last observation")
	}

	changed, _ := eventAnalyticsModel.MergeVPNSessions(nil, append(samples, rest...), func() uuid.UUID { return uuid.Must(uuid.NewV7()) })
	if err = repo.SaveVPNSessions(ctx, f.event, changed, nil); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetVPNCursor(ctx, f.event, cursor, nil); err != nil {
		t.Fatal(err)
	}
	open, err := repo.OpenVPNSessions(ctx, f.event, handshake)
	if err != nil || len(open) != 1 || open[0].RxBytes() != 4000 || open[0].TxBytes() != 700 || open[0].UserID == nil || *open[0].UserID != f.user {
		t.Fatalf("stored sessions = %+v err=%v", open, err)
	}
	candidates, err := repo.RollupCandidates(ctx, anStart.Add(time.Hour), 10)
	if err != nil || len(candidates) != 1 || candidates[0].VPNCursor != cursor {
		t.Fatalf("cursor round trip: %+v err=%v", candidates, err)
	}

	// Retention: the deleted account's sessions stay without the person.
	users := userRepo.New(db.Queries)
	u, err := users.GetByID(ctx, f.user)
	if err != nil {
		t.Fatal(err)
	}
	expected := u.UpdatedAt
	if err = u.SoftDelete(anStart); err != nil {
		t.Fatal(err)
	}
	if _, err = users.Update(ctx, u, expected); err != nil {
		t.Fatal(err)
	}
	if _, err = retentionRepo.New(db.Queries).PurgeDeletedAccounts(ctx, anStart, 10); err != nil {
		t.Fatal(err)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM event_vpn_sessions WHERE event_id = $1 AND user_id IS NULL AND client_name = ''`, f.event); n != 1 {
		t.Fatal("the session must stay, anonymized")
	}
	// ...and all of them go with the event's analytics after the period.
	if n, err := retentionRepo.New(db.Queries).PurgeEventAnalytics(ctx, anStart.AddDate(1, 0, 0), 10); err != nil || n < 1 {
		t.Fatalf("purge: n=%d err=%v", n, err)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM event_vpn_sessions WHERE event_id = $1`, f.event); n != 0 {
		t.Fatal("sessions of an event past its period must go")
	}
}

// The overview counters leave the moderators team out and count pending
// invitations apart from registrations.
func TestEventAnalytics_Overview(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "anoverview")
	invited := mustSeedUser(t, db, "anoverview-invited@test.test")
	pending := mustSeedUser(t, db, "anoverview-pending@test.test")
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, invited) VALUES ($1, $2, 1, $3, true)`, f.event, invited, anStart)
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at) VALUES ($1, $2, 1, $3)`, f.event, pending, anStart)
	anAttempt(t, db, f, f.team, f.teamChallenge, false, anStart.Add(time.Minute))
	anAttempt(t, db, f, f.team, f.teamChallenge, true, anStart.Add(2*time.Minute))
	anAttempt(t, db, f, f.moderators, f.modChallenge, true, anStart.Add(time.Minute))
	if err := db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: f.teamChallenge, SolvedAt: anStart.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: f.modChallenge, SolvedAt: anStart.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Queries.CreateEventTeamStand(ctx, postgres.CreateEventTeamStandParams{EventTeamID: f.team, EventID: f.event, Status: 3, Now: anStart}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Overview(ctx, f.event, anStart.Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	want := eventAnalyticsRepo.Overview{
		ParticipantsRegistered: 2, ParticipantsApproved: 1, ParticipantsPending: 1, ParticipantsInvited: 1, ParticipantsActive: 1,
		TeamsTotal: 1, TeamsAdmitted: 1, Attempts: 2, AttemptsCorrect: 1, Solves: 1, StandsFailed: 1,
	}
	if got != want {
		t.Fatalf("overview = %+v\nwant       %+v", got, want)
	}
}

// The feed lists the first blood among ranked teams, failed stands and
// created teams, never the moderators team, newest first.
func TestEventAnalytics_Feed(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "anfeed")
	if err := db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: f.teamChallenge, SolvedAt: anStart.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: f.modChallenge, SolvedAt: anStart.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	rtExec(t, db, `UPDATE event_teams SET created_at = $2 WHERE id = $1`, f.team, anStart.Add(-time.Hour))
	rtExec(t, db, `UPDATE event_teams SET created_at = $2 WHERE id = $1`, f.moderators, anStart.Add(-2*time.Hour))
	rtExec(t, db, `INSERT INTO event_stand_transitions (event_id, team_id, source, generation, to_status, reason, at) VALUES ($1, $2, 'stand', 1, 3, 'no capacity', $3)`,
		f.event, f.team, anStart.Add(5*time.Minute))
	rtExec(t, db, `INSERT INTO event_stand_transitions (event_id, team_id, source, generation, to_status, at) VALUES ($1, $2, 'stand', 1, 2, $3)`,
		f.event, f.team, anStart.Add(6*time.Minute))

	got, err := repo.Feed(ctx, f.event, 10)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(got))
	for _, item := range got {
		kinds = append(kinds, item.Kind)
	}
	if len(got) != 3 || kinds[0] != "stand_failed" || kinds[1] != "first_blood" || kinds[2] != "team_created" {
		t.Fatalf("feed kinds = %v (%+v)", kinds, got)
	}
	if got[0].Detail != "no capacity" || got[0].TeamName != "Blue" || got[1].TeamName != "Blue" || got[1].ChallengeID != f.challenge {
		t.Fatalf("feed items: %+v", got)
	}
}
