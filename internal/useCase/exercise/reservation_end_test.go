package exercise_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
)

type labNotices struct {
	ending []time.Time
	ended  []uuid.UUID
}

func (n *labNotices) TestLabEnding(_ context.Context, _ uuid.UUID, _ exerciseModel.TestDeploy, endsAt time.Time) error {
	n.ending = append(n.ending, endsAt)
	return nil
}

func (n *labNotices) TestLabEnded(_ context.Context, _ uuid.UUID, d exerciseModel.TestDeploy) error {
	n.ended = append(n.ended, d.ID)
	return nil
}

// The ending notice of a run's reservation is recorded for the lab page and told to the author once.
func TestTestRunEndingRecordedAndToldOnce(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	owner, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	endsAt := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	row := postgres.ExerciseTestDeployment{ID: id, CreatedBy: owner, GroupName: "tu-1", LabName: "l-1", ExpiresAt: endsAt}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	q.EXPECT().SetExerciseTestDeployEndingAt(gomock.Any(), postgres.SetExerciseTestDeployEndingAtParams{ID: id, CreatedBy: owner, EndingAt: pgtype.Timestamptz{Time: endsAt, Valid: true}}).Return(int64(1), nil)
	notices := &labNotices{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &fakeInfra{}})
	uc.SetTestLabNotifier(notices)
	if err := uc.TestRunEnding(context.Background(), owner, id, endsAt); err != nil {
		t.Fatal(err)
	}
	if len(notices.ending) != 1 || !notices.ending[0].Equal(endsAt) {
		t.Fatalf("author notices %v", notices.ending)
	}
	// Told already: the row carries the notice.
	row.EndingAt = pgtype.Timestamptz{Time: endsAt, Valid: true}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	if err := uc.TestRunEnding(context.Background(), owner, id, endsAt); err != nil {
		t.Fatal(err)
	}
	if len(notices.ending) != 1 {
		t.Fatalf("the notice is told once, got %d", len(notices.ending))
	}
}
