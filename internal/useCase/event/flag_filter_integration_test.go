package event_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
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

type flagFilterRow struct {
	Exercise uuid.UUID
	Mode     int16
	Salt     []byte
	Size     int64
	Inserted int32
	Capacity int32
	Resets   int32
}

// Static teams on two tasks (a bitmap-sized one and a bloom-sized one) of an
// event with a team limit: the stand pass issues distinct flags through the
// stored filters, moderators included; raising the limit rewrites every filter
// of the event (new salt, new capacity, the issued flags kept) and later teams
// still get distinct flags.
func TestFlagFilters_IssueDistinctAndRebuildOnTeamLimitChange(t *testing.T) {
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
	ownerID := newUser("owner@flags.test")
	e, err := eventModel.NewEvent("flags", "Flags", now.Add(-24*time.Hour), time.Time{}, ownerID, now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := eventRepo.New(db.Queries).Create(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	eventID := created.ID
	config := eventConfigModel.NewEventConfig(eventID, now)
	team := eventConfigModel.ParticipationTeam
	config.Participation = &team
	limit := int32(4)
	config.MaxTeams = &limit
	if _, err = eventConfigRepo.New(db.Queries).Create(ctx, config); err != nil {
		t.Fatal(err)
	}
	owner, err := eventManagerModel.New(eventID, ownerID, eventManagerModel.RoleOwner, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventManagerRepo.New(db.Queries).Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE events SET lifecycle_configured = true, publish_at = $2, start_at = $3, finish_at = $4, withdraw_at = $5 WHERE id = $1`,
		eventID, now.Add(-48*time.Hour), now.Add(10*time.Minute), now.Add(2*time.Hour), now.Add(26*time.Hour)); err != nil {
		t.Fatal(err)
	}
	exercises := exerciseRepo.New(db.Queries)
	attach := func(name, candidate string) uuid.UUID {
		value, newErr := exerciseModel.NewExercise(name, "desc", []string{"web"}, uuid.Nil, now)
		if newErr != nil {
			t.Fatal(newErr)
		}
		if value, newErr = exercises.Create(ctx, value); newErr != nil {
			t.Fatal(newErr)
		}
		variant := exerciseModel.Variant{Tasks: []exerciseModel.Task{{ID: uuid.Must(uuid.NewV7()), Name: "Find the flag", Description: json.RawMessage(`{"blocks":[]}`),
			Difficulty: exerciseModel.DifficultyEasy, Flag: []string{candidate}}}}
		variant.Topology.Devices = []exerciseModel.Device{}
		draft, draftErr := exercises.UpsertDraft(ctx, value.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{variant}}, now, uuid.NullUUID{})
		if draftErr != nil {
			t.Fatal(draftErr)
		}
		if _, err = exercises.Publish(ctx, value.ID, now); err != nil {
			t.Fatal(err)
		}
		attachment, attachErr := db.Queries.CreateEventExercise(ctx, postgres.CreateEventExerciseParams{ID: uuid.Must(uuid.NewV7()), EventID: eventID, ExerciseID: value.ID,
			ExerciseVersionID: draft.ID, VariantMode: 1, FixedVariantIndex: pgtype.Int4{Int32: 0, Valid: true}, Revision: 1, CreatedAt: now})
		if attachErr != nil {
			t.Fatal(attachErr)
		}
		if _, err = db.Queries.CreateEventChallenge(ctx, postgres.CreateEventChallengeParams{ID: uuid.Must(uuid.NewV7()), EventExerciseID: attachment.ID,
			TaskID: variant.Tasks[0].ID, Points: 100, Published: true, Snapshot: []byte(`{"name":"Task"}`), CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		return attachment.ID
	}
	small := attach("Digits", `template:ICE{\d}`)      // N=10 <= 64 bits: bitmap
	large := attach("Letters", `template:ICE{\l\l\l}`) // N=17576: bloom
	teams := eventTeamRepo.New(db.Queries)
	addTeams := func(from, to int) {
		for i := from; i < to; i++ {
			name := "Team" + strconv.Itoa(i)
			value, newErr := eventTeamModel.New(eventID, newUser("t"+strconv.Itoa(i)+"@flags.test"), name, "flags-code-"+name, now)
			if newErr != nil {
				t.Fatal(newErr)
			}
			value.AdmittedManually, value.FormedAt = true, &now
			if _, newErr = teams.Create(ctx, value); newErr != nil {
				t.Fatal(newErr)
			}
		}
	}
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(db.Pool)),
		SignalPublishers: event.NewOutboxSignalPublisherFactory(time.Now),
	})
	filters := func() map[uuid.UUID]flagFilterRow {
		rows, queryErr := db.Pool.Query(ctx, `SELECT event_exercise_id, mode, salt, size, inserted, capacity, resets FROM event_flag_filters`)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		defer rows.Close()
		out := map[uuid.UUID]flagFilterRow{}
		for rows.Next() {
			var r flagFilterRow
			if scanErr := rows.Scan(&r.Exercise, &r.Mode, &r.Salt, &r.Size, &r.Inserted, &r.Capacity, &r.Resets); scanErr != nil {
				t.Fatal(scanErr)
			}
			out[r.Exercise] = r
		}
		return out
	}
	distinct := func(exerciseID uuid.UUID) int {
		var all, unique int
		if scanErr := db.Pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT tc.expected_flag) FROM team_challenges tc
			JOIN event_challenges ec ON ec.id = tc.event_challenge_id WHERE ec.event_exercise_id = $1`, exerciseID).Scan(&all, &unique); scanErr != nil {
			t.Fatal(scanErr)
		}
		if all != unique {
			t.Fatalf("exercise %s: %d flags, %d distinct", exerciseID, all, unique)
		}
		return all
	}

	addTeams(0, 5)
	if err = uc.ReconcileEventStands(ctx); err != nil {
		t.Fatal(err)
	}
	if n := distinct(small); n != 6 { // five teams and the moderators
		t.Fatalf("small task flags = %d, want 6", n)
	}
	if n := distinct(large); n != 6 {
		t.Fatalf("large task flags = %d, want 6", n)
	}
	before := filters()
	if b := before[small]; b.Mode != 2 || b.Size != 10 || b.Inserted != 6 || b.Capacity != 4 {
		t.Fatalf("small filter = %+v", b)
	}
	if b := before[large]; b.Mode != 1 || b.Size != 64 || b.Inserted != 6 || b.Capacity != 8 {
		t.Fatalf("large filter = %+v (doubled once full)", b)
	}

	view, err := uc.GetEventConfig(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	raised := int32(50)
	if _, err = uc.UpdateEventConfig(ctx, eventID, event.UpdateConfigInput{Participation: view.Participation, Registration: view.Registration,
		ScoreboardVisibility: view.ScoreboardVisibility, ParticipantsVisibility: view.ParticipantsVisibility, PreviewDescription: view.PreviewDescription,
		PreviewPicture: view.PreviewPicture, MaxTeamSize: view.MaxTeamSize, MinTeamSize: view.MinTeamSize, MaxTeams: &raised}, ownerID); err != nil {
		t.Fatal(err)
	}
	after := filters()
	for id, b := range before {
		a := after[id]
		if a.Capacity != 50 || a.Inserted != 6 || bytes.Equal(a.Salt, b.Salt) {
			t.Fatalf("filter of %s after the limit change = %+v (before %+v)", id, a, b)
		}
	}
	if a := after[large]; a.Mode != 1 || a.Size != 256 {
		t.Fatalf("large filter at 50 teams = %+v", a)
	}

	addTeams(5, 9)
	if err = uc.ReconcileEventStands(ctx); err != nil {
		t.Fatal(err)
	}
	if n := distinct(small); n != 10 {
		t.Fatalf("small task flags = %d, want 10", n)
	}
	if n := distinct(large); n != 10 {
		t.Fatalf("large task flags = %d, want 10", n)
	}
	// The manage list warns about the task with fewer flags than teams only.
	items, err := uc.ListEventExercises(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	warned := map[uuid.UUID]bool{}
	for _, item := range items {
		warned[item.ID] = item.FlagsRepeat != nil
	}
	if !warned[small] || warned[large] {
		t.Fatalf("flag warnings = %v (small %s, large %s)", warned, small, large)
	}
	// One more team on the ten digits: every value is used, the bitmap starts a new round.
	addTeams(9, 10)
	if err = uc.ReconcileEventStands(ctx); err != nil {
		t.Fatal(err)
	}
	if r := filters()[small]; r.Resets != 1 || r.Inserted != 1 {
		t.Fatalf("small filter after the eleventh flag = %+v", r)
	}
}
