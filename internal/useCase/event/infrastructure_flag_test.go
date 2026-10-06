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
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func unpublishedStoredEvent(id uuid.UUID, allowed bool, now time.Time) postgres.Event {
	return postgres.Event{ID: id, Tag: "infra", AvailableFrom: now.Add(24 * time.Hour), PublishAt: now.Add(24 * time.Hour),
		StartAt: now.Add(24 * time.Hour), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}, InfrastructureAllowed: allowed}
}

func TestSetEventInfrastructure(t *testing.T) {
	now := time.Now()
	by := uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		name       string
		capability event.InfrastructureCapability
		stored     func(uuid.UUID) postgres.Event
		attached   int32
		allowed    bool
		wantErr    error
		wantWrite  bool
	}{
		{name: "turn on before publication", capability: availableLaboratoriesCapability{},
			stored: func(id uuid.UUID) postgres.Event { return unpublishedStoredEvent(id, false, now) }, allowed: true, wantWrite: true},
		{name: "turn off with nothing attached", capability: availableLaboratoriesCapability{},
			stored: func(id uuid.UUID) postgres.Event { return unpublishedStoredEvent(id, true, now) }, allowed: false, wantWrite: true},
		{name: "turn on needs laboratory", capability: unavailableInfrastructureCapability{},
			stored: func(id uuid.UUID) postgres.Event { return unpublishedStoredEvent(id, false, now) }, allowed: true,
			wantErr: eventModel.ErrEventInfrastructureUnavailable.Err()},
		{name: "turn off with attached sets", capability: availableLaboratoriesCapability{},
			stored: func(id uuid.UUID) postgres.Event { return unpublishedStoredEvent(id, true, now) }, attached: 1, allowed: false,
			wantErr: eventModel.ErrEventInfrastructureInUse.Err()},
		{name: "locked after publication", capability: availableLaboratoriesCapability{},
			stored: func(id uuid.UUID) postgres.Event { return startedEvent(id, now) }, allowed: true,
			wantErr: eventModel.ErrEventInfrastructureLocked.Err()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}, InfrastructureCapability: tc.capability})
			id := uuid.Must(uuid.NewV7())
			q.EXPECT().GetEventByID(gomock.Any(), id).Return(tc.stored(id), nil).AnyTimes()
			if tc.attached > 0 || (!tc.allowed && tc.wantWrite) {
				q.EXPECT().CountEventInfrastructureExercises(gomock.Any(), id).Return(tc.attached, nil)
			}
			if tc.wantWrite {
				q.EXPECT().UpdateEventInfrastructure(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, arg postgres.UpdateEventInfrastructureParams) (int64, error) {
						if arg.InfrastructureAllowed != tc.allowed || arg.UpdatedBy.UUID != by {
							t.Fatalf("written flag/actor wrong: %+v", arg)
						}
						return 1, nil
					})
			}
			view, err := uc.SetEventInfrastructure(context.Background(), id, tc.allowed, by)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || view.InfrastructureAllowed != tc.allowed {
				t.Fatalf("view = %+v, err = %v", view, err)
			}
		})
	}
}

func TestSetEventInfrastructure_ConcurrentEditIsAConflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}, InfrastructureCapability: availableLaboratoriesCapability{}})
	id, now := uuid.Must(uuid.NewV7()), time.Now()
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(unpublishedStoredEvent(id, false, now), nil).Times(2)
	q.EXPECT().UpdateEventInfrastructure(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	_, err := uc.SetEventInfrastructure(context.Background(), id, true, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventModel.ErrEventModified.Err()) {
		t.Fatalf("want ErrEventModified, got %v", err)
	}
}
