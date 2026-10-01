package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventChallengeGroupModel "github.com/cybericebox/daemon/internal/model/eventChallengeGroup"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestReorderChallengeGroups_VacatesBeforeSwapping(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	firstID := uuid.Must(uuid.NewV7())
	secondID := uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().ListEventChallengeGroups(gomock.Any(), eventID).Return([]postgres.EventChallengeGroup{
		{ID: firstID, EventID: eventID, Name: "Web", OrderIndex: 0, CreatedAt: now},
		{ID: secondID, EventID: eventID, Name: "Crypto", OrderIndex: 1, CreatedAt: now},
	}, nil)
	gomock.InOrder(
		q.EXPECT().VacateEventChallengeGroupOrders(gomock.Any(), eventID).Return(nil),
		q.EXPECT().SetEventChallengeGroupOrder(gomock.Any(), postgres.SetEventChallengeGroupOrderParams{ID: secondID, EventID: eventID, OrderIndex: 0}).Return(int64(1), nil),
		q.EXPECT().SetEventChallengeGroupOrder(gomock.Any(), postgres.SetEventChallengeGroupOrderParams{ID: firstID, EventID: eventID, OrderIndex: 1}).Return(int64(1), nil),
	)

	if err := uc.ReorderChallengeGroups(context.Background(), eventID, event.ReorderChallengeGroupsInput{GroupIDs: []uuid.UUID{secondID, firstID}}); err != nil {
		t.Fatalf("ReorderChallengeGroups: %v", err)
	}
	if !unit.saved {
		t.Fatal("reorder transaction was not saved")
	}
}

func TestReorderChallengeGroups_RejectsIncompleteOrder(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	firstID := uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().ListEventChallengeGroups(gomock.Any(), eventID).Return([]postgres.EventChallengeGroup{
		{ID: firstID, EventID: eventID, Name: "Web", OrderIndex: 0, CreatedAt: now},
		{ID: uuid.Must(uuid.NewV7()), EventID: eventID, Name: "Crypto", OrderIndex: 1, CreatedAt: now},
	}, nil)

	err := uc.ReorderChallengeGroups(context.Background(), eventID, event.ReorderChallengeGroupsInput{GroupIDs: []uuid.UUID{firstID}})
	if !errors.Is(err, eventChallengeGroupModel.ErrChallengeGroupOrderInvalid.Err()) {
		t.Fatalf("err = %v, want ErrChallengeGroupOrderInvalid", err)
	}
	if unit.saved {
		t.Fatal("invalid reorder must not be saved")
	}
}

func TestUpdateChallengeGroup_MapsUniqueViolationToConflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	groupID := uuid.Must(uuid.NewV7())

	q.EXPECT().ListEventChallengeGroups(gomock.Any(), eventID).Return([]postgres.EventChallengeGroup{
		{ID: groupID, EventID: eventID, Name: "Web", OrderIndex: 0, CreatedAt: time.Now()},
	}, nil)
	q.EXPECT().UpdateEventChallengeGroup(gomock.Any(), gomock.Any()).Return(postgres.EventChallengeGroup{}, &pgconn.PgError{Code: pgerrcode.UniqueViolation})

	_, err := uc.UpdateChallengeGroup(context.Background(), eventID, groupID, event.UpdateChallengeGroupInput{Name: "Web", Order: 1})
	if !errors.Is(err, eventChallengeGroupModel.ErrChallengeGroupUpdateConflict.Err()) {
		t.Fatalf("err = %v, want ErrChallengeGroupUpdateConflict", err)
	}
}
