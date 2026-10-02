package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

type fakeResourceGate struct {
	err    error
	called int
	teams  int
	perTm  resourcesModel.Amount
}

func (f *fakeResourceGate) HoldsForAllTeams(_ context.Context, _ uuid.UUID, perTeam resourcesModel.Amount, teams int, _ resourcesModel.Amount) error {
	f.called++
	f.teams, f.perTm = teams, perTeam
	return f.err
}

// A new lab task of a running event is refused when the reservation cannot hold it for all teams.
func TestAttachExercise_RunningEventAsksTheResourceGate(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	gate := &fakeResourceGate{err: eventExerciseModel.ErrEventExerciseExists.Err()}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	uc.SetResourceGate(gate)
	eventID, exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(labVersion(versionID, exerciseID, now), nil).AnyTimes()
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID}, nil).AnyTimes()
	q.EXPECT().IsExerciseAvailableToEvent(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	q.EXPECT().EventHasActiveExerciseFamily(gomock.Any(), gomock.Any()).Return(false, nil).AnyTimes()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID}, nil).AnyTimes()
	q.EXPECT().ListEventExercises(gomock.Any(), eventID).Return(nil, nil).AnyTimes()
	q.EXPECT().ListVersionVariantDevices(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().ListEventExerciseDetails(gomock.Any(), eventID).Return(nil, nil).AnyTimes()
	running := startedEvent(eventID, now)
	running.InfrastructureAllowed = true
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(running, nil).AnyTimes()

	_, err := uc.AttachExercise(context.Background(), eventID, event.AttachExerciseInput{ExerciseVersionID: versionID, VariantMode: eventExerciseModel.VariantModePerTeam}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventExerciseModel.ErrEventExerciseExists.Err()) || gate.called != 1 || unit.saved {
		t.Fatalf("a refused task must not be attached: err=%v called=%d saved=%v", err, gate.called, unit.saved)
	}
	if gate.teams < 1 {
		t.Fatalf("the gate must be asked for the teams of the event, got %d", gate.teams)
	}
}
