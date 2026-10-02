package exercise_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
	calendarUseCase "github.com/cybericebox/daemon/internal/useCase/resourceCalendar"
)

type fakeGate struct {
	err      error
	admitted []calendarUseCase.TestLabRequest
	released []uuid.UUID
}

func (f *fakeGate) AdmitTestLab(_ context.Context, req calendarUseCase.TestLabRequest) (calendarUseCase.TestLabRoom, error) {
	f.admitted = append(f.admitted, req)
	if f.err != nil {
		return calendarUseCase.TestLabRoom{}, f.err
	}
	return calendarUseCase.TestLabRoom{Available: true, Via: calModel.ViaPool}, nil
}
func (f *fakeGate) ReleaseTestLab(_ context.Context, id uuid.UUID) error {
	f.released = append(f.released, id)
	return nil
}
func (f *fakeGate) ExtendTestLab(context.Context, uuid.UUID, uuid.UUID, calendarUseCase.Amount, time.Time) error {
	return nil
}

func gateFixture(t *testing.T) (*postgresMocks.MockQuerier, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	variantID, versionID, ownerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	device := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx"}
	variants, _ := json.Marshal([]exerciseModel.Variant{{ID: variantID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{device}}}})
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil)
	return q, variantID, versionID, ownerID
}

// A test laboratory is admitted by the calendar before anything is created, with the size of its devices; when
// the calendar has no room the author gets that error and nothing was deployed.
func TestDeployVariantTest_CalendarRefusalStopsBeforeAnyLabIsCreated(t *testing.T) {
	q, variantID, versionID, ownerID := gateFixture(t)
	noRoom := calModel.ErrNoTestLabRoom.WithContext("nearest_from", "2026-10-05T13:00:00Z").Err()
	gate := &fakeGate{err: noRoom}
	infra := &fakeInfra{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	uc.SetTestLabGate(gate)

	_, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID)
	if !errors.Is(err, noRoom) && err != noRoom {
		t.Fatalf("the calendar's refusal must reach the author, got %v", err)
	}
	if len(gate.admitted) != 1 || gate.admitted[0].Owner != ownerID || gate.admitted[0].Size.CPUMillicores == 0 || gate.admitted[0].Size.MemoryBytes == 0 {
		t.Fatalf("admission request = %+v", gate.admitted)
	}
	if infra.deployedTopo.Devices != nil || infra.destroyed != "" {
		t.Fatalf("nothing may be deployed after a refusal: %+v", infra)
	}
}

// A hold that was admitted is released when the laboratory fails to start.
func TestDeployVariantTest_ReleasesTheHoldWhenTheLabFailsToStart(t *testing.T) {
	q, variantID, versionID, ownerID := gateFixture(t)
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		return postgres.ExerciseTestDeployment{ID: p.ID, GroupName: p.GroupName, CreatedBy: p.CreatedBy}, nil
	})
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	gate := &fakeGate{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &fakeInfra{deployErr: errors.New("lab creation failed")}})
	uc.SetTestLabGate(gate)

	if _, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID); err == nil {
		t.Fatal("deploy failure must be returned")
	}
	if len(gate.admitted) != 1 || len(gate.released) == 0 || gate.released[0] != gate.admitted[0].ID {
		t.Fatalf("the hold must be released: admitted=%+v released=%v", gate.admitted, gate.released)
	}
}
