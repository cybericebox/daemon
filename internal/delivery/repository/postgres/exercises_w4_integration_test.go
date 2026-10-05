package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func setHintChargeMode(t *testing.T, db *testhelpers.TestDB, eventID uuid.UUID, mode int) {
	t.Helper()
	tag, err := db.Pool.Exec(context.Background(), `UPDATE event_configs SET hint_charge_mode = $2 WHERE event_id = $1`, eventID, mode)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() == 0 {
		if _, err = db.Queries.CreateEventConfig(context.Background(), postgres.CreateEventConfigParams{EventID: eventID, CreatedAt: itNow, UpdatedAt: pgtype.Timestamptz{Time: itNow, Valid: true},
			MaxTeamSize: 5, StandTeardownDelayMinutes: 60, ResultsFreezeMinutes: 60, ResultsChartTeams: 10, FinishCountdownMinutes: 10, HintChargeMode: int16(mode), TaskRevealMode: "all_ready"}); err != nil {
			t.Fatalf("create event config: %v", err)
		}
	}
}

// TestHintCostsInScoring covers W4 hints in the single scoring formula:
// reward mode (A) reduces the solve by costs unlocked before it, never below
// 0; balance mode (B) adds negative penalty rows that are not solves and that
// a freeze cutoff filters like solves; detached attachments do not score.
func TestHintCostsInScoring(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "hintscoring")
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)
	setHintChargeMode(t, db, event.ID, 0)

	hintID := uuid.Must(uuid.NewV7())
	type seeded struct{ team, teamChallenge uuid.UUID }
	seed := func(suffix string, solvedAt time.Time) seeded {
		captainID := mustSeedUser(t, db, "hint-"+suffix+"@test.test")
		team, err := eventTeamModel.New(event.ID, captainID, "team "+suffix, "hintscore-code-"+suffix, itNow)
		if err != nil {
			t.Fatal(err)
		}
		if team, err = eventTeamRepo.New(db.Queries).Create(ctx, team); err != nil {
			t.Fatal(err)
		}
		tc, err := db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: team.ID,
			EventChallengeID: challenge.ID, Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", CreatedAt: itNow})
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: tc.ID, SolvedAt: solvedAt}); err != nil {
			t.Fatal(err)
		}
		return seeded{team.ID, tc.ID}
	}
	unlock := func(s seeded, at time.Time, cost int32) postgres.CreateHintUnlockRow {
		row, err := db.Queries.CreateHintUnlock(ctx, postgres.CreateHintUnlockParams{TeamChallengeID: s.teamChallenge, HintID: hintID, EventID: event.ID,
			EventTeamID: s.team, EventChallengeID: challenge.ID, UnlockedAt: at, Cost: cost})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	a := seed("a", itNow.Add(20*time.Minute))
	b := seed("b", itNow.Add(30*time.Minute))
	first := unlock(a, itNow.Add(10*time.Minute), 30)
	if !first.Created || first.Cost != 30 {
		t.Fatalf("first unlock = %+v", first)
	}
	if again := unlock(a, itNow.Add(11*time.Minute), 99); again.Created || again.Cost != 30 {
		t.Fatalf("a repeated unlock must return the first one: %+v", again)
	}
	unlock(b, itNow.Add(25*time.Minute), 150)

	points := func(cutoff pgtype.Timestamptz) map[uuid.UUID]postgres.ListEventScoreboardRow {
		rows, err := db.Queries.ListEventScoreboard(ctx, postgres.ListEventScoreboardParams{EventID: event.ID, Cutoff: cutoff})
		if err != nil {
			t.Fatal(err)
		}
		out := map[uuid.UUID]postgres.ListEventScoreboardRow{}
		for _, row := range rows {
			out[row.TeamID] = row
		}
		return out
	}
	reward := points(pgtype.Timestamptz{})
	if reward[a.team].Points != 70 || reward[a.team].Solved != 1 || reward[b.team].Points != 0 || reward[b.team].Solved != 1 {
		t.Fatalf("reward mode: a=%+v b=%+v", reward[a.team], reward[b.team])
	}

	setHintChargeMode(t, db, event.ID, 1)
	balance := points(pgtype.Timestamptz{})
	if balance[a.team].Points != 70 || balance[a.team].Solved != 1 || balance[b.team].Points != -50 || balance[b.team].Solved != 1 {
		t.Fatalf("balance mode: a=%+v b=%+v", balance[a.team], balance[b.team])
	}
	if last, ok := balance[a.team].LastSolveAt.(time.Time); !ok || !last.Equal(itNow.Add(20*time.Minute)) {
		t.Fatalf("penalties are not solves: last solve = %+v", balance[a.team].LastSolveAt)
	}
	frozen := points(pgtype.Timestamptz{Time: itNow.Add(15 * time.Minute), Valid: true})
	if frozen[a.team].Points != -30 || frozen[a.team].Solved != 0 || frozen[b.team].Points != 0 {
		t.Fatalf("freeze filters penalties like solves: a=%+v b=%+v", frozen[a.team], frozen[b.team])
	}
	timeline, err := db.Queries.ListTeamScoreTimeline(ctx, a.team)
	if err != nil || len(timeline) != 2 || timeline[0].Points != -30 || timeline[1].Points != 100 {
		t.Fatalf("team timeline includes penalties: %+v err=%v", timeline, err)
	}

	if _, err = db.Pool.Exec(ctx, `UPDATE event_exercises SET status = 2 WHERE event_id = $1`, event.ID); err != nil {
		t.Fatal(err)
	}
	if detached := points(pgtype.Timestamptz{}); detached[a.team].Points != 0 || detached[a.team].Solved != 0 {
		t.Fatalf("detached exercises do not score: %+v", detached[a.team])
	}
}

// TestExerciseVisibilityAndEventCatalog covers the W4 access rules in SQL:
// catalog access levels, event ownership, the manager read rule, the event
// catalog, and archiving the exercises of a deleted event.
func TestExerciseVisibilityAndEventCatalog(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	eventA := mustSeedEventForParticipants(t, db, "visa")
	eventB := mustSeedEventForParticipants(t, db, "visb")
	manager := mustSeedUser(t, db, "vis-manager@test.test")
	if _, err := db.Queries.CreateEventManager(ctx, postgres.CreateEventManagerParams{EventID: eventA.ID, UserID: manager, Role: 1, CreatedAt: itNow}); err != nil {
		t.Fatal(err)
	}
	publish := func(e exerciseModel.Exercise) exerciseModel.Exercise {
		created, err := repo.Create(ctx, e)
		if err != nil {
			t.Fatalf("create %q: %v", e.Name, err)
		}
		if _, err = repo.UpsertDraft(ctx, created.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: itVariants()}, itNow, uuid.NullUUID{}); err != nil {
			t.Fatal(err)
		}
		if _, err = repo.Publish(ctx, created.ID, itNow); err != nil {
			t.Fatal(err)
		}
		return created
	}
	all, _ := exerciseModel.NewExercise("Open to all", "", []string{"web", "crypto"}, uuid.Nil, itNow)
	selected, _ := exerciseModel.NewExercise("Only selected", "", []string{"pwn"}, uuid.Nil, itNow)
	selected.AccessLevel = exerciseModel.AccessSelectedEvents
	origin, _ := exerciseModel.NewExercise("Only origin", "", nil, uuid.Nil, itNow)
	origin.AccessLevel, origin.OriginEventID = exerciseModel.AccessOriginEvent, uuid.NullUUID{UUID: eventB.ID, Valid: true}
	own, _ := exerciseModel.NewEventExercise("Open to all", "", []string{"web"}, eventA.ID, uuid.Nil, itNow)
	other, _ := exerciseModel.NewEventExercise("Other event", "", nil, eventB.ID, uuid.Nil, itNow)
	all, selected, origin, own, other = publish(all), publish(selected), publish(origin), publish(own), publish(other)
	if err := repo.ReplaceAccessEvents(ctx, selected.ID, []uuid.UUID{eventA.ID}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name     string
		id       uuid.UUID
		event    uuid.UUID
		readable bool
		want     bool
	}{
		{"all levels admit any event", all.ID, eventB.ID, true, true},
		{"selected admits a listed event", selected.ID, eventA.ID, true, true},
		{"selected refuses others", selected.ID, eventB.ID, true, false},
		{"origin admits only its origin", origin.ID, eventA.ID, false, false},
		{"the owner event", own.ID, eventA.ID, true, true},
		{"another event's exercise", other.ID, eventA.ID, false, false},
	} {
		available, err := repo.AvailableToEvent(ctx, tc.id, tc.event)
		if err != nil || available != tc.want {
			t.Fatalf("%s: available=%t err=%v", tc.name, available, err)
		}
		readable, err := repo.ReadableBy(ctx, tc.id, manager)
		if err != nil || readable != tc.readable {
			t.Fatalf("%s: readable=%t err=%v", tc.name, readable, err)
		}
	}

	page, err := repo.ListPage(ctx, exerciseRepo.PageParams{Tags: []string{}, SortBy: "name", SortDir: "asc", Limit: 20, Archived: "exclude",
		Visibility: exerciseRepo.Visibility{ViewerID: uuid.NullUUID{UUID: manager, Valid: true}}})
	if err != nil || len(page) != 3 {
		t.Fatalf("a manager lists own + available catalog exercises: %d err=%v", len(page), err)
	}

	catalog, err := db.Queries.ListEventCatalog(ctx, postgres.ListEventCatalogParams{EventID: eventA.ID})
	if err != nil || len(catalog) != 3 || catalog[0].ID != own.ID || catalog[0].Scope != 1 {
		t.Fatalf("event catalog = own first, then available catalog: %+v err=%v", catalog, err)
	}

	// Tag filter: union of the given tags, ANDed with the other filters; nil
	// tags mean no filter.
	for _, tc := range []struct {
		name string
		tags []string
		want int
	}{
		{"web matches own and open", []string{"web"}, 2},
		{"union of web and pwn", []string{"web", "pwn"}, 3},
		{"crypto matches one", []string{"crypto"}, 1},
		{"unknown tag matches none", []string{"nope"}, 0},
		{"empty list is no filter", []string{}, 3},
	} {
		got, listErr := db.Queries.ListEventCatalog(ctx, postgres.ListEventCatalogParams{EventID: eventA.ID, Tags: tc.tags})
		if listErr != nil || len(got) != tc.want {
			t.Fatalf("%s: %d rows err=%v", tc.name, len(got), listErr)
		}
	}
	tagged, err := db.Queries.ListEventCatalog(ctx, postgres.ListEventCatalogParams{EventID: eventA.ID, Search: "selected", Tags: []string{"web", "pwn"}})
	if err != nil || len(tagged) != 1 || tagged[0].ID != selected.ID {
		t.Fatalf("tags AND search: %+v err=%v", tagged, err)
	}
	suggest, err := db.Queries.ListEventCatalogTags(ctx, postgres.ListEventCatalogTagsParams{EventID: eventA.ID, LimitVal: 10})
	if err != nil || len(suggest) != 3 || suggest[0].Tag != "web" || suggest[0].ExerciseCount != 2 {
		t.Fatalf("event tag suggestions, most used first: %+v err=%v", suggest, err)
	}
	prefixed, err := db.Queries.ListEventCatalogTags(ctx, postgres.ListEventCatalogTagsParams{EventID: eventA.ID, Prefix: "P", LimitVal: 10})
	if err != nil || len(prefixed) != 1 || prefixed[0].Tag != "pwn" {
		t.Fatalf("prefix is case-insensitive: %+v err=%v", prefixed, err)
	}
	limited, err := db.Queries.ListEventCatalogTags(ctx, postgres.ListEventCatalogTagsParams{EventID: eventB.ID, LimitVal: 1})
	if err != nil || len(limited) != 1 {
		t.Fatalf("limit and per-event scope: %+v err=%v", limited, err)
	}

	if err = repo.ArchiveOwnedBy(ctx, eventA.ID, itNow); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Queries.DeleteEvent(ctx, eventA.ID); err != nil {
		t.Fatal(err)
	}
	orphan, err := repo.GetByID(ctx, own.ID)
	if err != nil || orphan.ArchivedAt == nil || orphan.OwnerEventID.Valid || orphan.Scope != exerciseModel.ScopeEvent {
		t.Fatalf("a deleted event's exercise stays an archived event exercise: %+v err=%v", orphan, err)
	}
}
