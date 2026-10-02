package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

type countingListener struct{ n int }

func (l *countingListener) Invalidate() { l.n++ }

func TestEventTagExists_AsksTheRepositoryNotTheLiveWindow(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)

	q.EXPECT().EventTagExists(gomock.Any(), "archived").Return(true, nil)
	q.EXPECT().EventTagExists(gomock.Any(), "gone").Return(false, nil)

	if ok, err := uc.EventTagExists(context.Background(), "archived"); err != nil || !ok {
		t.Fatalf("existing tag: ok=%v err=%v", ok, err)
	}
	if ok, err := uc.EventTagExists(context.Background(), "gone"); err != nil || ok {
		t.Fatalf("unknown tag: ok=%v err=%v", ok, err)
	}
}

func TestTagListener_CreateUpdateDeleteInvalidate(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	l := &countingListener{}
	uc.SetTagListener(l)

	adminID := uuid.Must(uuid.NewV7())
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	q.EXPECT().CountLiveEventsWithTag(gomock.Any(), gomock.Any()).Return(int64(0), nil).Times(2)
	q.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEventParams) (postgres.Event, error) {
			return postgres.Event{ID: arg.ID, Tag: arg.Tag, Name: arg.Name, AvailableFrom: arg.AvailableFrom,
				ArchiveAt: arg.ArchiveAt, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		})
	q.EXPECT().CreateEventConfig(gomock.Any(), gomock.Any()).Return(postgres.EventConfig{}, nil)
	q.EXPECT().CreateEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, nil)

	created, err := uc.CreateEvent(context.Background(), event.CreateEventInput{
		Tag: "myevent", Name: "My Event", AvailableFrom: from, ArchiveAt: to, CreatedBy: adminID,
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if l.n != 1 {
		t.Fatalf("create must invalidate once, got %d", l.n)
	}

	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	q.EXPECT().GetEventByID(gomock.Any(), created.ID).Return(postgres.Event{
		ID: created.ID, Tag: "myevent", Name: "My Event", AvailableFrom: from,
		ArchiveAt: pgtype.Timestamptz{Time: to, Valid: true},
		CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().UpdateEvent(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	if _, err = uc.UpdateEvent(context.Background(), created.ID, event.UpdateEventInput{
		Tag: "renamed", Name: "My Event", AvailableFrom: from, ArchiveAt: to,
	}, adminID); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if l.n != 2 {
		t.Fatalf("retag must invalidate, got %d", l.n)
	}

	q.EXPECT().ArchiveEventExercises(gomock.Any(), gomock.Any()).Return(nil)
	q.EXPECT().DeleteEvent(gomock.Any(), created.ID).Return(int64(1), nil)
	if err = uc.DeleteEvent(context.Background(), created.ID); err != nil {
		t.Fatalf("DeleteEvent: %v", err)
	}
	if l.n != 3 {
		t.Fatalf("delete must invalidate, got %d", l.n)
	}
}

func TestTagListener_FailedWritesDoNotInvalidate(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	l := &countingListener{}
	uc.SetTagListener(l)
	id := uuid.Must(uuid.NewV7())

	q.EXPECT().ArchiveEventExercises(gomock.Any(), gomock.Any()).Return(nil)
	q.EXPECT().DeleteEvent(gomock.Any(), id).Return(int64(0), nil)
	if err := uc.DeleteEvent(context.Background(), id); err == nil {
		t.Fatal("want not found")
	}
	if l.n != 0 {
		t.Fatalf("a failed delete must not invalidate, got %d", l.n)
	}
}
