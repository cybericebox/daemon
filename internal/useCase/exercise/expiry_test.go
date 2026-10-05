package exercise_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
)

func fixedNow() time.Time { return time.Date(2026, 10, 5, 16, 3, 0, 0, time.UTC) }

type scheduled struct {
	owner, deploy uuid.UUID
	at            time.Time
}

type fakeExpiry struct {
	got []scheduled
	err error
}

func (f *fakeExpiry) ScheduleTestDeployExpiry(_ context.Context, owner, deploy uuid.UUID, at time.Time) error {
	f.got = append(f.got, scheduled{owner: owner, deploy: deploy, at: at})
	return f.err
}

func fixedID(n byte) uuid.UUID { return uuid.UUID{15: n} }

func TestDeployVariantTest_SchedulesTheEndAtTheLeaseExpiry(t *testing.T) {
	now := fixedNow()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	variantID, versionID, ownerID := fixedID(1), fixedID(2), fixedID(3)
	variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: fixedID(4), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx"}}}}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	var created uuid.UUID
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		created = p.ID
		return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, CreatedBy: p.CreatedBy, ExpiresAt: p.ExpiresAt}, nil
	})
	expiry := &fakeExpiry{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &fakeInfra{}})
	uc.SetClock(func() time.Time { return now })
	uc.SetTestDeployExpiry(expiry)

	if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID); err != nil {
		t.Fatal(err)
	}
	want := scheduled{owner: ownerID, deploy: created, at: now.Add(2 * time.Hour)}
	if len(expiry.got) != 1 || expiry.got[0] != want {
		t.Fatalf("the end must be scheduled once, at the lease expiry: got %+v want %+v", expiry.got, want)
	}
}

func TestDeployVariantTest_ASchedulingFailureDoesNotFailTheDeploy(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	variantID, versionID := fixedID(1), fixedID(2)
	variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: fixedID(4), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx"}}}}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, CreatedBy: p.CreatedBy}, nil
	})
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &fakeInfra{}})
	uc.SetTestDeployExpiry(&fakeExpiry{err: errors.New("river is down")})
	if _, err := uc.DeployVariantTest(context.Background(), fixedID(3), versionID, variantID); err != nil {
		t.Fatalf("the periodic sweep covers a missed job: %v", err)
	}
}

func TestExtendTestDeploy_SchedulesTheEndAtTheNewExpiry(t *testing.T) {
	now := fixedNow()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	owner, deployID := fixedID(3), fixedID(5)
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{
		ID: deployID, CreatedBy: owner, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Minute),
	}, nil)
	q.EXPECT().ExtendOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ExtendOwnedExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
			return postgres.ExerciseTestDeployment{ID: deployID, CreatedBy: owner, ExpiresAt: arg.ExpiresAt}, nil
		})
	expiry := &fakeExpiry{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q})
	uc.SetClock(func() time.Time { return now })
	uc.SetTestDeployExpiry(expiry)

	if _, err := uc.ExtendTestDeploy(context.Background(), owner, deployID); err != nil {
		t.Fatal(err)
	}
	want := scheduled{owner: owner, deploy: deployID, at: now.Add(2 * time.Hour)}
	if len(expiry.got) != 1 || expiry.got[0] != want {
		t.Fatalf("got %+v want %+v", expiry.got, want)
	}
}

func TestEndExpiredTestDeploy_RemovesTheExpiredLab(t *testing.T) {
	now := fixedNow()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	owner, id := fixedID(3), fixedID(5)
	row := postgres.ExerciseTestDeployment{ID: id, GroupName: "tu-x", LabName: "l-x", CreatedBy: owner, ExpiresAt: now}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return([]postgres.ExerciseTestDeployment{row}, nil)
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	uc.SetClock(func() time.Time { return now })

	if err := uc.EndExpiredTestDeploy(context.Background(), owner, id); err != nil {
		t.Fatal(err)
	}
	if infra.destroyed != "tu-x" {
		t.Fatalf("the author's last lab takes the group with it: %q", infra.destroyed)
	}
}

func TestEndExpiredTestDeploy_AnExtendedLabIsLeftAlone(t *testing.T) {
	now := fixedNow()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	owner, id := fixedID(3), fixedID(5)
	// The earlier job fires at now, but the lease was extended past it: nothing may be listed, deleted or destroyed.
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{
		ID: id, CreatedBy: owner, ExpiresAt: now.Add(2 * time.Hour),
	}, nil)
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	uc.SetClock(func() time.Time { return now })

	if err := uc.EndExpiredTestDeploy(context.Background(), owner, id); err != nil {
		t.Fatal(err)
	}
	if infra.destroyed != "" || len(infra.deletedLabs) != 0 {
		t.Fatalf("an extended lab must stay: %+v", infra)
	}
}

func TestEndExpiredTestDeploy_AnAlreadyEndedLabIsFine(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{}, pgx.ErrNoRows)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &fakeInfra{}})
	if err := uc.EndExpiredTestDeploy(context.Background(), fixedID(3), fixedID(5)); err != nil {
		t.Fatalf("a lab its author already ended is not an error: %v", err)
	}
}

func TestEndExpiredTestDeploy_AFailedAgentDeletionKeepsTheRowAndRetries(t *testing.T) {
	now := fixedNow()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	owner, id := fixedID(3), fixedID(5)
	row := postgres.ExerciseTestDeployment{ID: id, GroupName: "tu-x", LabName: "l-x", CreatedBy: owner, ExpiresAt: now}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return([]postgres.ExerciseTestDeployment{row}, nil)
	// No DeleteOwnedExerciseTestDeploy expectation: the row must stay.
	infra := &fakeInfra{destroyErr: errors.New("agent unreachable")}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	uc.SetClock(func() time.Time { return now })

	if err := uc.EndExpiredTestDeploy(context.Background(), owner, id); err == nil {
		t.Fatal("the failure must reach River so the job is retried")
	}
}

func TestDestroyDeployTest_EndsAnExpiredLabItsAuthorStillSees(t *testing.T) {
	now := fixedNow()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	owner, id := fixedID(3), fixedID(5)
	row := postgres.ExerciseTestDeployment{ID: id, GroupName: "tu-x", LabName: "l-x", CreatedBy: owner, ExpiresAt: now.Add(-17 * time.Minute)}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return([]postgres.ExerciseTestDeployment{row}, nil)
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	uc.SetClock(func() time.Time { return now })

	if err := uc.DestroyDeployTest(context.Background(), owner, id); err != nil {
		t.Fatalf("an expired lab can be terminated: %v", err)
	}
	if infra.destroyed != "tu-x" {
		t.Fatalf("destroyed %q", infra.destroyed)
	}
}

func TestDeployVariantTest_AnExpiredLabDoesNotCountAgainstTheLimit(t *testing.T) {
	now := fixedNow()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	owner := fixedID(3)
	dead := postgres.ExerciseTestDeployment{ID: fixedID(5), ExpiresAt: now.Add(-time.Minute)}
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return([]postgres.ExerciseTestDeployment{dead}, nil).AnyTimes()
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), gomock.Any()).Return(postgres.ExerciseVersion{}, errors.New("gone")).AnyTimes()
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &fakeInfra{}})
	uc.SetClock(func() time.Time { return now })

	_, err := uc.DeployVariantTest(context.Background(), owner, fixedID(2), fixedID(1))
	if errors.Is(err, exerciseModel.ErrTestDeployActiveExists.Err()) {
		t.Fatal("a lab past its lease is not an active one")
	}
}
