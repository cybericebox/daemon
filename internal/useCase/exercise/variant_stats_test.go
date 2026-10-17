package exercise_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/model"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

var (
	statsExercise = uuid.FromStringOrNil("0199f100-0000-7000-8000-000000000001")
	statsOld      = uuid.FromStringOrNil("0199f100-0000-7000-8000-000000000002")
	statsNew      = uuid.FromStringOrNil("0199f100-0000-7000-8000-000000000003")
	statsUnused   = uuid.FromStringOrNil("0199f100-0000-7000-8000-000000000004")
	statsVar0     = uuid.FromStringOrNil("0199f100-0000-7000-8000-0000000000a0")
	statsVar1     = uuid.FromStringOrNil("0199f100-0000-7000-8000-0000000000a1")
	statsTask1    = uuid.FromStringOrNil("0199f100-0000-7000-8000-0000000000b1")
	statsTask2    = uuid.FromStringOrNil("0199f100-0000-7000-8000-0000000000b2")
	statsCreated  = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
)

// statsVersionRow is a version row with two variants of two subtasks each.
func statsVersionRow(t *testing.T, id uuid.UUID, status string, createdAt time.Time, publishedAt *time.Time) postgres.ExerciseVersion {
	t.Helper()
	task := func(id uuid.UUID, name string) map[string]any {
		return map[string]any{"id": id, "name": name, "difficulty": "easy"}
	}
	variants, err := json.Marshal([]map[string]any{
		{"id": statsVar0, "index": 0, "tasks": []any{task(statsTask1, "Recon"), task(statsTask2, "Exploit")}},
		{"id": statsVar1, "index": 1, "tasks": []any{task(statsTask1, "Recon"), task(statsTask2, "Exploit")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	row := postgres.ExerciseVersion{ID: id, ExerciseID: statsExercise, Status: status, Variants: variants, CreatedAt: createdAt}
	if publishedAt != nil {
		row.PublishedAt = pgtype.Timestamptz{Time: *publishedAt, Valid: true}
	}
	return row
}

func TestGetVariantStats_ShapesPerVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	oldPublished := statsCreated.Add(time.Hour)
	newPublished := statsCreated.Add(48 * time.Hour)

	q.EXPECT().ListExerciseVersions(gomock.Any(), statsExercise).Return([]postgres.ExerciseVersion{
		statsVersionRow(t, statsOld, "unpublished", statsCreated, &oldPublished),
		statsVersionRow(t, statsUnused, "draft", statsCreated.Add(72*time.Hour), nil),
		statsVersionRow(t, statsNew, "published", statsCreated.Add(24*time.Hour), &newPublished),
	}, nil)
	q.EXPECT().ListExerciseVariantIssues(gomock.Any(), statsExercise).Return([]postgres.ListExerciseVariantIssuesRow{
		{ExerciseVersionID: statsOld, VariantID: statsVar1, Issued: 5},
		{ExerciseVersionID: statsNew, VariantID: statsVar0, Issued: 3},
	}, nil)
	q.EXPECT().ListExerciseVariantTaskSolves(gomock.Any(), statsExercise).Return([]postgres.ListExerciseVariantTaskSolvesRow{
		{ExerciseVersionID: statsOld, VariantID: statsVar1, TaskID: statsTask2, Solved: 4},
		{ExerciseVersionID: statsNew, VariantID: statsVar0, TaskID: statsTask1, Solved: 2},
	}, nil)
	// Fully solved = teams with at least as many solved subtasks as the variant has (2).
	q.EXPECT().ListExerciseVariantSolveDepths(gomock.Any(), statsExercise).Return([]postgres.ListExerciseVariantSolveDepthsRow{
		{ExerciseVersionID: statsOld, VariantID: statsVar1, SolvedTasks: 1, Teams: 2},
		{ExerciseVersionID: statsOld, VariantID: statsVar1, SolvedTasks: 2, Teams: 1},
		{ExerciseVersionID: statsOld, VariantID: statsVar1, SolvedTasks: 3, Teams: 1},
		{ExerciseVersionID: statsNew, VariantID: statsVar0, SolvedTasks: 1, Teams: 1},
		{ExerciseVersionID: statsNew, VariantID: statsVar0, SolvedTasks: 2, Teams: 1},
	}, nil)

	got, err := uc.GetVariantStats(context.Background(), statsExercise)
	if err != nil {
		t.Fatal(err)
	}
	want := []exercise.VersionVariantStats{
		{VersionID: statsNew, Status: "published", CreatedAt: statsCreated.Add(24 * time.Hour), PublishedAt: &newPublished, Variants: []exercise.VariantStats{
			{VariantID: statsVar0, Number: 1, Issued: 3, Solved: 1, Tasks: []exercise.TaskSolves{{TaskID: statsTask1, Name: "Recon", Solved: 2}, {TaskID: statsTask2, Name: "Exploit"}}},
			{VariantID: statsVar1, Number: 2, Tasks: []exercise.TaskSolves{{TaskID: statsTask1, Name: "Recon"}, {TaskID: statsTask2, Name: "Exploit"}}},
		}},
		{VersionID: statsOld, Status: "unpublished", CreatedAt: statsCreated, PublishedAt: &oldPublished, Variants: []exercise.VariantStats{
			{VariantID: statsVar0, Number: 1, Tasks: []exercise.TaskSolves{{TaskID: statsTask1, Name: "Recon"}, {TaskID: statsTask2, Name: "Exploit"}}},
			{VariantID: statsVar1, Number: 2, Issued: 5, Solved: 2, Tasks: []exercise.TaskSolves{{TaskID: statsTask1, Name: "Recon"}, {TaskID: statsTask2, Name: "Exploit", Solved: 4}}},
		}},
	}
	assertJSONEqual(t, got, want)
}

func TestGetVariantStats_DropsUnknownVariant(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	unknown := uuid.FromStringOrNil("0199f100-0000-7000-8000-0000000000ff")

	q.EXPECT().ListExerciseVersions(gomock.Any(), statsExercise).Return([]postgres.ExerciseVersion{
		statsVersionRow(t, statsNew, "published", statsCreated, &statsCreated),
	}, nil)
	q.EXPECT().ListExerciseVariantIssues(gomock.Any(), statsExercise).Return([]postgres.ListExerciseVariantIssuesRow{
		{ExerciseVersionID: statsNew, VariantID: unknown, Issued: 7},
		{ExerciseVersionID: statsNew, VariantID: statsVar1, Issued: 1},
	}, nil)
	q.EXPECT().ListExerciseVariantTaskSolves(gomock.Any(), statsExercise).Return([]postgres.ListExerciseVariantTaskSolvesRow{
		{ExerciseVersionID: statsNew, VariantID: unknown, TaskID: statsTask1, Solved: 7},
	}, nil)
	q.EXPECT().ListExerciseVariantSolveDepths(gomock.Any(), statsExercise).Return([]postgres.ListExerciseVariantSolveDepthsRow{
		{ExerciseVersionID: statsNew, VariantID: unknown, SolvedTasks: 2, Teams: 7},
	}, nil)

	got, err := uc.GetVariantStats(context.Background(), statsExercise)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Variants) != 2 {
		t.Fatalf("stats = %+v", got)
	}
	for _, v := range got[0].Variants {
		if v.VariantID == unknown || (v.VariantID == statsVar0 && v.Issued != 0) || v.Tasks[0].Solved != 0 {
			t.Fatalf("an unknown variant must be dropped: %+v", got[0].Variants)
		}
	}
}

func TestGetVariantStats_EmptyWhenNeverIssued(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})

	q.EXPECT().ListExerciseVersions(gomock.Any(), statsExercise).Return([]postgres.ExerciseVersion{
		statsVersionRow(t, statsNew, "published", statsCreated, &statsCreated),
	}, nil)
	q.EXPECT().ListExerciseVariantIssues(gomock.Any(), statsExercise).Return(nil, nil)
	q.EXPECT().ListExerciseVariantTaskSolves(gomock.Any(), statsExercise).Return(nil, nil)
	q.EXPECT().ListExerciseVariantSolveDepths(gomock.Any(), statsExercise).Return(nil, nil)

	got, err := uc.GetVariantStats(context.Background(), statsExercise)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("want an empty, non-nil slice, got %#v", got)
	}
}

func TestGetVariantStats_RepositoryFailureIsPlatformError(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})

	q.EXPECT().ListExerciseVersions(gomock.Any(), statsExercise).Return(nil, nil).AnyTimes()
	q.EXPECT().ListExerciseVariantIssues(gomock.Any(), statsExercise).Return(nil, errors.New("boom"))

	_, err := uc.GetVariantStats(context.Background(), statsExercise)
	var ae appErr.Error
	if !errors.As(err, &ae) || !ae.StatusCode().IsInternal() || !ae.StatusCode().Is(model.ErrPlatform.Err().StatusCode()) {
		t.Fatalf("want a platform error, got %v", err)
	}
}

func assertJSONEqual(t *testing.T, got, want any) {
	t.Helper()
	g, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	w, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(g) != string(w) {
		t.Fatalf("got  %s\nwant %s", g, w)
	}
}
