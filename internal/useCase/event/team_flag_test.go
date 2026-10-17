package event

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

type flagKey struct {
	exercise uuid.UUID
	variant  int32
	task     uuid.UUID
}

// flagStore is the database of the flag filter queries: the filter rows and
// the stored expected flags. Every filter read or write must come after the
// variant pick lock of its transaction.
type flagStore struct {
	t       *testing.T
	locked  bool
	rows    map[flagKey]postgres.EventFlagFilter
	flags   map[flagKey][]string
	writes  int
	configs int
}

func newFlagStore(t *testing.T, q *postgresMocks.MockQuerier, maxTeams *int32) *flagStore {
	s := &flagStore{t: t, rows: map[flagKey]postgres.EventFlagFilter{}, flags: map[flagKey][]string{}}
	limit := pgtype.Int4{}
	if maxTeams != nil {
		limit = pgtype.Int4{Int32: *maxTeams, Valid: true}
	}
	q.EXPECT().LockEventExerciseVariantPick(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, uuid.UUID) error {
		s.locked = true
		return nil
	}).AnyTimes()
	q.EXPECT().GetEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, eventID uuid.UUID) (postgres.EventConfig, error) {
		s.configs++
		return postgres.EventConfig{EventID: eventID, MaxTeams: limit}, nil
	}).AnyTimes()
	q.EXPECT().GetEventFlagFilter(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.GetEventFlagFilterParams) (postgres.EventFlagFilter, error) {
		s.requireLock()
		row, ok := s.rows[flagKey{arg.EventExerciseID, arg.VariantIndex, arg.TaskID}]
		if !ok {
			return postgres.EventFlagFilter{}, pgx.ErrNoRows
		}
		return row, nil
	}).AnyTimes()
	q.EXPECT().ListTaskVariantIssuedFlags(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.ListTaskVariantIssuedFlagsParams) ([]string, error) {
		s.requireLock()
		return append([]string(nil), s.flags[flagKey{arg.EventExerciseID, arg.VariantIndex, arg.TaskID}]...), nil
	}).AnyTimes()
	q.EXPECT().UpsertEventFlagFilter(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventFlagFilterParams) error {
		s.requireLock()
		s.writes++
		s.rows[flagKey{arg.EventExerciseID, arg.VariantIndex, arg.TaskID}] = postgres.EventFlagFilter{EventExerciseID: arg.EventExerciseID, VariantIndex: arg.VariantIndex,
			TaskID: arg.TaskID, Mode: arg.Mode, Salt: append([]byte(nil), arg.Salt...), Hashes: arg.Hashes, Size: arg.Size, Bits: append([]byte(nil), arg.Bits...),
			Inserted: arg.Inserted, Capacity: arg.Capacity, Resets: arg.Resets}
		return nil
	}).AnyTimes()
	return s
}

func (s *flagStore) requireLock() {
	if !s.locked {
		s.t.Fatal("flag filter used before the variant pick lock")
	}
}

func (s *flagStore) stored(key flagKey, flag string) { s.flags[key] = append(s.flags[key], flag) }

// 10 teams on 2 variants of a task with N=10 values, and the moderators team:
// every flag of a variant is distinct, moderators included.
func TestTeamFlags_TeamsAndModeratorsGetDistinctFlagsPerVariant(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	link := perTeamLink()
	task := exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task", Flag: []string{`template:ICE{\d}`}}
	version := exerciseModel.ExerciseVersion{ID: uuid.Must(uuid.NewV7()), Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{task}}, {Tasks: []exerciseModel.Task{task}}}}
	challenge := eventChallengeModel.EventChallenge{ID: uuid.Must(uuid.NewV7()), EventExerciseID: link.ID, TaskID: task.ID}
	limit := int32(10)
	store := newFlagStore(t, q, &limit)
	onVariant := map[int32]int{}
	moderators := uuid.Must(uuid.NewV7())
	q.EXPECT().GetTeamEventExerciseVariant(gomock.Any(), gomock.Any()).Return(int32(0), pgx.ErrNoRows).AnyTimes()
	q.EXPECT().CountEventExerciseVariantTeams(gomock.Any(), link.ID).DoAndReturn(func(context.Context, uuid.UUID) ([]postgres.CountEventExerciseVariantTeamsRow, error) {
		var rows []postgres.CountEventExerciseVariantTeamsRow
		for index, teams := range onVariant {
			rows = append(rows, postgres.CountEventExerciseVariantTeamsRow{VariantIndex: index, Teams: int64(teams)})
		}
		return rows, nil
	}).AnyTimes()
	flags := map[int32]map[string]bool{0: {}, 1: {}}
	q.EXPECT().CreateTeamChallenge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateTeamChallengeParams) (postgres.TeamChallenge, error) {
		if flags[arg.VariantIndex][arg.ExpectedFlag] {
			t.Fatalf("variant %d repeats %q", arg.VariantIndex, arg.ExpectedFlag)
		}
		flags[arg.VariantIndex][arg.ExpectedFlag] = true
		store.stored(flagKey{link.ID, arg.VariantIndex, task.ID}, arg.ExpectedFlag)
		if arg.EventTeamID != moderators {
			onVariant[arg.VariantIndex]++
		}
		return postgres.TeamChallenge{ID: arg.ID}, nil
	}).Times(11)

	u := &EventUseCase{flagRandomBytes: 20}
	repo := teamChallengeRepo.New(q)
	tasks := map[uuid.UUID]exerciseModel.Task{task.ID: task}
	prepare := func(teamID uuid.UUID) {
		store.locked = false // one transaction per team
		teamFlags := u.newTeamFlags(q, link.EventID, link.ID)
		index, err := pickTeamVariant(context.Background(), repo, teamID, link, version, teamFlags)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = u.newTeamChallenge(context.Background(), repo, teamFlags, link.EventID, teamID, challenge, tasks, index, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 10 {
		if i == 3 {
			prepare(moderators)
		}
		prepare(uuid.Must(uuid.NewV7()))
	}
	if len(flags[0])+len(flags[1]) != 11 {
		t.Fatalf("distinct flags: %d + %d", len(flags[0]), len(flags[1]))
	}
}

// A team that keeps its variant: the pick lock, then the team limit, the filter
// read, its rebuild from the stored flags, the filter write and the team
// challenge, all in that order in one transaction.
func TestTeamFlags_FilterIsReadAndWrittenUnderTheLockBeforeTheChallenge(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	link, teamID := perTeamLink(), uuid.Must(uuid.NewV7())
	task := exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task", Flag: []string{`ICE{a}`, `template:ICE{[ab][0-2]}`}}
	version := exerciseModel.ExerciseVersion{ID: uuid.Must(uuid.NewV7()), Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{task}}, {Tasks: []exerciseModel.Task{task}}}}
	challenge := eventChallengeModel.EventChallenge{ID: uuid.Must(uuid.NewV7()), EventExerciseID: link.ID, TaskID: task.ID}
	var saved postgres.UpsertEventFlagFilterParams
	var written string
	gomock.InOrder(
		q.EXPECT().GetTeamEventExerciseVariant(gomock.Any(), gomock.Any()).Return(int32(1), nil),
		q.EXPECT().LockEventExerciseVariantPick(gomock.Any(), link.ID).Return(nil),
		q.EXPECT().GetEventConfig(gomock.Any(), link.EventID).Return(postgres.EventConfig{EventID: link.EventID}, nil),
		q.EXPECT().GetEventFlagFilter(gomock.Any(), postgres.GetEventFlagFilterParams{EventExerciseID: link.ID, VariantIndex: 1, TaskID: task.ID}).Return(postgres.EventFlagFilter{}, pgx.ErrNoRows),
		q.EXPECT().ListTaskVariantIssuedFlags(gomock.Any(), postgres.ListTaskVariantIssuedFlagsParams{EventExerciseID: link.ID, TaskID: task.ID, VariantIndex: 1}).Return([]string{"ICE{a0}", "ICE{b1}"}, nil),
		q.EXPECT().UpsertEventFlagFilter(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventFlagFilterParams) error {
			saved = arg
			return nil
		}),
		q.EXPECT().CreateTeamChallenge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateTeamChallengeParams) (postgres.TeamChallenge, error) {
			written = arg.ExpectedFlag
			return postgres.TeamChallenge{ID: arg.ID}, nil
		}),
	)
	u := &EventUseCase{flagRandomBytes: 20}
	repo := teamChallengeRepo.New(q)
	teamFlags := u.newTeamFlags(q, link.EventID, link.ID)
	index, err := pickTeamVariant(context.Background(), repo, teamID, link, version, teamFlags)
	if err != nil || index != 1 {
		t.Fatalf("kept variant = %d, %v", index, err)
	}
	if _, err = u.newTeamChallenge(context.Background(), repo, teamFlags, link.EventID, teamID, challenge, map[uuid.UUID]exerciseModel.Task{task.ID: task}, index, time.Now()); err != nil {
		t.Fatal(err)
	}
	// N=7 (bitmap); ICE{a0} and ICE{b1} are taken, ICE{a} is free.
	if written == "ICE{a0}" || written == "ICE{b1}" || written == "" {
		t.Fatalf("flag = %q repeats a stored flag", written)
	}
	if saved.Mode != 2 || saved.Size != 7 || saved.Inserted != 3 || saved.Capacity != 100 || len(saved.Salt) != 16 {
		t.Fatalf("saved filter = %+v", saved)
	}
}

// A task without candidates keeps the full random mode and touches no filter.
func TestNewTeamChallenge_NoCandidatesStaysRandom(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl) // strict: no lock, no filter
	task := exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task"}
	challenge := eventChallengeModel.EventChallenge{ID: uuid.Must(uuid.NewV7()), EventExerciseID: uuid.Must(uuid.NewV7()), TaskID: task.ID}
	var written string
	q.EXPECT().CreateTeamChallenge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateTeamChallengeParams) (postgres.TeamChallenge, error) {
		written = arg.ExpectedFlag
		return postgres.TeamChallenge{ID: arg.ID}, nil
	})
	u := &EventUseCase{flagRandomBytes: 20}
	repo := teamChallengeRepo.New(q)
	if _, err := u.newTeamChallenge(context.Background(), repo, u.newTeamFlags(q, uuid.Must(uuid.NewV7()), challenge.EventExerciseID), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), challenge, map[uuid.UUID]exerciseModel.Task{task.ID: task}, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(written) != len("ICE{}")+40 {
		t.Fatalf("random flag = %q", written)
	}
}

// An event without a team limit: 150 teams and the moderators on a large
// template. The filter starts at 100, grows from the stored flags when full,
// and no flag repeats.
func TestTeamFlags_UnlimitedEventGrowsTheFilterWithoutRepeats(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	link := perTeamLink()
	link.VariantMode, link.FixedVariantIndex = eventExerciseModel.VariantModeFixed, new(int32)
	task := exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task", Flag: []string{`template:ICE{[a-f]\d\d\d}`}}
	version := exerciseModel.ExerciseVersion{ID: uuid.Must(uuid.NewV7()), Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{task}}}}
	challenge := eventChallengeModel.EventChallenge{ID: uuid.Must(uuid.NewV7()), EventExerciseID: link.ID, TaskID: task.ID}
	store := newFlagStore(t, q, nil)
	seen := map[string]bool{}
	key := flagKey{link.ID, 0, task.ID}
	q.EXPECT().CreateTeamChallenge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateTeamChallengeParams) (postgres.TeamChallenge, error) {
		if seen[arg.ExpectedFlag] {
			t.Fatalf("repeated %q after %d teams", arg.ExpectedFlag, len(seen))
		}
		seen[arg.ExpectedFlag] = true
		store.stored(key, arg.ExpectedFlag)
		return postgres.TeamChallenge{ID: arg.ID}, nil
	}).Times(151)
	u := &EventUseCase{flagRandomBytes: 20}
	repo := teamChallengeRepo.New(q)
	for range 151 {
		store.locked = false
		teamFlags := u.newTeamFlags(q, link.EventID, link.ID)
		if _, err := u.newTeamChallenge(context.Background(), repo, teamFlags, link.EventID, uuid.Must(uuid.NewV7()), challenge, map[uuid.UUID]exerciseModel.Task{task.ID: task}, 0, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	row := store.rows[key]
	if row.Capacity != 200 || row.Size != 1024 || row.Inserted != 151 || row.Resets != 0 {
		t.Fatalf("filter after growth: capacity %d size %d inserted %d resets %d", row.Capacity, row.Size, row.Inserted, row.Resets)
	}
	_ = version
}

// A source switch to a fixed variant moves several teams onto one variant in
// one pass: the pick lock comes first, every moved row draws through the
// filter, and all flags on the variant (moderators included) are distinct.
func TestRefreshTeamAssignments_TeamsMovedOntoOneVariantGetDistinctFlags(t *testing.T) {
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
	row := func(variant int32, mods bool) postgres.ListTeamChallengesForRefreshRow {
		return postgres.ListTeamChallengesForRefreshRow{ID: uuid.Must(uuid.NewV7()), EventTeamID: uuid.Must(uuid.NewV7()), EventChallengeID: board[0].ID, VariantIndex: variant,
			Snapshot: []byte(`{}`), Hints: []byte(`[]`), ExpectedFlag: "ICE{old}", Moderators: mods}
	}
	rows := []postgres.ListTeamChallengesForRefreshRow{row(0, false), row(1, false), row(3, true), row(2, false), row(3, false)}
	store := newFlagStore(t, q, nil)
	q.EXPECT().ListTeamChallengesForRefresh(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, []uuid.UUID) ([]postgres.ListTeamChallengesForRefreshRow, error) {
		if !store.locked {
			t.Fatal("rows read before the variant pick lock")
		}
		return rows, nil
	})
	got := map[string]bool{}
	q.EXPECT().UpdateTeamChallengeContent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateTeamChallengeContentParams) (int64, error) {
		if got[arg.ExpectedFlag] {
			t.Fatalf("repeated %q", arg.ExpectedFlag)
		}
		got[arg.ExpectedFlag] = true
		store.stored(flagKey{link.ID, arg.VariantIndex, task.ID}, arg.ExpectedFlag)
		return 1, nil
	}).Times(len(rows))
	u := &EventUseCase{flagRandomBytes: 20}
	if err := u.refreshTeamAssignments(context.Background(), q, link, previous, next, board, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(rows) || store.writes != len(rows) {
		t.Fatalf("flags %v, filter writes %d", got, store.writes)
	}
}

// A source switch that re-picks dropped variants draws each re-picked row's
// flag through the filter of its new variant; a kept row keeps its flag.
func TestRefreshTeamAssignments_RepickedRowsDrawThroughTheirVariantFilter(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	link := perTeamLink()
	task := exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task", Flag: []string{`template:ICE{\d}`}}
	next := exerciseModel.ExerciseVersion{ID: uuid.Must(uuid.NewV7())}
	for range 3 {
		next.Variants = append(next.Variants, exerciseModel.Variant{Tasks: []exerciseModel.Task{task}})
	}
	board := []eventChallengeModel.EventChallenge{{ID: uuid.Must(uuid.NewV7()), EventExerciseID: link.ID, TaskID: task.ID}}
	row := func(variant int32) postgres.ListTeamChallengesForRefreshRow {
		return postgres.ListTeamChallengesForRefreshRow{ID: uuid.Must(uuid.NewV7()), EventTeamID: uuid.Must(uuid.NewV7()), EventChallengeID: board[0].ID, VariantIndex: variant,
			Snapshot: []byte(`{}`), Hints: []byte(`[]`), ExpectedFlag: "ICE{old}"}
	}
	rows := []postgres.ListTeamChallengesForRefreshRow{row(0), row(7), row(8), row(9), row(10)}
	store := newFlagStore(t, q, nil)
	q.EXPECT().ListTeamChallengesForRefresh(gomock.Any(), gomock.Any()).Return(rows, nil)
	q.EXPECT().CountEventExerciseVariantTeams(gomock.Any(), link.ID).Return([]postgres.CountEventExerciseVariantTeamsRow{
		{VariantIndex: 0, Teams: 1}, {VariantIndex: 7, Teams: 1}, {VariantIndex: 8, Teams: 1}, {VariantIndex: 9, Teams: 1}, {VariantIndex: 10, Teams: 1},
	}, nil)
	perVariant := map[int32]map[string]bool{}
	kept := ""
	q.EXPECT().UpdateTeamChallengeContent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateTeamChallengeContentParams) (int64, error) {
		if arg.ID == rows[0].ID {
			kept = arg.ExpectedFlag
			return 1, nil
		}
		if perVariant[arg.VariantIndex] == nil {
			perVariant[arg.VariantIndex] = map[string]bool{}
		}
		if perVariant[arg.VariantIndex][arg.ExpectedFlag] {
			t.Fatalf("variant %d repeats %q", arg.VariantIndex, arg.ExpectedFlag)
		}
		perVariant[arg.VariantIndex][arg.ExpectedFlag] = true
		store.stored(flagKey{link.ID, arg.VariantIndex, task.ID}, arg.ExpectedFlag)
		return 1, nil
	}).Times(len(rows))
	u := &EventUseCase{flagRandomBytes: 20}
	if err := u.refreshTeamAssignments(context.Background(), q, link, next, next, board, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if kept != "ICE{old}" {
		t.Fatalf("kept row flag changed to %q", kept)
	}
	if store.writes != 4 {
		t.Fatalf("filter writes = %d, want 4", store.writes)
	}
}
