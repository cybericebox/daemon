package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventActivityRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventActivityModel "github.com/cybericebox/daemon/internal/model/eventActivity"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// anSecondTeam adds a team "Red" with one approved member and its board of
// the seeded challenge; it returns the team, the member and the board.
func anSecondTeam(t *testing.T, db *testhelpers.TestDB, f anFixture) (team, user, board uuid.UUID) {
	t.Helper()
	user = mustSeedUser(t, db, "anred-"+f.event.String()+"@test.test")
	team = eaTeam(t, db, f.event, user, "Red")
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role) VALUES ($1, $2, 2, $3, $4, 0)`, f.event, user, anStart, team)
	tc, err := db.Queries.CreateTeamChallenge(context.Background(), postgres.CreateTeamChallengeParams{
		ID: uuid.Must(uuid.NewV7()), EventID: f.event, EventTeamID: team, EventChallengeID: f.challenge,
		Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", Readiness: 2, CreatedAt: anStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	return team, user, tc.ID
}

// The «Завдання» and «Прогрес» reads over one solved, one failed and one
// moderator board: the moderators team is never counted.
func TestEventAnalytics_TasksAndProgress(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "antasks")
	rtExec(t, db, `UPDATE event_challenges SET published = true, snapshot = '{"name":"Web 1","difficulty":"easy"}' WHERE id = $1`, f.challenge)
	red, redUser, redBoard := anSecondTeam(t, db, f)
	at := func(m int) time.Time { return anStart.Add(time.Duration(m) * time.Minute) }
	period := eventAnalyticsModel.Period{From: anStart, To: anStart.Add(2 * time.Hour)}

	// Blue opens at +5, fails at +10, solves at +20. Red fails twice, unlocks a
	// hint after. The moderators team solves at +1.
	if err := eventActivityRepo.New(db.Queries).Append(ctx, eventActivityModel.TaskOpened(f.event, f.user, f.team, f.challenge, at(5))); err != nil {
		t.Fatal(err)
	}
	anAttempt(t, db, f, f.team, f.teamChallenge, false, at(10))
	anAttempt(t, db, f, f.team, f.teamChallenge, true, at(20))
	anAttempt(t, db, f, red, redBoard, false, at(30))
	anAttempt(t, db, f, red, redBoard, false, at(31))
	anAttempt(t, db, f, f.moderators, f.modChallenge, true, at(1))
	for _, solve := range []struct {
		board uuid.UUID
		at    time.Time
	}{{f.teamChallenge, at(20)}, {f.modChallenge, at(1)}} {
		if err := db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: solve.board, SolvedAt: solve.at}); err != nil {
			t.Fatal(err)
		}
	}
	rtExec(t, db, `
INSERT INTO team_challenge_hint_unlocks (team_challenge_id, hint_id, event_id, event_team_id, event_challenge_id, unlocked_by, unlocked_at, cost)
VALUES ($1, $2, $3, $4, $5, $6, $7, 10)`, redBoard, uuid.Must(uuid.NewV7()), f.event, red, f.challenge, redUser, at(32))

	challenges, err := repo.Challenges(ctx, f.event)
	if err != nil || len(challenges) != 1 || challenges[0].Name != "Web 1" || challenges[0].Difficulty != "easy" || challenges[0].GroupID != uuid.Nil {
		t.Fatalf("challenges = %+v err=%v", challenges, err)
	}
	if _, ok, err := repo.Challenge(ctx, f.event, uuid.Must(uuid.NewV7())); ok || err != nil {
		t.Fatalf("unknown challenge: ok=%v err=%v", ok, err)
	}

	stats, err := repo.TaskStats(ctx, f.event, period)
	if err != nil || len(stats) != 1 {
		t.Fatalf("stats = %+v err=%v", stats, err)
	}
	s := stats[0]
	if s.Attempts != 4 || s.AttemptsCorrect != 1 || s.TeamsTried != 2 || s.TeamsOpened != 1 || s.Solves != 1 || s.HintsOpened != 1 || s.HintPoints != 10 {
		t.Fatalf("counts: %+v", s)
	}
	if s.MedianSinceStart == nil || *s.MedianSinceStart != 20*60 || s.MedianSinceOpen == nil || *s.MedianSinceOpen != 15*60 {
		t.Fatalf("medians: start=%v open=%v", s.MedianSinceStart, s.MedianSinceOpen)
	}
	if s.FirstBloodTeam != "Blue" || s.FirstBloodAt == nil || !s.FirstBloodAt.Equal(at(20)) {
		t.Fatalf("first blood: %q %v", s.FirstBloodTeam, s.FirstBloodAt)
	}
	// The period narrows everything: only Red's failures remain.
	late, err := repo.TaskStats(ctx, f.event, eventAnalyticsModel.Period{From: at(25), To: at(60)})
	if err != nil || late[0].Attempts != 2 || late[0].Solves != 0 || late[0].MedianSinceStart != nil || late[0].FirstBloodTeam != "" || late[0].FirstBloodAt != nil {
		t.Fatalf("late stats = %+v err=%v", late, err)
	}

	failed, err := repo.TaskFailedTeams(ctx, f.event, f.challenge, period)
	if err != nil || len(failed) != 1 || failed[0].TeamName != "Red" || failed[0].Attempts != 2 || failed[0].HintsOpened != 1 || !failed[0].LastAttemptAt.Equal(at(31)) {
		t.Fatalf("failed teams = %+v err=%v", failed, err)
	}
	wrong, err := repo.TaskWrongAnswers(ctx, f.event, f.challenge, period, 10)
	if err != nil || len(wrong) != 1 || wrong[0].Attempts != 3 || wrong[0].Teams != 2 {
		t.Fatalf("wrong answers = %+v err=%v", wrong, err)
	}
	hints, err := repo.TaskHintEffect(ctx, f.event, f.challenge, period)
	if err != nil || len(hints) != 2 {
		t.Fatalf("hint effect = %+v err=%v", hints, err)
	}
	for _, h := range hints {
		switch h.TeamID {
		case f.team:
			// Solved without a hint: 15 minutes after the open.
			if h.Hinted || !h.Solved || h.SinceStart == nil || *h.SinceStart != 15*60 || h.SinceHint != nil {
				t.Fatalf("blue: %+v", h)
			}
		case red:
			if !h.Hinted || h.Solved || h.SinceStart != nil || h.SinceHint != nil {
				t.Fatalf("red: %+v", h)
			}
		default:
			t.Fatalf("unexpected team %s", h.TeamID)
		}
	}

	cells, err := repo.Matrix(ctx, f.event, period)
	if err != nil || len(cells) != 2 {
		t.Fatalf("matrix = %+v err=%v", cells, err)
	}
	for _, c := range cells {
		switch c.TeamID {
		case f.team:
			if c.Attempts != 2 || c.SolvedAt == nil || !c.SolvedAt.Equal(at(20)) {
				t.Fatalf("blue cell: %+v", c)
			}
		case red:
			if c.Attempts != 2 || c.SolvedAt != nil {
				t.Fatalf("red cell: %+v", c)
			}
		default:
			t.Fatalf("unexpected team %s", c.TeamID)
		}
	}

	teams, err := repo.RankedTeams(ctx, f.event)
	if err != nil || len(teams) != 2 || teams[0].Name != "Blue" || teams[0].Solved != 1 || teams[0].Points <= 0 || !teams[0].Admitted {
		t.Fatalf("ranked teams = %+v err=%v", teams, err)
	}
	scores, err := repo.ScoreEvents(ctx, f.event, []uuid.UUID{f.team})
	if err != nil || len(scores) != 1 || scores[0].TeamID != f.team || !scores[0].At.Equal(at(20)) || scores[0].Points <= 0 {
		t.Fatalf("score events = %+v err=%v", scores, err)
	}
	if none, err := repo.ScoreEvents(ctx, f.event, nil); err != nil || len(none) != 0 {
		t.Fatalf("no teams, no events: %+v err=%v", none, err)
	}

	if err = repo.RefreshBuckets(ctx, f.event); err != nil {
		t.Fatal(err)
	}
	heat, err := repo.Heatmap(ctx, f.event, period)
	if err != nil || len(heat) != 2 {
		t.Fatalf("heatmap = %+v err=%v", heat, err)
	}
	for _, c := range heat {
		if !c.HourAt.Equal(anStart) {
			t.Fatalf("hour: %+v", c)
		}
		if c.TeamID == f.team && (c.Attempts != 2 || c.Opens != 1 || c.Solves != 1) || c.TeamID == red && (c.Attempts != 2 || c.Opens != 0 || c.Solves != 0) {
			t.Fatalf("cell: %+v", c)
		}
	}
	series, err := repo.TaskSeries(ctx, f.event, f.challenge, period)
	if err != nil || len(series) != 4 {
		t.Fatalf("task series = %+v err=%v", series, err)
	}

	activity, err := repo.TeamActivity(ctx, f.event, at(60))
	if err != nil || len(activity) != 2 {
		t.Fatalf("activity = %+v err=%v", activity, err)
	}
	for _, a := range activity {
		want := map[uuid.UUID]time.Time{f.team: at(20), red: at(32)}[a.TeamID]
		if a.LastActivityAt == nil || !a.LastActivityAt.Equal(want) {
			t.Fatalf("last activity of %s = %v, want %v", a.TeamName, a.LastActivityAt, want)
		}
	}
	// As of before anything happened nobody has a sign of life.
	if before, err := repo.TeamActivity(ctx, f.event, at(-60)); err != nil || before[0].LastActivityAt != nil {
		t.Fatalf("activity before the event = %+v err=%v", before, err)
	}
}
