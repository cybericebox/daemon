package event

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/flagpattern"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

var flagSlotTestSecret = []byte("fixed-flag-slot-secret-for-tests")

// A new team challenge of a task with candidates takes the flag of the team's
// slot: the rank query result, under the key of (event exercise, task, variant).
func TestNewTeamChallenge_UsesTheTeamRankAsFlagSlot(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	task := exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task", Flag: []string{`ICE{a}`, `template:ICE{[ab][0-2]}`}}
	challenge := eventChallengeModel.EventChallenge{ID: uuid.Must(uuid.NewV7()), EventExerciseID: uuid.Must(uuid.NewV7()), TaskID: task.ID}
	key := teamChallengeModel.FlagSlotKey(flagSlotTestSecret, challenge.EventExerciseID, task.ID, 1)
	want, err := flagpattern.ResolveAt(task.Flag, big.NewInt(5), key)
	if err != nil {
		t.Fatal(err)
	}
	q.EXPECT().CountEventTeamsBefore(gomock.Any(), postgres.CountEventTeamsBeforeParams{EventID: eventID, EventTeamID: teamID}).Return(int64(5), nil).Times(1)
	var written string
	q.EXPECT().CreateTeamChallenge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateTeamChallengeParams) (postgres.TeamChallenge, error) {
		written = arg.ExpectedFlag
		return postgres.TeamChallenge{ID: arg.ID, ExpectedFlag: arg.ExpectedFlag}, nil
	}).Times(2)
	u := &EventUseCase{flagRandomBytes: 20, flagSlotSecret: flagSlotTestSecret}
	repo := teamChallengeRepo.New(q)
	slots := newTeamFlagSlots(repo, eventID)
	tasks := map[uuid.UUID]exerciseModel.Task{task.ID: task}
	for range 2 { // the slot is read once per team and transaction
		if _, err = u.newTeamChallenge(context.Background(), repo, slots, eventID, teamID, challenge, tasks, 1, time.Now()); err != nil {
			t.Fatal(err)
		}
		if written != want {
			t.Fatalf("expected flag = %q, want slot 5's %q", written, want)
		}
	}
}

// Teams of ranks 0..N-1 get N distinct flags.
func TestNewTeamChallenge_RanksGiveDistinctFlags(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	eventID := uuid.Must(uuid.NewV7())
	task := exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task", Flag: []string{`ICE{a}`, `ICE{b}`, `template:ICE{x\d}`}}
	challenge := eventChallengeModel.EventChallenge{ID: uuid.Must(uuid.NewV7()), EventExerciseID: uuid.Must(uuid.NewV7()), TaskID: task.ID}
	rank := int64(0)
	q.EXPECT().CountEventTeamsBefore(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, postgres.CountEventTeamsBeforeParams) (int64, error) {
		rank++
		return rank - 1, nil
	}).Times(12)
	seen := map[string]bool{}
	q.EXPECT().CreateTeamChallenge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateTeamChallengeParams) (postgres.TeamChallenge, error) {
		if seen[arg.ExpectedFlag] {
			t.Fatalf("flag %q repeats", arg.ExpectedFlag)
		}
		seen[arg.ExpectedFlag] = true
		return postgres.TeamChallenge{ID: arg.ID}, nil
	}).Times(12)
	u := &EventUseCase{flagRandomBytes: 20, flagSlotSecret: flagSlotTestSecret}
	repo := teamChallengeRepo.New(q)
	slots := newTeamFlagSlots(repo, eventID)
	tasks := map[uuid.UUID]exerciseModel.Task{task.ID: task}
	for range 12 {
		if _, err := u.newTeamChallenge(context.Background(), repo, slots, eventID, uuid.Must(uuid.NewV7()), challenge, tasks, 0, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
}

// A task without candidates keeps the full random mode and reads no slot.
func TestNewTeamChallenge_NoCandidatesStaysRandom(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl) // strict: no CountEventTeamsBefore
	task := exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task"}
	challenge := eventChallengeModel.EventChallenge{ID: uuid.Must(uuid.NewV7()), EventExerciseID: uuid.Must(uuid.NewV7()), TaskID: task.ID}
	var written string
	q.EXPECT().CreateTeamChallenge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateTeamChallengeParams) (postgres.TeamChallenge, error) {
		written = arg.ExpectedFlag
		return postgres.TeamChallenge{ID: arg.ID}, nil
	})
	u := &EventUseCase{flagRandomBytes: 20, flagSlotSecret: flagSlotTestSecret}
	repo := teamChallengeRepo.New(q)
	eventID := uuid.Must(uuid.NewV7())
	if _, err := u.newTeamChallenge(context.Background(), repo, newTeamFlagSlots(repo, eventID), eventID, uuid.Must(uuid.NewV7()), challenge, map[uuid.UUID]exerciseModel.Task{task.ID: task}, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(written) != len("ICE{}")+40 {
		t.Fatalf("random flag = %q", written)
	}
}
