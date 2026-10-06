package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
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

// variantsJSON is one variant with one container device of a resource preset.
func variantsJSON(preset string) []byte {
	return []byte(`[{"id":"` + uuid.Must(uuid.NewV7()).String() + `","tasks":[{"name":"Task","difficulty":"easy"}],"topology":{"devices":[{"id":"` + uuid.Must(uuid.NewV7()).String() +
		`","name":"web","type":"container","resource_preset":"` + preset + `"}]}}]`)
}

// gatedChangeFixture is a running event with one attached lab task pinned to a version of a small device and a
// newer version of a device of the given preset.
//
// The change itself never runs: without a unit of work the use case stops right after the gate.
func gatedChangeFixture(t *testing.T, oldPreset, newPreset string, gate *fakeResourceGate) (*event.EventUseCase, *testUoW, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	uc.SetResourceGate(gate)
	eventID, exerciseID, attachmentID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	oldID, newID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	oldVariants, newVariants := variantsJSON(oldPreset), variantsJSON(newPreset)
	forkID := uuid.Must(uuid.NewV7())
	newVersion := postgres.ExerciseVersion{ID: newID, ExerciseID: exerciseID, Status: "published", PublishedAt: pgtype.Timestamptz{Time: now, Valid: true}, Variants: newVariants, CreatedAt: now}
	attachment := postgres.EventExercise{ID: attachmentID, EventID: eventID, ExerciseID: exerciseID, ExerciseVersionID: oldID, Status: int16(eventExerciseModel.StatusActive), CreatedAt: now}
	q.EXPECT().GetEventExerciseByID(gomock.Any(), gomock.Any()).Return(attachment, nil).AnyTimes()
	oldVersion := postgres.ExerciseVersion{ID: oldID, ExerciseID: exerciseID, Status: "published", PublishedAt: pgtype.Timestamptz{Time: now, Valid: true}, Variants: oldVariants, CreatedAt: now}
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), newID).Return(newVersion, nil).AnyTimes()
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), oldID).Return(oldVersion, nil).AnyTimes()
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID}, nil).AnyTimes()
	// The event's own copy of the exercise (fork) is the newer version, under another exercise.
	forkVersion := newVersion
	forkVersion.ExerciseID = forkID
	q.EXPECT().FindEventFork(gomock.Any(), gomock.Any()).Return(postgres.Exercise{ID: forkID, Scope: int16(exerciseModel.ScopeEvent), PublishedVersionID: uuid.NullUUID{UUID: newID, Valid: true}}, nil).AnyTimes()
	q.EXPECT().GetExerciseByID(gomock.Any(), forkID).Return(postgres.Exercise{ID: forkID, Scope: int16(exerciseModel.ScopeEvent)}, nil).AnyTimes()
	q.EXPECT().IsExerciseAvailableToEvent(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID}, nil).AnyTimes()
	q.EXPECT().ListEventExercises(gomock.Any(), eventID).Return([]postgres.EventExercise{attachment}, nil).AnyTimes()
	q.EXPECT().ListVersionVariantDevices(gomock.Any(), gomock.Any()).Return([]postgres.ListVersionVariantDevicesRow{{VersionID: oldID, ExerciseID: exerciseID, Variants: oldVariants}}, nil).AnyTimes()
	q.EXPECT().ListEventExerciseDetails(gomock.Any(), eventID).Return(nil, nil).AnyTimes()
	running := startedEvent(eventID, now)
	running.InfrastructureAllowed = true
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(running, nil).AnyTimes()
	return uc, unit, eventID, attachmentID, newID
}

// Moving the pin of a task of a running event to a version that asks for more is checked against the reservation for
// all teams, by the size the plan of a team becomes; a version that does not ask for more is not checked.
func TestUpdateEventExercise_AGrowingVersionAsksTheResourceGate(t *testing.T) {
	gate := &fakeResourceGate{err: eventExerciseModel.ErrEventExerciseExists.Err()}
	uc, unit, eventID, attachmentID, newID := gatedChangeFixture(t, "micro", "large", gate)
	_, err := uc.UpdateEventExercise(context.Background(), eventID, attachmentID, &newID, uuid.Must(uuid.NewV7()), false)
	if !errors.Is(err, eventExerciseModel.ErrEventExerciseExists.Err()) || gate.called != 1 || unit.saved {
		t.Fatalf("a task that grew beyond the reservation must not be switched: err=%v called=%d saved=%v", err, gate.called, unit.saved)
	}
	// micro 25m/64Mi to large 250m/1Gi: the plan of a team grows by the difference.
	if gate.perTm.CPUMillicores < 250 || gate.teams < 1 {
		t.Fatalf("the gate must be asked for the new size of a team, got %+v for %d teams", gate.perTm, gate.teams)
	}

	same := &fakeResourceGate{err: eventExerciseModel.ErrEventExerciseExists.Err()}
	uc, _, eventID, attachmentID, newID = gatedChangeFixture(t, "micro", "micro", same)
	_, err = uc.UpdateEventExercise(context.Background(), eventID, attachmentID, &newID, uuid.Must(uuid.NewV7()), false)
	if same.called != 0 || errors.Is(err, eventExerciseModel.ErrEventExerciseExists.Err()) {
		t.Fatalf("a version of the same size is not checked: %d calls, %v", same.called, err)
	}

	// A version that asks for less passes the gate, too, even if the reservation is short of the plan as it is.
	smaller := &fakeResourceGate{err: eventExerciseModel.ErrEventExerciseExists.Err()}
	uc, _, eventID, attachmentID, newID = gatedChangeFixture(t, "large", "micro", smaller)
	if _, err = uc.UpdateEventExercise(context.Background(), eventID, attachmentID, &newID, uuid.Must(uuid.NewV7()), false); smaller.called != 0 {
		t.Fatalf("not checked: %d calls, %v", smaller.called, err)
	}
}

// The event's own copy of a task (fork) is checked like a new version when it already exists and asks for more.
func TestForkEventExercise_AGrowingCopyAsksTheResourceGate(t *testing.T) {
	gate := &fakeResourceGate{err: eventExerciseModel.ErrEventExerciseExists.Err()}
	uc, unit, eventID, attachmentID, _ := gatedChangeFixture(t, "micro", "large", gate)
	_, err := uc.ForkEventExercise(context.Background(), eventID, attachmentID, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventExerciseModel.ErrEventExerciseExists.Err()) || gate.called != 1 || unit.saved {
		t.Fatalf("a copy that grew beyond the reservation must not be switched to: err=%v called=%d saved=%v", err, gate.called, unit.saved)
	}
}
