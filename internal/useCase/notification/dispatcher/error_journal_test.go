package dispatcherUseCase_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/internal/model/notification/types"
	dispatcherUseCase "github.com/cybericebox/daemon/internal/useCase/notification/dispatcher"
	"github.com/cybericebox/daemon/internal/useCase/notification/dispatcher/mocks"
	"github.com/cybericebox/daemon/pkg/tools"
	"github.com/cybericebox/daemon/pkg/worker"
)

type collectingReporter struct {
	mu     sync.Mutex
	events []errorJournal.Event
}

func (c *collectingReporter) Report(e errorJournal.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

// An e-mail that still fails after its retry rounds goes to the error journal; an in-app copy that fails does not.
func TestProcess_EmailFailureAfterRetriesIsJournaled(t *testing.T) {
	reporter := &collectingReporter{}
	errorJournal.SetReporter(reporter)
	t.Cleanup(func() { errorJournal.SetReporter(nil) })

	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	mail := mocks.NewMockHandler(ctrl)
	mail.EXPECT().Channel().Return(notificationTypes.NotificationChannelEmail).AnyTimes()
	uc := dispatcherUseCase.NewNotificationDispatcher(dispatcherUseCase.Dependencies{
		Repo: repo, Enqueuer: worker.NewEnqueuer(), Handlers: []dispatcherUseCase.Handler{mail}, RetryDelay: time.Millisecond,
	})

	id, uid := tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().SetDispatchStatus(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Email: "real@b.test"}, nil)
	repo.EXPECT().GetActiveChannels(gomock.Any(), gomock.Any()).Return([]string{string(notificationTypes.NotificationChannelEmail)}, nil)
	mail.EXPECT().Handle(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("smtp: 550 mailbox real@b.test unavailable")).AnyTimes()
	repo.EXPECT().UpsertDispatchTarget(gomock.Any(), gomock.Any()).Return(nil)

	require.NoError(t, uc.ProcessNotification(context.Background(), dispatchModel.ProcessInput{
		DispatchID: id, UserID: uid, Type: string(notificationTypes.NotificationTypeFlagAccepted),
	}))

	require.Len(t, reporter.events, 1)
	e := reporter.events[0]
	require.Equal(t, errorJournal.KindMail, e.Kind)
	require.Equal(t, "email", e.Source)
	require.Equal(t, string(notificationTypes.NotificationTypeFlagAccepted), e.Details["notification_type"])
}
