package inboxUseCase_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
	"github.com/cybericebox/daemon/pkg/tools"
)

// An event reservation that ends while the event runs reaches the super admins and the organizers once each;
// a reduced one only the admins.
func TestEndingNotifiesRecipients(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	notifier := &fakeNotifier{}
	router := inboxUseCase.NewRequestRouter(repo, notifier)
	admin, organizer, both, event := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.User, error) {
		return postgres.User{ID: id, Email: "x@example.org"}, nil
	}).AnyTimes()
	repo.EXPECT().ListSuperAdminUserIDs(gomock.Any()).Return([]uuid.UUID{admin, both}, nil).Times(2)
	repo.EXPECT().ListEventWriteManagerUserIDs(gomock.Any(), event).Return([]uuid.UUID{organizer, both}, nil)

	n := inboxUseCase.ReservationNotice{EventID: event, EventName: "CTF", EndsAt: time.Date(2026, 10, 19, 15, 5, 0, 0, time.UTC)}
	require.NoError(t, router.ReservationEnding(context.Background(), n))
	require.Len(t, notifier.sent, 3, "every admin and organizer once")
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeReservationEnding), notifier.sent[0].typ)
	assert.Equal(t, "18:05", notifier.sent[0].vars["ends_at"], "the time is Kyiv time")
	assert.Equal(t, "CTF", notifier.sent[0].vars["event_name"])

	notifier.sent = nil
	require.NoError(t, router.ReservationReduced(context.Background(), n))
	assert.Len(t, notifier.sent, 2)

	notifier.sent = nil
	author := tools.NewUUIDv7()
	require.NoError(t, router.TestLabEnding(context.Background(), author, tools.NewUUIDv7(), "Web basics", n.EndsAt))
	require.NoError(t, router.TestLabEnded(context.Background(), author, tools.NewUUIDv7(), "Web basics"))
	require.Len(t, notifier.sent, 2)
	assert.Equal(t, author, notifier.sent[0].userID)
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeTestLabEnding), notifier.sent[0].typ)
	assert.Equal(t, "Web basics", notifier.sent[0].vars["exercise_name"])
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeTestLabEnded), notifier.sent[1].typ)
}

func TestClusterReservationTypesSupportInApp(t *testing.T) {
	for _, typ := range []string{inboxModel.TypeReservationEnding, inboxModel.TypeReservationReduced, inboxModel.TypeTestLabEnding, inboxModel.TypeTestLabEnded} {
		assert.True(t, notificationTypes.Supports(notificationTypes.NotificationType(typ), notificationTypes.NotificationChannelInApp), typ)
	}
}
