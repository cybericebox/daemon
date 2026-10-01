package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestSetEventManagerPublishesSignalForAssigneeInSameTransaction(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	publisher := &recordingSignalPublisher{}
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit},
		SignalPublishers: func(event.IRepository) event.SignalPublisher { return publisher },
	})
	eventID, assigneeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetUserByID(gomock.Any(), assigneeID).Return(postgres.User{ID: assigneeID, Role: "user"}, nil)
	q.EXPECT().GetEventManager(gomock.Any(), postgres.GetEventManagerParams{EventID: eventID, UserID: assigneeID}).Return(postgres.EventManager{}, pgx.ErrNoRows)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "spring", Name: "Spring CTF", AvailableFrom: now, ArchiveAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}}, nil)
	q.EXPECT().UpsertEventManager(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventManagerParams) (postgres.EventManager, error) {
		return postgres.EventManager{EventID: arg.EventID, UserID: arg.UserID, Role: arg.Role, CreatedAt: arg.CreatedAt}, nil
	})
	view, err := uc.SetEventManager(context.Background(), eventID, event.SetEventManagerInput{UserID: assigneeID, Role: int16(eventManagerModel.RoleViewer)})
	require.NoError(t, err)
	require.Equal(t, int16(eventManagerModel.RoleViewer), view.Role)
	require.True(t, unit.saved)
	require.Equal(t, []signalModel.Type{signalModel.TypeEventManagerAssigned}, publisher.types)
	payload := publisher.payloads[0].(*signalModel.EventManagerPayload)
	require.Equal(t, assigneeID, payload.SubjectUserID)
	require.Equal(t, eventID, payload.ScopeEventID)
	require.Equal(t, "observer", payload.ManagerRole)
	require.Equal(t, "Spring CTF", payload.EventName)
}

func TestSetEventManagerSameRoleDoesNotNotifyAgain(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	publisher := &recordingSignalPublisher{}
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit},
		SignalPublishers: func(event.IRepository) event.SignalPublisher { return publisher },
	})
	eventID, assigneeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventManager(gomock.Any(), postgres.GetEventManagerParams{EventID: eventID, UserID: assigneeID}).Return(postgres.EventManager{EventID: eventID, UserID: assigneeID, Role: int16(eventManagerModel.RoleManager), CreatedAt: now}, nil)
	view, err := uc.SetEventManager(context.Background(), eventID, event.SetEventManagerInput{UserID: assigneeID, Role: int16(eventManagerModel.RoleManager)})
	require.NoError(t, err)
	require.Equal(t, now, view.CreatedAt)
	require.Empty(t, publisher.types)
	require.False(t, unit.saved)
}

func TestSetEventManagerRejectsViewerRoleForPlatformStaff(t *testing.T) {
	for _, role := range []string{"admin", "admin_viewer", "super_admin"} {
		t.Run(role, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			publisher := &recordingSignalPublisher{}
			uc := event.NewEventUseCase(event.Dependencies{
				Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}},
				SignalPublishers: func(event.IRepository) event.SignalPublisher { return publisher },
			})
			eventID, adminID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			q.EXPECT().GetUserByID(gomock.Any(), adminID).Return(postgres.User{ID: adminID, Role: role}, nil)
			_, err := uc.SetEventManager(context.Background(), eventID, event.SetEventManagerInput{UserID: adminID, Role: int16(eventManagerModel.RoleViewer)})
			require.ErrorIs(t, err, eventManagerModel.ErrEventManagerViewerRedundant.Err())
			require.Empty(t, publisher.types)
		})
	}
}

func TestSetEventManagerAllowsWriteRoleForPlatformAdmin(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}},
		SignalPublishers: func(event.IRepository) event.SignalPublisher { return &recordingSignalPublisher{} },
	})
	eventID, adminID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventManager(gomock.Any(), postgres.GetEventManagerParams{EventID: eventID, UserID: adminID}).Return(postgres.EventManager{}, pgx.ErrNoRows)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "spring", Name: "Spring CTF", AvailableFrom: now, ArchiveAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}}, nil)
	q.EXPECT().UpsertEventManager(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventManagerParams) (postgres.EventManager, error) {
		return postgres.EventManager{EventID: arg.EventID, UserID: arg.UserID, Role: arg.Role, CreatedAt: arg.CreatedAt}, nil
	})
	view, err := uc.SetEventManager(context.Background(), eventID, event.SetEventManagerInput{UserID: adminID, Role: int16(eventManagerModel.RoleManager)})
	require.NoError(t, err)
	require.Equal(t, int16(eventManagerModel.RoleManager), view.Role)
}
