package event

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
)

func TestNavigationPagesOfAHiddenEventAreForStaffOnly(t *testing.T) {
	eventID, staff, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, CreatedAt: time.Now()}, nil).AnyTimes()
	q.EXPECT().ListEventPages(gomock.Any(), eventID).Return(nil, nil).AnyTimes()
	q.EXPECT().GetEventManager(gomock.Any(), postgres.GetEventManagerParams{EventID: eventID, UserID: staff}).
		Return(postgres.EventManager{EventID: eventID, UserID: staff, Role: int16(eventManagerModel.RoleViewer)}, nil).AnyTimes()
	q.EXPECT().GetEventManager(gomock.Any(), postgres.GetEventManagerParams{EventID: eventID, UserID: other}).
		Return(postgres.EventManager{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows).AnyTimes()
	uc := NewEventUseCase(Dependencies{Repo: q})

	pages, err := uc.ListNavigationPages(context.Background(), eventID, ContentAccess{UserID: &staff})
	require.NoError(t, err)
	require.Empty(t, pages)

	_, err = uc.ListNavigationPages(context.Background(), eventID, ContentAccess{})
	require.ErrorIs(t, err, eventModel.ErrEventNotFound.Err())

	_, err = uc.ListNavigationPages(context.Background(), eventID, ContentAccess{UserID: &other})
	require.ErrorIs(t, err, eventModel.ErrEventNotFound.Err())
}
