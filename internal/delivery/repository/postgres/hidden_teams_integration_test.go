package postgres_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// hiddenFixture is an event with one first-solves-ladder challenge and teams that
// solved it at chosen moments.
type hiddenFixture struct {
	db        *testhelpers.TestDB
	eventID   uuid.UUID
	challenge postgres.EventChallenge
	teams     map[string]uuid.UUID
	solves    map[string]uuid.UUID
}

func newHiddenFixture(t *testing.T, tag string) *hiddenFixture {
	t.Helper()
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, tag)
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)
	if _, err := db.Pool.Exec(ctx, `UPDATE events SET scoring_mode = 2, dynamic_min_points = 100,
		dynamic_max_points = 500, dynamic_floor_at_percent = 100 WHERE id = $1`, event.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Queries.InsertEventScoringPopulation(ctx, postgres.InsertEventScoringPopulationParams{EventID: event.ID, UnitsCount: 4, CapturedAt: itNow}); err != nil {
		t.Fatal(err)
	}
	return &hiddenFixture{db: db, eventID: event.ID, challenge: challenge, teams: map[string]uuid.UUID{}, solves: map[string]uuid.UUID{}}
}

// solve adds a one-member team that solved the challenge at the given moment.
func (f *hiddenFixture) solve(t *testing.T, name string, at time.Time, hidden bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	captainID := mustSeedUser(t, f.db, "hidden-"+name+"@test.test")
	team, err := eventTeamModel.New(f.eventID, captainID, name, "hidden-code-"+name, itNow)
	if err != nil {
		t.Fatal(err)
	}
	if team, err = eventTeamRepo.New(f.db.Queries).Create(ctx, team); err != nil {
		t.Fatal(err)
	}
	if hidden {
		f.setHidden(t, team.ID, true)
	}
	tc, err := f.db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
		ID: uuid.Must(uuid.NewV7()), EventID: f.eventID, EventTeamID: team.ID,
		EventChallengeID: f.challenge.ID, Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", CreatedAt: itNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: tc.ID, SolvedAt: at}); err != nil {
		t.Fatal(err)
	}
	f.teams[name], f.solves[name] = team.ID, tc.ID
	return team.ID
}

func (f *hiddenFixture) setHidden(t *testing.T, teamID uuid.UUID, hidden bool) {
	t.Helper()
	if _, err := f.db.Queries.SetEventTeamHidden(context.Background(), postgres.SetEventTeamHiddenParams{ID: teamID, EventID: f.eventID, Hidden: hidden, UpdatedAt: itNow}); err != nil {
		t.Fatal(err)
	}
}

func (f *hiddenFixture) points(t *testing.T, team string) int32 {
	t.Helper()
	rows, err := f.db.Queries.ListTeamScoreTimeline(context.Background(), f.teams[team])
	if err != nil || len(rows) != 1 {
		t.Fatalf("timeline of %s: %+v, %v", team, rows, err)
	}
	return rows[0].Points
}

func (f *hiddenFixture) page(t *testing.T, own uuid.UUID, cutoff *time.Time, after uuid.UUID, limit int32) []postgres.ListEventChallengeSolvesRow {
	t.Helper()
	params := postgres.ListEventChallengeSolvesParams{EventID: f.eventID, EventChallengeID: f.challenge.ID, OwnTeamID: own, RowLimit: limit}
	if cutoff != nil {
		params.Cutoff = pgtype.Timestamptz{Time: *cutoff, Valid: true}
	}
	if after != uuid.Nil {
		params.AfterID = uuid.NullUUID{UUID: after, Valid: true}
	}
	rows, err := f.db.Queries.ListEventChallengeSolves(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// Visibility is applied at read time: unhiding a team brings back its place in
// the counters, the solvers list, first blood and every dynamic score, exactly
// as if it had always been visible.
func TestHiddenToggleRecomputesEverythingFromStoredSolves(t *testing.T) {
	f := newHiddenFixture(t, "hiddenrecompute")
	ctx := context.Background()
	f.solve(t, "idle-one", itNow.Add(time.Hour), false)
	f.solve(t, "idle-two", itNow.Add(time.Hour), false)
	tester := f.solve(t, "tester", itNow, true) // first solver, hidden
	f.solve(t, "alpha", itNow.Add(time.Minute), false)
	f.solve(t, "bravo", itNow.Add(2*time.Minute), false)

	alphaHiddenTester, bravoHiddenTester := f.points(t, "alpha"), f.points(t, "bravo")
	if alphaHiddenTester <= bravoHiddenTester {
		t.Fatalf("with a hidden first solver alpha is the first visible solve and earns more than bravo: %d vs %d", alphaHiddenTester, bravoHiddenTester)
	}
	counts, err := f.db.Queries.CountEventChallengeSolves(ctx, postgres.CountEventChallengeSolvesParams{EventID: f.eventID, OwnTeamID: uuid.Must(uuid.NewV7())})
	if err != nil || len(counts) != 1 || counts[0].Solves != 4 {
		t.Fatalf("hidden solve must not be counted: %+v, %v", counts, err)
	}
	rows := f.page(t, uuid.Must(uuid.NewV7()), nil, uuid.Nil, 10)
	if len(rows) != 4 || rows[0].TeamName != "alpha" || !rows[0].FirstBlood || rows[1].FirstBlood {
		t.Fatalf("hidden team must not be in the list and first blood is alpha's: %+v", rows)
	}
	if got := f.points(t, "tester"); got != 100 {
		t.Fatalf("a hidden team scores the fixed challenge points, got %d", got)
	}

	onBoard := func() bool {
		board, boardErr := f.db.Queries.ListEventScoreboard(ctx, postgres.ListEventScoreboardParams{EventID: f.eventID})
		if boardErr != nil {
			t.Fatal(boardErr)
		}
		for _, row := range board {
			if row.TeamID == tester {
				return true
			}
		}
		return false
	}
	if onBoard() {
		t.Fatal("a hidden team is absent from the scoreboard")
	}
	f.setHidden(t, tester, false)
	if !onBoard() {
		t.Fatal("an unhidden team is on the scoreboard again")
	}
	if f.points(t, "alpha") >= alphaHiddenTester {
		t.Fatalf("alpha must lose points once the earlier solver is visible: %d -> %d", alphaHiddenTester, f.points(t, "alpha"))
	}
	counts, err = f.db.Queries.CountEventChallengeSolves(ctx, postgres.CountEventChallengeSolvesParams{EventID: f.eventID, OwnTeamID: uuid.Must(uuid.NewV7())})
	if err != nil || counts[0].Solves != 5 {
		t.Fatalf("unhidden solve is counted again: %+v, %v", counts, err)
	}
	rows = f.page(t, uuid.Must(uuid.NewV7()), nil, uuid.Nil, 10)
	if len(rows) != 5 || rows[0].TeamName != "tester" || !rows[0].FirstBlood || rows[1].FirstBlood || rows[2].FirstBlood {
		t.Fatalf("unhidden team becomes first blood: %+v", rows)
	}

	f.setHidden(t, tester, true)
	if f.points(t, "alpha") != alphaHiddenTester || f.points(t, "bravo") != bravoHiddenTester {
		t.Fatal("hiding again must restore the previous scores")
	}
}

func TestChallengeSolversKeysetIsStableAndCutByFreeze(t *testing.T) {
	f := newHiddenFixture(t, "hiddenkeyset")
	// Five solves share one moment: the solve id alone breaks the tie.
	for _, name := range []string{"team1", "team2", "team3", "team4", "team5"} {
		f.solve(t, name, itNow, false)
	}
	late := f.solve(t, "late", itNow.Add(time.Hour), false)
	want := make([]uuid.UUID, 0, 6)
	for _, name := range []string{"team1", "team2", "team3", "team4", "team5"} {
		want = append(want, f.solves[name])
	}
	sort.Slice(want, func(i, j int) bool { return want[i].String() < want[j].String() })
	want = append(want, f.solves["late"])

	var got []uuid.UUID
	after := uuid.Nil
	for pages := 0; pages < 10; pages++ {
		rows := f.page(t, uuid.Must(uuid.NewV7()), nil, after, 2)
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			got = append(got, row.TeamChallengeID)
		}
		after = rows[len(rows)-1].TeamChallengeID
	}
	if len(got) != len(want) {
		t.Fatalf("pages returned %d solves, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order differs at %d: got %v want %v", i, got, want)
		}
	}

	// Freeze: others' solves after the cut-off are gone, the own team's stay.
	cutoff := itNow.Add(time.Minute)
	if rows := f.page(t, uuid.Must(uuid.NewV7()), &cutoff, uuid.Nil, 10); len(rows) != 5 {
		t.Fatalf("freeze must cut the late solve: %d rows", len(rows))
	}
	rows := f.page(t, late, &cutoff, uuid.Nil, 10)
	if len(rows) != 6 || rows[5].EventTeamID != late || rows[5].FirstBlood {
		t.Fatalf("the own late solve stays and is not first blood: %+v", rows)
	}
}

func TestModeratorsTeamMustStayHidden(t *testing.T) {
	f := newHiddenFixture(t, "hiddenmoderators")
	ctx := context.Background()
	ownerID := mustSeedUser(t, f.db, "hidden-owner@test.test")
	_, err := f.db.Pool.Exec(ctx, `INSERT INTO event_teams (id, event_id, name, join_code, captain_id, hidden, member_count, created_at, updated_at,
		individual, admitted_manually, admission_locked, moderators)
		VALUES ($1, $2, 'mods', 'mods-code-1234', $3, false, 1, now(), now(), false, true, true, true)`, uuid.Must(uuid.NewV7()), f.eventID, ownerID)
	assertCheckViolation(t, err)

	teamID := uuid.Must(uuid.NewV7())
	if _, err = f.db.Pool.Exec(ctx, `INSERT INTO event_teams (id, event_id, name, join_code, captain_id, hidden, member_count, created_at, updated_at,
		individual, admitted_manually, admission_locked, moderators)
		VALUES ($1, $2, 'mods', 'mods-code-1234', $3, true, 1, now(), now(), false, true, true, true)`, teamID, f.eventID, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool.Exec(ctx, `UPDATE event_teams SET hidden = false WHERE id = $1`, teamID); err == nil {
		t.Fatal("the moderators team cannot be unhidden")
	}
	affected, err := f.db.Queries.SetEventTeamHidden(ctx, postgres.SetEventTeamHiddenParams{ID: teamID, EventID: f.eventID, Hidden: false, UpdatedAt: itNow})
	if err != nil || affected != 0 {
		t.Fatalf("the toggle query must skip the moderators team: %d, %v", affected, err)
	}
	var visible bool
	if err = f.db.Pool.QueryRow(ctx, `SELECT event_team_visible(hidden, moderators, event_id, individual, admitted_manually, admission_locked, member_count)
		FROM event_teams WHERE id = $1`, teamID).Scan(&visible); err != nil || visible {
		t.Fatalf("the moderators team is never visible: %v, %v", visible, err)
	}
}
