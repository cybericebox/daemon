package event

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/flagpattern"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

var flagSlotTestSecret = []byte("fixed-flag-slot-secret-for-tests")

func slotFlag(t *testing.T, candidates []string, eventExerciseID, taskID uuid.UUID, variant int32, slot int64) string {
	t.Helper()
	flag, err := flagpattern.ResolveAt(candidates, big.NewInt(slot), teamChallengeModel.FlagSlotKey(flagSlotTestSecret, eventExerciseID, taskID, variant))
	if err != nil {
		t.Fatal(err)
	}
	return flag
}

// 20 teams on 2 variants of a task with N=10 values: the teams on each variant
// get 10 distinct flags. The moderators team takes no slot, and every count is
// read after the pick lock.
func TestTeamFlags_TwentyTeamsTwoVariantsTenValues(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	link := perTeamLink()
	task := exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task", Flag: []string{`template:ICE{\d}`}}
	version := exerciseModel.ExerciseVersion{ID: uuid.Must(uuid.NewV7()), Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{task}}, {Tasks: []exerciseModel.Task{task}}}}
	challenge := eventChallengeModel.EventChallenge{ID: uuid.Must(uuid.NewV7()), EventExerciseID: link.ID, TaskID: task.ID}

	onVariant := map[int32]int{} // the database: teams per variant, moderators excluded
	moderators := uuid.Must(uuid.NewV7())
	locked := false
	q.EXPECT().GetTeamEventExerciseVariant(gomock.Any(), gomock.Any()).Return(int32(0), pgx.ErrNoRows).AnyTimes()
	q.EXPECT().LockEventExerciseVariantPick(gomock.Any(), link.ID).DoAndReturn(func(context.Context, uuid.UUID) error {
		locked = true
		return nil
	}).AnyTimes()
	q.EXPECT().CountEventExerciseVariantTeams(gomock.Any(), link.ID).DoAndReturn(func(context.Context, uuid.UUID) ([]postgres.CountEventExerciseVariantTeamsRow, error) {
		if !locked {
			t.Fatal("variant teams counted before the pick lock")
		}
		var rows []postgres.CountEventExerciseVariantTeamsRow
		for index, teams := range onVariant {
			rows = append(rows, postgres.CountEventExerciseVariantTeamsRow{VariantIndex: index, Teams: int64(teams)})
		}
		return rows, nil
	}).AnyTimes()
	flags := map[int32]map[string]bool{0: {}, 1: {}}
	q.EXPECT().CreateTeamChallenge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateTeamChallengeParams) (postgres.TeamChallenge, error) {
		if arg.EventTeamID != moderators {
			if flags[arg.VariantIndex][arg.ExpectedFlag] {
				t.Fatalf("variant %d repeats %q", arg.VariantIndex, arg.ExpectedFlag)
			}
			flags[arg.VariantIndex][arg.ExpectedFlag] = true
			onVariant[arg.VariantIndex]++
		}
		return postgres.TeamChallenge{ID: arg.ID}, nil
	}).Times(21)

	u := &EventUseCase{flagRandomBytes: 20, flagSlotSecret: flagSlotTestSecret}
	repo := teamChallengeRepo.New(q)
	tasks := map[uuid.UUID]exerciseModel.Task{task.ID: task}
	prepare := func(teamID uuid.UUID) {
		locked = false // one transaction per team
		slots := newTeamFlagSlots(repo, link.ID)
		index, err := pickTeamVariant(context.Background(), repo, teamID, link, version, slots)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = u.newTeamChallenge(context.Background(), repo, slots, link.EventID, teamID, challenge, tasks, index, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 20 {
		if i == 5 {
			prepare(moderators)
		}
		prepare(uuid.Must(uuid.NewV7()))
	}
	for variant, seen := range flags {
		if len(seen) != 10 {
			t.Fatalf("variant %d got %d distinct flags, want 10", variant, len(seen))
		}
	}
}

// A team that keeps its variant takes the lock, then counts the teams on that
// variant that got the event exercise before it.
func TestTeamFlags_KeptVariantLocksThenCountsEarlierTeams(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	link, teamID := perTeamLink(), uuid.Must(uuid.NewV7())
	task := exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task", Flag: []string{`ICE{a}`, `template:ICE{[ab][0-2]}`}}
	version := exerciseModel.ExerciseVersion{ID: uuid.Must(uuid.NewV7()), Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{task}}, {Tasks: []exerciseModel.Task{task}}}}
	challenge := eventChallengeModel.EventChallenge{ID: uuid.Must(uuid.NewV7()), EventExerciseID: link.ID, TaskID: task.ID}
	var written string
	gomock.InOrder(
		q.EXPECT().GetTeamEventExerciseVariant(gomock.Any(), gomock.Any()).Return(int32(1), nil),
		q.EXPECT().LockEventExerciseVariantPick(gomock.Any(), link.ID).Return(nil),
		q.EXPECT().CountEarlierEventExerciseVariantTeams(gomock.Any(), postgres.CountEarlierEventExerciseVariantTeamsParams{EventExerciseID: link.ID, VariantIndex: 1, EventTeamID: teamID}).Return(int64(4), nil),
		q.EXPECT().CreateTeamChallenge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateTeamChallengeParams) (postgres.TeamChallenge, error) {
			written = arg.ExpectedFlag
			return postgres.TeamChallenge{ID: arg.ID}, nil
		}),
	)
	u := &EventUseCase{flagRandomBytes: 20, flagSlotSecret: flagSlotTestSecret}
	repo := teamChallengeRepo.New(q)
	slots := newTeamFlagSlots(repo, link.ID)
	index, err := pickTeamVariant(context.Background(), repo, teamID, link, version, slots)
	if err != nil || index != 1 {
		t.Fatalf("kept variant = %d, %v", index, err)
	}
	if _, err = u.newTeamChallenge(context.Background(), repo, slots, link.EventID, teamID, challenge, map[uuid.UUID]exerciseModel.Task{task.ID: task}, index, time.Now()); err != nil {
		t.Fatal(err)
	}
	if want := slotFlag(t, task.Flag, link.ID, task.ID, 1, 4); written != want {
		t.Fatalf("flag = %q, want slot 4's %q", written, want)
	}
}

// A task without candidates keeps the full random mode and reads no slot.
func TestNewTeamChallenge_NoCandidatesStaysRandom(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl) // strict: no lock, no count
	task := exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task"}
	challenge := eventChallengeModel.EventChallenge{ID: uuid.Must(uuid.NewV7()), EventExerciseID: uuid.Must(uuid.NewV7()), TaskID: task.ID}
	var written string
	q.EXPECT().CreateTeamChallenge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateTeamChallengeParams) (postgres.TeamChallenge, error) {
		written = arg.ExpectedFlag
		return postgres.TeamChallenge{ID: arg.ID}, nil
	})
	u := &EventUseCase{flagRandomBytes: 20, flagSlotSecret: flagSlotTestSecret}
	repo := teamChallengeRepo.New(q)
	if _, err := u.newTeamChallenge(context.Background(), repo, newTeamFlagSlots(repo, challenge.EventExerciseID), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), challenge, map[uuid.UUID]exerciseModel.Task{task.ID: task}, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(written) != len("ICE{}")+40 {
		t.Fatalf("random flag = %q", written)
	}
}

// snapshotOf is the team variant snapshot of refresh rows (one row per team,
// first rows in row order).
func snapshotOf(rows []postgres.ListTeamChallengesForRefreshRow) []postgres.ListEventExerciseTeamVariantsRow {
	out := make([]postgres.ListEventExerciseTeamVariantsRow, 0, len(rows))
	base := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	for i, r := range rows {
		out = append(out, postgres.ListEventExerciseTeamVariantsRow{EventTeamID: r.EventTeamID, VariantIndex: r.VariantIndex, Moderators: r.Moderators,
			FirstCreatedAt: base.Add(time.Duration(i) * time.Minute), FirstID: r.ID})
	}
	return out
}

// A source switch to a fixed variant moves several teams onto one variant in
// one pass: each moved team takes the snapshot count plus its order in the
// pass, the kept team keeps slot 0, and the pass's own row updates never shift
// a count (no per-team query; strict mock).
func TestRefreshTeamAssignments_TeamsMovedOntoOneVariantGetDistinctSlots(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	fixed := int32(0)
	link := perTeamLink()
	link.VariantMode, link.FixedVariantIndex = eventExerciseModel.VariantModeFixed, &fixed
	oldTask := exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task", Flag: []string{`ICE{old}`}}
	task := oldTask
	task.Flag = []string{`template:ICE{\d}`}
	previous := exerciseModel.ExerciseVersion{ID: uuid.Must(uuid.NewV7())}
	next := exerciseModel.ExerciseVersion{ID: uuid.Must(uuid.NewV7())}
	for range 4 {
		previous.Variants = append(previous.Variants, exerciseModel.Variant{Tasks: []exerciseModel.Task{oldTask}})
	}
	next.Variants = []exerciseModel.Variant{{Tasks: []exerciseModel.Task{task}}}
	board := []eventChallengeModel.EventChallenge{{ID: uuid.Must(uuid.NewV7()), EventExerciseID: link.ID, TaskID: task.ID}}
	kept, moverA, moverB, moverC, moderators := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := func(team uuid.UUID, variant int32, mods bool) postgres.ListTeamChallengesForRefreshRow {
		return postgres.ListTeamChallengesForRefreshRow{ID: uuid.Must(uuid.NewV7()), EventTeamID: team, EventChallengeID: board[0].ID, VariantIndex: variant,
			Snapshot: []byte(`{}`), Hints: []byte(`[]`), ExpectedFlag: "ICE{old}", Moderators: mods}
	}
	rows := []postgres.ListTeamChallengesForRefreshRow{row(kept, 0, false), row(moverA, 1, false), row(moderators, 3, true), row(moverB, 2, false), row(moverC, 3, false)}
	want := map[uuid.UUID]string{
		kept:       slotFlag(t, task.Flag, link.ID, task.ID, 0, 0),
		moverA:     slotFlag(t, task.Flag, link.ID, task.ID, 0, 1),
		moderators: slotFlag(t, task.Flag, link.ID, task.ID, 0, 2),
		moverB:     slotFlag(t, task.Flag, link.ID, task.ID, 0, 2),
		moverC:     slotFlag(t, task.Flag, link.ID, task.ID, 0, 3),
	}
	snapshotRead := false
	gomock.InOrder(
		// The pick lock comes before the row locks (the stand preparation's order).
		q.EXPECT().LockEventExerciseVariantPick(gomock.Any(), link.ID).Return(nil),
		q.EXPECT().ListTeamChallengesForRefresh(gomock.Any(), gomock.Any()).Return(rows, nil),
		q.EXPECT().ListEventExerciseTeamVariants(gomock.Any(), link.ID).DoAndReturn(func(context.Context, uuid.UUID) ([]postgres.ListEventExerciseTeamVariantsRow, error) {
			snapshotRead = true
			return snapshotOf(rows), nil
		}),
	)
	team := map[uuid.UUID]uuid.UUID{}
	for _, r := range rows {
		team[r.ID] = r.EventTeamID
	}
	got := map[uuid.UUID]string{}
	q.EXPECT().UpdateTeamChallengeContent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateTeamChallengeContentParams) (int64, error) {
		if !snapshotRead {
			t.Fatal("a row was updated before the slot snapshot was read")
		}
		got[team[arg.ID]] = arg.ExpectedFlag
		return 1, nil
	}).Times(len(rows))
	u := &EventUseCase{flagRandomBytes: 20, flagSlotSecret: flagSlotTestSecret}
	if err := u.refreshTeamAssignments(context.Background(), q, link, previous, next, board, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for id, flag := range want {
		if got[id] != flag {
			t.Fatalf("flags = %v, want %v", got, want)
		}
		if id != moderators {
			if seen[flag] {
				t.Fatalf("regular teams share %q", flag)
			}
			seen[flag] = true
		}
	}
}

// A source switch re-picks dropped variants: each re-picked team's slot is the
// balance count of its new variant, including the picks made earlier in the pass.
func TestRefreshTeamAssignments_RepickSlotsCountThePass(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	link := perTeamLink()
	task := exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task", Flag: []string{`template:ICE{\d}`}}
	next := exerciseModel.ExerciseVersion{ID: uuid.Must(uuid.NewV7())}
	for range 3 {
		next.Variants = append(next.Variants, exerciseModel.Variant{Tasks: []exerciseModel.Task{task}})
	}
	board := []eventChallengeModel.EventChallenge{{ID: uuid.Must(uuid.NewV7()), EventExerciseID: link.ID, TaskID: task.ID}}
	teamA, teamB, teamC, teamD, teamE := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := func(team uuid.UUID, variant int32) postgres.ListTeamChallengesForRefreshRow {
		return postgres.ListTeamChallengesForRefreshRow{ID: uuid.Must(uuid.NewV7()), EventTeamID: team, EventChallengeID: board[0].ID, VariantIndex: variant,
			Snapshot: []byte(`{}`), Hints: []byte(`[]`), ExpectedFlag: "ICE{old}"}
	}
	rows := []postgres.ListTeamChallengesForRefreshRow{row(teamA, 0), row(teamB, 7), row(teamC, 8), row(teamD, 9), row(teamE, 10)}
	// Balance {0:1}: B→1 (slot 0), C→2 (slot 0), D→0 (slot 1, A counted), E→1 (slot 1, B counted in the pass).
	want := map[uuid.UUID]string{
		teamB: slotFlag(t, task.Flag, link.ID, task.ID, 1, 0),
		teamC: slotFlag(t, task.Flag, link.ID, task.ID, 2, 0),
		teamD: slotFlag(t, task.Flag, link.ID, task.ID, 0, 1),
		teamE: slotFlag(t, task.Flag, link.ID, task.ID, 1, 1),
	}
	gomock.InOrder(
		// The pick lock comes before the row locks (the stand preparation's order).
		q.EXPECT().LockEventExerciseVariantPick(gomock.Any(), link.ID).Return(nil),
		q.EXPECT().ListTeamChallengesForRefresh(gomock.Any(), gomock.Any()).Return(rows, nil),
		q.EXPECT().ListEventExerciseTeamVariants(gomock.Any(), link.ID).Return(snapshotOf(rows), nil),
		q.EXPECT().CountEventExerciseVariantTeams(gomock.Any(), link.ID).Return([]postgres.CountEventExerciseVariantTeamsRow{
			{VariantIndex: 0, Teams: 1}, {VariantIndex: 7, Teams: 1}, {VariantIndex: 8, Teams: 1}, {VariantIndex: 9, Teams: 1}, {VariantIndex: 10, Teams: 1},
		}, nil),
	)
	team := map[uuid.UUID]uuid.UUID{}
	for _, r := range rows {
		team[r.ID] = r.EventTeamID
	}
	got := map[uuid.UUID]string{}
	q.EXPECT().UpdateTeamChallengeContent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateTeamChallengeContentParams) (int64, error) {
		got[team[arg.ID]] = arg.ExpectedFlag
		return 1, nil
	}).Times(len(rows))
	u := &EventUseCase{flagRandomBytes: 20, flagSlotSecret: flagSlotTestSecret}
	if err := u.refreshTeamAssignments(context.Background(), q, link, next, next, board, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got[teamA] != "ICE{old}" {
		t.Fatalf("kept team A flag changed to %q", got[teamA])
	}
	for id, flag := range want {
		if got[id] != flag {
			t.Fatalf("flags = %v, want %v", got, want)
		}
	}
}
