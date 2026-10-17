package event

import (
	"context"
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
)

func variantVersion(n int, taskID uuid.UUID) exerciseModel.ExerciseVersion {
	version := exerciseModel.ExerciseVersion{ID: uuid.Must(uuid.NewV7())}
	for range n {
		version.Variants = append(version.Variants, exerciseModel.Variant{Tasks: []exerciseModel.Task{{ID: taskID, Name: "Task", Flag: []string{"ICE{x}"}}}})
	}
	return version
}

func perTeamLink() eventExerciseModel.EventExercise {
	return eventExerciseModel.EventExercise{ID: uuid.Must(uuid.NewV7()), EventID: uuid.Must(uuid.NewV7()), VariantMode: eventExerciseModel.VariantModePerTeam}
}

func TestPickTeamVariant_FixedModeNeverCounts(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl) // strict: no lock, no count
	fixed := int32(2)
	link := perTeamLink()
	link.VariantMode, link.FixedVariantIndex = eventExerciseModel.VariantModeFixed, &fixed
	got, err := pickTeamVariant(context.Background(), teamChallengeRepo.New(q), uuid.Must(uuid.NewV7()), link, variantVersion(3, uuid.Nil))
	if err != nil || got != 2 {
		t.Fatalf("fixed pick = %d, %v; want 2", got, err)
	}
}

func TestPickTeamVariant_LocksBeforeCountingAndFillsTheLowestGap(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	link, teamID := perTeamLink(), uuid.Must(uuid.NewV7())
	gomock.InOrder(
		q.EXPECT().GetTeamEventExerciseVariant(gomock.Any(), postgres.GetTeamEventExerciseVariantParams{EventTeamID: teamID, EventExerciseID: link.ID, VariantCount: 3}).Return(int32(0), pgx.ErrNoRows),
		q.EXPECT().LockEventExerciseVariantPick(gomock.Any(), link.ID).Return(nil),
		q.EXPECT().CountEventExerciseVariantTeams(gomock.Any(), link.ID).Return([]postgres.CountEventExerciseVariantTeamsRow{
			{VariantIndex: 0, Teams: 1}, {VariantIndex: 2, Teams: 1},
		}, nil),
	)
	got, err := pickTeamVariant(context.Background(), teamChallengeRepo.New(q), teamID, link, variantVersion(3, uuid.Nil))
	if err != nil || got != 1 {
		t.Fatalf("pick = %d, %v; want 1", got, err)
	}
}

func TestPickTeamVariant_KeepsTheTeamsExistingVariant(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl) // strict: no lock, no count
	link, teamID := perTeamLink(), uuid.Must(uuid.NewV7())
	q.EXPECT().GetTeamEventExerciseVariant(gomock.Any(), postgres.GetTeamEventExerciseVariantParams{EventTeamID: teamID, EventExerciseID: link.ID, VariantCount: 3}).Return(int32(2), nil)
	got, err := pickTeamVariant(context.Background(), teamChallengeRepo.New(q), teamID, link, variantVersion(3, uuid.Nil))
	if err != nil || got != 2 {
		t.Fatalf("pick = %d, %v; want the kept 2", got, err)
	}
}

func TestPickTeamVariant_ReplacesAVariantThatNoLongerExists(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	link, teamID := perTeamLink(), uuid.Must(uuid.NewV7())
	gomock.InOrder(
		// Only variants the version still has count as the team's: the query
		// filters by the current count, so a retained row on 4 is not returned.
		q.EXPECT().GetTeamEventExerciseVariant(gomock.Any(), postgres.GetTeamEventExerciseVariantParams{EventTeamID: teamID, EventExerciseID: link.ID, VariantCount: 2}).Return(int32(0), pgx.ErrNoRows),
		q.EXPECT().LockEventExerciseVariantPick(gomock.Any(), link.ID).Return(nil),
		// Teams on removed variants (4) do not count.
		q.EXPECT().CountEventExerciseVariantTeams(gomock.Any(), link.ID).Return([]postgres.CountEventExerciseVariantTeamsRow{
			{VariantIndex: 0, Teams: 1}, {VariantIndex: 4, Teams: 7},
		}, nil),
	)
	got, err := pickTeamVariant(context.Background(), teamChallengeRepo.New(q), teamID, link, variantVersion(2, uuid.Nil))
	if err != nil || got != 1 {
		t.Fatalf("pick = %d, %v; want 1", got, err)
	}
}

// A source switch that drops variants re-picks them in one pass: every team
// keeps one variant across its challenges, the pass counts its own picks, and
// the moderators team takes no slot.
func TestRefreshTeamAssignments_RebalancesRemovedVariants(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	link := perTeamLink()
	taskOne, taskTwo := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	next := exerciseModel.ExerciseVersion{ID: uuid.Must(uuid.NewV7())}
	for range 3 {
		next.Variants = append(next.Variants, exerciseModel.Variant{Tasks: []exerciseModel.Task{
			{ID: taskOne, Name: "One", Flag: []string{"ICE{x}"}}, {ID: taskTwo, Name: "Two", Flag: []string{"ICE{x}"}},
		}})
	}
	board := []eventChallengeModel.EventChallenge{{ID: uuid.Must(uuid.NewV7()), TaskID: taskOne}, {ID: uuid.Must(uuid.NewV7()), TaskID: taskTwo}}
	teamA, teamB, teamC, moderators := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := func(team, challenge uuid.UUID, variant int32, mods bool) postgres.ListTeamChallengesForRefreshRow {
		return postgres.ListTeamChallengesForRefreshRow{ID: uuid.Must(uuid.NewV7()), EventTeamID: team, EventChallengeID: challenge, VariantIndex: variant,
			Snapshot: []byte(`{}`), Hints: []byte(`[]`), ExpectedFlag: "ICE{x}", Moderators: mods}
	}
	rows := []postgres.ListTeamChallengesForRefreshRow{
		row(teamA, board[0].ID, 0, false), row(teamA, board[1].ID, 0, false), // in range: kept
		row(teamB, board[0].ID, 7, false), row(teamB, board[1].ID, 7, false),
		row(moderators, board[0].ID, 5, true),
		row(teamC, board[0].ID, 6, false),
	}
	want := map[uuid.UUID]int32{teamA: 0, teamB: 1, moderators: 2, teamC: 2}
	gomock.InOrder(
		q.EXPECT().ListTeamChallengesForRefresh(gomock.Any(), gomock.Any()).Return(rows, nil),
		q.EXPECT().LockEventExerciseVariantPick(gomock.Any(), link.ID).Return(nil),
		q.EXPECT().CountEventExerciseVariantTeams(gomock.Any(), link.ID).Return([]postgres.CountEventExerciseVariantTeamsRow{
			{VariantIndex: 0, Teams: 1}, {VariantIndex: 6, Teams: 1}, {VariantIndex: 7, Teams: 1},
		}, nil),
	)
	// Re-resolved flags read each team's slot once (team B has two rows; team A keeps its flag).
	q.EXPECT().CountEventTeamsBefore(gomock.Any(), gomock.Any()).Return(int64(0), nil).Times(3)
	got := map[uuid.UUID]int32{}
	byID := map[uuid.UUID]uuid.UUID{}
	for _, r := range rows {
		byID[r.ID] = r.EventTeamID
	}
	q.EXPECT().UpdateTeamChallengeContent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateTeamChallengeContentParams) (int64, error) {
		team := byID[arg.ID]
		if previous, seen := got[team]; seen && previous != arg.VariantIndex {
			t.Fatalf("team %s split across variants %d and %d", team, previous, arg.VariantIndex)
		}
		got[team] = arg.VariantIndex
		return 1, nil
	}).Times(len(rows))
	u := &EventUseCase{flagRandomBytes: 20}
	if err := u.refreshTeamAssignments(context.Background(), q, link, next, next, board, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	for team, index := range want {
		if got[team] != index {
			t.Fatalf("variants = %v, want %v", got, want)
		}
	}
}
