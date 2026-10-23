package event_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventManagerRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

// variantEvent is a static-only team event with one per-team task of three
// variants; teams join through the real stand engine pass, which is the code
// path that gives a team its variant (prepareTeamAssignment → pickTeamVariant).
type variantEvent struct {
	f         *standFixture
	uc        *event.EventUseCase
	challenge uuid.UUID
	newTeam   func(name string) uuid.UUID
}

func newVariantEvent(t *testing.T, weights []int) *variantEvent {
	t.Helper()
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now()
	users := userRepo.New(db.Queries)
	newUser := func(email string) uuid.UUID {
		u, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), email, now))
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	ownerID := newUser("owner@variants.test")
	e, err := eventModel.NewEvent("variants", "Variants", now.Add(-24*time.Hour), time.Time{}, ownerID, now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := eventRepo.New(db.Queries).Create(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	config := eventConfigModel.NewEventConfig(created.ID, now)
	team := eventConfigModel.ParticipationTeam
	config.Participation = &team
	if _, err = eventConfigRepo.New(db.Queries).Create(ctx, config); err != nil {
		t.Fatal(err)
	}
	owner, err := eventManagerModel.New(created.ID, ownerID, eventManagerModel.RoleOwner, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventManagerRepo.New(db.Queries).Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	f := &standFixture{db: db, eventID: created.ID, ownerID: ownerID}
	f.shiftLifecycle(t, 10*time.Minute, 2*time.Hour)
	if _, err = db.Pool.Exec(ctx, `UPDATE events SET infrastructure_allowed = false WHERE id = $1`, created.ID); err != nil {
		t.Fatal(err)
	}

	// Three variants of one static task (the same task ID in each, as the editor writes it).
	exercises := exerciseRepo.New(db.Queries)
	value, err := exerciseModel.NewExercise("Variant task "+uuid.Must(uuid.NewV4()).String()[:8], "desc", []string{"web"}, uuid.Nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if value, err = exercises.Create(ctx, value); err != nil {
		t.Fatal(err)
	}
	taskID := uuid.Must(uuid.NewV7())
	variants := make([]exerciseModel.Variant, 3)
	for i := range variants {
		variants[i] = exerciseModel.Variant{ID: uuid.Must(uuid.NewV7()), Index: int32(i), Tasks: []exerciseModel.Task{{
			ID: taskID, Name: "Find the flag", Description: json.RawMessage(`{"blocks":[]}`),
			Difficulty: exerciseModel.DifficultyEasy, Flag: []string{fmt.Sprintf("ICE{v%d}", i)},
		}}}
		variants[i].Topology.Devices = []exerciseModel.Device{}
		if i < len(weights) {
			variants[i].Weight = weights[i]
		}
	}
	draft, err := exercises.UpsertDraft(ctx, value.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: variants}, now, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exercises.Publish(ctx, value.ID, now); err != nil {
		t.Fatal(err)
	}
	attachment, err := db.Queries.CreateEventExercise(ctx, postgres.CreateEventExerciseParams{
		ID: uuid.Must(uuid.NewV7()), EventID: created.ID, ExerciseID: value.ID, ExerciseVersionID: draft.ID,
		VariantMode: 0, FixedVariantIndex: pgtype.Int4{}, Revision: 1, CreatedAt: now, // per team
	})
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := db.Queries.CreateEventChallenge(ctx, postgres.CreateEventChallengeParams{
		ID: uuid.Must(uuid.NewV7()), EventExerciseID: attachment.ID, TaskID: taskID,
		Points: 100, Published: true, Snapshot: []byte(`{"name":"Variant task"}`), CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	teams := eventTeamRepo.New(db.Queries)
	return &variantEvent{
		f: f, challenge: challenge.ID,
		uc: event.NewEventUseCase(event.Dependencies{
			Repo: db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(db.Pool)),
			SignalPublishers: event.NewOutboxSignalPublisherFactory(time.Now),
		}),
		newTeam: func(name string) uuid.UUID {
			value, err := eventTeamModel.New(created.ID, newUser(name+"@variants.test"), name, "variant-code-"+name, now)
			if err != nil {
				t.Fatal(err)
			}
			value.AdmittedManually = true
			value.FormedAt = &now
			stored, err := teams.Create(ctx, value)
			if err != nil {
				t.Fatal(err)
			}
			return stored.ID
		},
	}
}

func (v *variantEvent) pass(t *testing.T) {
	t.Helper()
	if err := v.uc.ReconcileEventStands(context.Background()); err != nil {
		t.Fatalf("ReconcileEventStands: %v", err)
	}
}

func (v *variantEvent) variantOf(t *testing.T, teamID uuid.UUID) int32 {
	t.Helper()
	var index int32
	if err := v.f.db.Pool.QueryRow(context.Background(), `SELECT variant_index FROM team_challenges WHERE event_team_id = $1 AND event_challenge_id = $2`, teamID, v.challenge).Scan(&index); err != nil {
		t.Fatalf("read variant of team %s: %v", teamID, err)
	}
	return index
}

// distribution counts the non-moderator teams per variant.
func (v *variantEvent) distribution(t *testing.T) [3]int {
	t.Helper()
	rows, err := v.f.db.Pool.Query(context.Background(), `SELECT tc.variant_index, count(*) FROM team_challenges tc
		JOIN event_teams team ON team.id = tc.event_team_id WHERE tc.event_challenge_id = $1 AND NOT team.moderators GROUP BY 1`, v.challenge)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [3]int
	for rows.Next() {
		var index int32
		var n int
		if err = rows.Scan(&index, &n); err != nil {
			t.Fatal(err)
		}
		out[index] = n
	}
	return out
}

// oldRule is the origin/develop pick: the fewest teams, ties to the lowest index.
func oldRule(counts []int) int32 {
	best := 0
	for i, n := range counts {
		if n < counts[best] {
			best = i
		}
	}
	return int32(best)
}

// Teams join one by one: each gets exactly what the old rule gives (round robin, fewest first).
func TestVariantPick_Integration_NoWeightsMatchesOldRule(t *testing.T) {
	for _, weights := range [][]int{nil, {0, 0, 0}, {1, 1, 1}} {
		t.Run(fmt.Sprint(weights), func(t *testing.T) {
			v := newVariantEvent(t, weights)
			counts := []int{0, 0, 0}
			var got []int32
			for i := range 10 {
				team := v.newTeam(fmt.Sprintf("solo%d", i))
				v.pass(t)
				index := v.variantOf(t, team)
				if want := oldRule(counts); index != want {
					t.Fatalf("team %d got variant %d, old rule gives %d (so far %v)", i, index, want, got)
				}
				counts[index]++
				got = append(got, index)
			}
			if want := []int32{0, 1, 2, 0, 1, 2, 0, 1, 2, 0}; fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("sequence %v, want %v", got, want)
			}
			// A batch of teams joining in one pass lands on the same even split.
			for i := range 8 {
				v.newTeam(fmt.Sprintf("batch%d", i))
			}
			v.pass(t)
			if d := v.distribution(t); d != [3]int{6, 6, 6} {
				t.Fatalf("18 teams: distribution %v, want [6 6 6]", d)
			}
		})
	}
}

// The same path with weights 2:1:1 follows teams ÷ weight.
func TestVariantPick_Integration_WeightedFollowsWeights(t *testing.T) {
	v := newVariantEvent(t, []int{2, 1, 1})
	var got []int32
	for i := range 9 {
		team := v.newTeam(fmt.Sprintf("solo%d", i))
		v.pass(t)
		got = append(got, v.variantOf(t, team))
	}
	if want := []int32{0, 1, 2, 0, 0, 1, 2, 0, 0}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("weighted sequence %v, want %v", got, want)
	}
	for i := range 7 {
		v.newTeam(fmt.Sprintf("batch%d", i))
	}
	v.pass(t)
	if d := v.distribution(t); d != [3]int{8, 4, 4} {
		t.Fatalf("16 teams with 2:1:1: distribution %v, want [8 4 4]", d)
	}
}
