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
	"github.com/cybericebox/daemon/internal/useCase/exercise"
)

type labNotices struct {
	ending []time.Time
	ended  []uuid.UUID
	names  []string
}

func (n *labNotices) TestLabEnding(_ context.Context, _, _ uuid.UUID, name string, endsAt time.Time) error {
	n.ending = append(n.ending, endsAt)
	n.names = append(n.names, name)
	return nil
}

func (n *labNotices) TestLabEnded(_ context.Context, _, id uuid.UUID, _ string) error {
	n.ended = append(n.ended, id)
	return nil
}

// The ending notice of a run's reservation is recorded for the lab page and told to the author once.
func TestTestRunEndingRecordedAndToldOnce(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	owner, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	endsAt := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	versionID, exerciseID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := postgres.ExerciseTestDeployment{ID: id, CreatedBy: owner, GroupName: "tu-1", LabName: "l-1", ExpiresAt: endsAt, VersionID: versionID}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, ExerciseID: exerciseID}, nil)
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID, Name: "Web basics"}, nil)
	q.EXPECT().SetExerciseTestDeployEndingAt(gomock.Any(), postgres.SetExerciseTestDeployEndingAtParams{ID: id, CreatedBy: owner, EndingAt: pgtype.Timestamptz{Time: endsAt, Valid: true}}).Return(int64(1), nil)
	notices := &labNotices{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &fakeInfra{}})
	uc.SetTestLabNotifier(notices)
	if err := uc.TestRunEnding(context.Background(), owner, id, endsAt); err != nil {
		t.Fatal(err)
	}
	if len(notices.ending) != 1 || !notices.ending[0].Equal(endsAt) || notices.names[0] != "Web basics" {
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
