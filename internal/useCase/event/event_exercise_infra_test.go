package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

// A set with a lab topology needs an event that allows infrastructure.
func labVersion(versionID, exerciseID uuid.UUID, now time.Time) postgres.ExerciseVersion {
	return postgres.ExerciseVersion{ID: versionID, ExerciseID: exerciseID, Status: string(exerciseModel.VersionStatusPublished), PublishedAt: pgtype.Timestamptz{Time: now, Valid: true},
		Variants: []byte(`[{"tasks":[{"name":"Task","difficulty":"easy"}],"topology":{"devices":[{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"web"}]}}]`), CreatedAt: now}
}

func TestAttachExercise_RefusesInfrastructureWithoutEventInfrastructure(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(labVersion(versionID, exerciseID, now), nil)
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID}, nil)
	q.EXPECT().IsExerciseAvailableToEvent(gomock.Any(), gomock.Any()).Return(true, nil)
	infraOff := startedEvent(eventID, now)
	infraOff.InfrastructureAllowed = false
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(infraOff, nil)

	_, err := uc.AttachExercise(context.Background(), eventID, event.AttachExerciseInput{ExerciseVersionID: versionID, VariantMode: eventExerciseModel.VariantModePerTeam}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventExerciseModel.ErrEventExerciseInfrastructureNotAllowed.Err()) || unit.saved {
		t.Fatalf("want ErrEventExerciseInfrastructureNotAllowed without saving, got %v", err)
	}
}

func TestUpdateEventExercise_RefusesVersionAddingInfrastructure(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, exerciseID, attachmentID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventExerciseByID(gomock.Any(), gomock.Any()).Return(postgres.EventExercise{ID: attachmentID, EventID: eventID, ExerciseID: exerciseID, ExerciseVersionID: uuid.Must(uuid.NewV7()), Status: int16(eventExerciseModel.StatusActive), CreatedAt: now}, nil)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(labVersion(versionID, exerciseID, now), nil)
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID}, nil)
	q.EXPECT().IsExerciseAvailableToEvent(gomock.Any(), gomock.Any()).Return(true, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)

	_, err := uc.UpdateEventExercise(context.Background(), eventID, attachmentID, &versionID, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventExerciseModel.ErrEventExerciseInfrastructureNotAllowed.Err()) || unit.saved {
		t.Fatalf("want ErrEventExerciseInfrastructureNotAllowed without saving, got %v", err)
	}
}
