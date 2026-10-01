package dispatcherUseCase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/model/notification"
	"github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/internal/model/notification/types"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	dispatcherUseCase "github.com/cybericebox/daemon/internal/useCase/notification/dispatcher"
	"github.com/cybericebox/daemon/internal/useCase/notification/dispatcher/mocks"
	"github.com/cybericebox/daemon/pkg/tools"
	"github.com/cybericebox/daemon/pkg/worker"
)

// setup creates a dispatcher use case with no handlers wired.
func setup(t *testing.T) (
	*gomock.Controller,
	*postgresMocks.MockQuerier,
	*dispatcherUseCase.NotificationDispatcher,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := dispatcherUseCase.NewNotificationDispatcher(
		dispatcherUseCase.Dependencies{Repo: repo, Enqueuer: worker.NewEnqueuer(), RetryDelay: time.Millisecond},
	)
	return ctrl, repo, uc
}

// setupFull creates a dispatcher use case with a mocked in-app handler wired.
// The handler is a MockHandler so the dispatcher test stays focused on routing.
func setupFull(t *testing.T) (
	ctrl *gomock.Controller,
	repo *postgresMocks.MockQuerier,
	handler *mocks.MockHandler,
	uc *dispatcherUseCase.NotificationDispatcher,
) {
	t.Helper()
	ctrl = gomock.NewController(t)
	repo = postgresMocks.NewMockQuerier(ctrl)
	handler = mocks.NewMockHandler(ctrl)
	// New() calls Channel() once per handler when building its routing map.
	handler.EXPECT().Channel().Return(notificationTypes.NotificationChannelInApp).AnyTimes()
	uc = dispatcherUseCase.NewNotificationDispatcher(
		dispatcherUseCase.Dependencies{
			Repo: repo, Enqueuer: worker.NewEnqueuer(),
			Handlers:   []dispatcherUseCase.Handler{handler},
			RetryDelay: time.Millisecond,
		},
	)
	return ctrl, repo, handler, uc
}

func TestProcess_MarksStartedAndDone(t *testing.T) {
	ctrl, repo, uc := setup(t)
	defer ctrl.Finish()
	id := tools.NewUUIDv7()
	uid := tools.NewUUIDv7()

	repo.EXPECT().SetDispatchStatus(
		gomock.Any(),
		postgres.SetDispatchStatusParams{ID: id, Status: "started"},
	).Return(nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(
		postgres.User{ID: uid, Email: "real@b.test"}, nil,
	)
	repo.EXPECT().GetActiveChannels(gomock.Any(), postgres.GetActiveChannelsParams{
		UserID:           uid,
		NotificationType: string(notificationTypes.NotificationTypeFlagAccepted),
	}).Return(nil, nil)
	repo.EXPECT().
		SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "done"}).
		Return(nil)

	require.NoError(
		t,
		uc.ProcessNotification(
			context.Background(),
			dispatchModel.ProcessInput{
				DispatchID: id,
				UserID:     uid,
				Type:       string(notificationTypes.NotificationTypeFlagAccepted),
			},
		),
	)
}

func TestProcess_InApp_HappyPath(t *testing.T) {
	ctrl, repo, handler, uc := setupFull(t)
	defer ctrl.Finish()

	id := tools.NewUUIDv7()
	uid := tools.NewUUIDv7()
	vars := map[string]any{"Name": "Alice"}

	repo.EXPECT().SetDispatchStatus(
		gomock.Any(),
		postgres.SetDispatchStatusParams{ID: id, Status: "started"},
	).Return(nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(
		postgres.User{ID: uid, Email: "real@b.test"}, nil,
	)
	repo.EXPECT().GetActiveChannels(gomock.Any(), postgres.GetActiveChannelsParams{
		UserID:           uid,
		NotificationType: string(notificationTypes.NotificationTypeFlagAccepted),
	}).Return([]string{string(notificationTypes.NotificationChannelInApp)}, nil)
	handler.EXPECT().Handle(
		gomock.Any(),
		gomock.Any(),
		notificationTypes.NotificationTypeFlagAccepted,
		vars,
		nil,
	).Return(nil).Times(1)
	repo.EXPECT().UpsertDispatchTarget(
		gomock.Any(), postgres.UpsertDispatchTargetParams{Attempts: 1,
			DispatchID: id,
			Channel:    string(notificationTypes.NotificationChannelInApp),
			Status:     "done",
			Error:      "",
		},
	).Return(nil)
	repo.EXPECT().
		SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "done"}).
		Return(nil)

	require.NoError(
		t, uc.ProcessNotification(
			context.Background(), dispatchModel.ProcessInput{
				DispatchID: id,
				UserID:     uid,
				Type:       string(notificationTypes.NotificationTypeFlagAccepted),
				Vars:       vars,
			},
		),
	)
}

func TestProcess_ForwardsSpecifiedTemplateIDToChannel(t *testing.T) {
	ctrl, repo, handler, uc := setupFull(t)
	defer ctrl.Finish()

	id := tools.NewUUIDv7()
	uid := tools.NewUUIDv7()
	templateID := tools.NewUUIDv7()
	recipient := userModel.User{ID: uid, Email: "preview@example.com"}

	repo.EXPECT().SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "started"}).Return(nil)
	handler.EXPECT().Handle(
		gomock.Any(), recipient, notificationTypes.NotificationTypeFlagAccepted, gomock.Any(), &templateID,
	).Return(nil)
	repo.EXPECT().UpsertDispatchTarget(gomock.Any(), postgres.UpsertDispatchTargetParams{Attempts: 1,
		DispatchID: id, Channel: string(notificationTypes.NotificationChannelInApp), Status: "done",
	}).Return(nil)
	repo.EXPECT().SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "done"}).Return(nil)

	require.NoError(t, uc.ProcessNotification(context.Background(), dispatchModel.ProcessInput{
		DispatchID: id,
		UserID:     uid,
		Type:       string(notificationTypes.NotificationTypeFlagAccepted),
		Recipient:  &recipient,
		TemplateID: &templateID,
		OverrideChannels: []notificationTypes.NotificationChannel{
			notificationTypes.NotificationChannelInApp,
		},
	}))
}

func TestProcess_InApp_NoTemplate(t *testing.T) {
	ctrl, repo, handler, uc := setupFull(t)
	defer ctrl.Finish()

	id := tools.NewUUIDv7()
	uid := tools.NewUUIDv7()

	repo.EXPECT().SetDispatchStatus(
		gomock.Any(),
		postgres.SetDispatchStatusParams{ID: id, Status: "started"},
	).Return(nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(
		postgres.User{ID: uid, Email: "real@b.test"}, nil,
	)
	repo.EXPECT().GetActiveChannels(gomock.Any(), postgres.GetActiveChannelsParams{
		UserID:           uid,
		NotificationType: string(notificationTypes.NotificationTypeFlagAccepted),
	}).Return([]string{string(notificationTypes.NotificationChannelInApp)}, nil)
	// Handler returns ErrTemplateNotFound → the retry loop stops immediately and
	// the target is recorded as "no template".
	handler.EXPECT().Handle(
		gomock.Any(),
		gomock.Any(),
		notificationTypes.NotificationTypeFlagAccepted,
		gomock.Any(),
		nil,
	).Return(notificationModel.ErrTemplateNotFound.Err()).Times(1)
	repo.EXPECT().UpsertDispatchTarget(
		gomock.Any(), postgres.UpsertDispatchTargetParams{Attempts: 1,
			DispatchID: id,
			Channel:    string(notificationTypes.NotificationChannelInApp),
			Status:     "error",
			Error:      "no template",
		},
	).Return(nil)
	repo.EXPECT().
		SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "done"}).
		Return(nil)

	require.NoError(
		t, uc.ProcessNotification(
			context.Background(), dispatchModel.ProcessInput{
				DispatchID: id,
				UserID:     uid,
				Type:       string(notificationTypes.NotificationTypeFlagAccepted),
			},
		),
	)
}

func TestProcess_Override_BypassesSettingsKeepsL1(t *testing.T) {
	// flag_accepted supports in_app + email. Override asks for both PLUS a
	// bogus channel. GetActiveChannels must NOT be consulted under override.
	// in_app handler is wired (setupFull); email handler is not, so only in_app fires.
	ctrl, repo, handler, uc := setupFull(t)
	defer ctrl.Finish()

	id := tools.NewUUIDv7()
	uid := tools.NewUUIDv7()
	vars := map[string]any{"Challenge": "SQLi", "Points": 100}

	repo.EXPECT().SetDispatchStatus(
		gomock.Any(),
		postgres.SetDispatchStatusParams{ID: id, Status: "started"},
	).Return(nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(
		postgres.User{ID: uid, Email: "real@b.test"}, nil,
	)
	// No GetActiveChannels expectation: override must skip it entirely.
	handler.EXPECT().Handle(
		gomock.Any(),
		gomock.Any(),
		notificationTypes.NotificationTypeFlagAccepted,
		vars,
		nil,
	).Return(nil).Times(1)
	repo.EXPECT().UpsertDispatchTarget(
		gomock.Any(), postgres.UpsertDispatchTargetParams{Attempts: 1,
			DispatchID: id,
			Channel:    string(notificationTypes.NotificationChannelInApp),
			Status:     "done",
			Error:      "",
		},
	).Return(nil)
	repo.EXPECT().
		SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "done"}).
		Return(nil)

	require.NoError(t, uc.ProcessNotification(
		context.Background(),
		dispatchModel.ProcessInput{
			DispatchID: id,
			UserID:     uid,
			Type:       string(notificationTypes.NotificationTypeFlagAccepted),
			Vars:       vars,
			OverrideChannels: []notificationTypes.NotificationChannel{
				notificationTypes.NotificationChannelInApp,
				notificationTypes.NotificationChannelEmail,
				"bogus",
			},
		},
	))
}

func TestProcess_L1Block_SkipsChannel(t *testing.T) {
	// TypeEmailConfirmation supports only ChannelEmail (L1 blocks in_app).
	// Even with L2 enabling in_app, the in-app handler must NOT be invoked and
	// no UpsertDispatchTarget must be called for the in_app channel.
	ctrl, repo, _, uc := setupFull(t)
	defer ctrl.Finish()

	id := tools.NewUUIDv7()
	uid := tools.NewUUIDv7()

	repo.EXPECT().SetDispatchStatus(
		gomock.Any(),
		postgres.SetDispatchStatusParams{ID: id, Status: "started"},
	).Return(nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(
		postgres.User{ID: uid, Email: "real@b.test"}, nil,
	)
	// Resolver returns in_app for email_confirmation — L1 will block it.
	repo.EXPECT().GetActiveChannels(gomock.Any(), postgres.GetActiveChannelsParams{
		UserID:           uid,
		NotificationType: string(notificationTypes.NotificationTypeEmailConfirmation),
	}).Return([]string{string(notificationTypes.NotificationChannelInApp)}, nil)
	// No handler.Handle calls expected; no UpsertDispatchTarget expected.
	repo.EXPECT().
		SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "done"}).
		Return(nil)

	require.NoError(
		t, uc.ProcessNotification(
			context.Background(), dispatchModel.ProcessInput{
				DispatchID: id,
				UserID:     uid,
				Type:       string(notificationTypes.NotificationTypeEmailConfirmation),
			},
		),
	)
}

func TestProcess_RetriesFailedChannel(t *testing.T) {
	// Handler returns a retryable error on the first 2 calls, then nil on the 3rd.
	// Expect exactly 3 Handle calls (1 per round) and the final target as "done".
	ctrl, repo, handler, uc := setupFull(t)
	defer ctrl.Finish()

	id := tools.NewUUIDv7()
	uid := tools.NewUUIDv7()
	someErr := errors.New("transient delivery error")

	repo.EXPECT().SetDispatchStatus(
		gomock.Any(),
		postgres.SetDispatchStatusParams{ID: id, Status: "started"},
	).Return(nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(
		postgres.User{ID: uid, Email: "real@b.test"}, nil,
	)
	repo.EXPECT().GetActiveChannels(gomock.Any(), postgres.GetActiveChannelsParams{
		UserID:           uid,
		NotificationType: string(notificationTypes.NotificationTypeFlagAccepted),
	}).Return([]string{string(notificationTypes.NotificationChannelInApp)}, nil)

	gomock.InOrder(
		handler.EXPECT().Handle(
			gomock.Any(),
			gomock.Any(),
			notificationTypes.NotificationTypeFlagAccepted,
			gomock.Any(),
			nil,
		).Return(someErr),
		handler.EXPECT().Handle(
			gomock.Any(),
			gomock.Any(),
			notificationTypes.NotificationTypeFlagAccepted,
			gomock.Any(),
			nil,
		).Return(someErr),
		handler.EXPECT().Handle(
			gomock.Any(),
			gomock.Any(),
			notificationTypes.NotificationTypeFlagAccepted,
			gomock.Any(),
			nil,
		).Return(nil),
	)

	repo.EXPECT().UpsertDispatchTarget(
		gomock.Any(), postgres.UpsertDispatchTargetParams{Attempts: 3,
			DispatchID: id,
			Channel:    string(notificationTypes.NotificationChannelInApp),
			Status:     "done",
			Error:      "",
		},
	).Return(nil)
	repo.EXPECT().
		SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "done"}).
		Return(nil)

	require.NoError(
		t, uc.ProcessNotification(
			context.Background(), dispatchModel.ProcessInput{
				DispatchID: id,
				UserID:     uid,
				Type:       string(notificationTypes.NotificationTypeFlagAccepted),
			},
		),
	)
}

func TestProcess_TemplateNotFound_NotRetried(t *testing.T) {
	// Handler returns ErrTemplateNotFound — this is terminal; Handle must be called
	// exactly ONCE (no retry rounds) and the target must be "error"/"no template".
	ctrl, repo, handler, uc := setupFull(t)
	defer ctrl.Finish()

	id := tools.NewUUIDv7()
	uid := tools.NewUUIDv7()

	repo.EXPECT().SetDispatchStatus(
		gomock.Any(),
		postgres.SetDispatchStatusParams{ID: id, Status: "started"},
	).Return(nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(
		postgres.User{ID: uid, Email: "real@b.test"}, nil,
	)
	repo.EXPECT().GetActiveChannels(gomock.Any(), postgres.GetActiveChannelsParams{
		UserID:           uid,
		NotificationType: string(notificationTypes.NotificationTypeFlagAccepted),
	}).Return([]string{string(notificationTypes.NotificationChannelInApp)}, nil)

	// Exactly one call — terminal error must NOT be retried.
	handler.EXPECT().Handle(
		gomock.Any(),
		gomock.Any(),
		notificationTypes.NotificationTypeFlagAccepted,
		gomock.Any(),
		nil,
	).Return(notificationModel.ErrTemplateNotFound.Err()).Times(1)

	repo.EXPECT().UpsertDispatchTarget(
		gomock.Any(), postgres.UpsertDispatchTargetParams{Attempts: 1,
			DispatchID: id,
			Channel:    string(notificationTypes.NotificationChannelInApp),
			Status:     "error",
			Error:      "no template",
		},
	).Return(nil)
	repo.EXPECT().
		SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "done"}).
		Return(nil)

	require.NoError(
		t, uc.ProcessNotification(
			context.Background(), dispatchModel.ProcessInput{
				DispatchID: id,
				UserID:     uid,
				Type:       string(notificationTypes.NotificationTypeFlagAccepted),
			},
		),
	)
}

// TestProcess_RecipientOverride_SkipsFetch asserts that when ProcessInput.Recipient
// is set the handler receives that override email and GetUserByID is NOT called
// (gomock strict mode fails the test if an unexpected GetUserByID call happens).
func TestProcess_RecipientOverride_SkipsFetch(t *testing.T) {
	ctrl, repo, handler, uc := setupFull(t)
	defer ctrl.Finish()

	id := tools.NewUUIDv7()
	uid := tools.NewUUIDv7()
	const overrideEmail = "override@b.test"

	overrideUser := userModel.User{ID: uid, Email: overrideEmail}

	// No GetUserByID expectation — gomock strict mode will fail if it's called.
	repo.EXPECT().SetDispatchStatus(
		gomock.Any(),
		postgres.SetDispatchStatusParams{ID: id, Status: "started"},
	).Return(nil)
	repo.EXPECT().GetActiveChannels(gomock.Any(), postgres.GetActiveChannelsParams{
		UserID:           uid,
		NotificationType: string(notificationTypes.NotificationTypeFlagAccepted),
	}).Return([]string{string(notificationTypes.NotificationChannelInApp)}, nil)

	handler.EXPECT().Handle(
		gomock.Any(),
		gomock.Any(),
		notificationTypes.NotificationTypeFlagAccepted,
		gomock.Any(),
		nil,
	).DoAndReturn(func(_ context.Context, user userModel.User, _ notificationTypes.NotificationType, _ map[string]any, _ *uuid.UUID, _ ...*uuid.UUID) error {
		require.Equal(t, overrideEmail, user.Email, "handler must receive the override email, not the stored one")
		return nil
	}).Times(1)

	repo.EXPECT().UpsertDispatchTarget(
		gomock.Any(), postgres.UpsertDispatchTargetParams{Attempts: 1,
			DispatchID: id,
			Channel:    string(notificationTypes.NotificationChannelInApp),
			Status:     "done",
			Error:      "",
		},
	).Return(nil)
	repo.EXPECT().
		SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "done"}).
		Return(nil)

	require.NoError(
		t, uc.ProcessNotification(
			context.Background(), dispatchModel.ProcessInput{
				DispatchID: id,
				UserID:     uid,
				Type:       string(notificationTypes.NotificationTypeFlagAccepted),
				Recipient:  &overrideUser,
			},
		),
	)
}

// TestProcess_RealEmailReachesHandler asserts that the real user email fetched
// from the repository is passed through to the channel handler — proving the
// empty-email bug is fixed.
func TestProcess_RealEmailReachesHandler(t *testing.T) {
	ctrl, repo, handler, uc := setupFull(t)
	defer ctrl.Finish()

	id := tools.NewUUIDv7()
	uid := tools.NewUUIDv7()
	const wantEmail = "real@b.test"

	repo.EXPECT().SetDispatchStatus(
		gomock.Any(),
		postgres.SetDispatchStatusParams{ID: id, Status: "started"},
	).Return(nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(
		postgres.User{ID: uid, Email: wantEmail}, nil,
	)
	repo.EXPECT().GetActiveChannels(gomock.Any(), postgres.GetActiveChannelsParams{
		UserID:           uid,
		NotificationType: string(notificationTypes.NotificationTypeFlagAccepted),
	}).Return([]string{string(notificationTypes.NotificationChannelInApp)}, nil)

	// Use DoAndReturn to capture the user argument and assert the email.
	handler.EXPECT().Handle(
		gomock.Any(),
		gomock.Any(),
		notificationTypes.NotificationTypeFlagAccepted,
		gomock.Any(),
		nil,
	).DoAndReturn(func(_ context.Context, user userModel.User, _ notificationTypes.NotificationType, _ map[string]any, _ *uuid.UUID, _ ...*uuid.UUID) error {
		require.Equal(t, wantEmail, user.Email, "handler must receive the real user email, not empty string")
		return nil
	}).Times(1)

	repo.EXPECT().UpsertDispatchTarget(
		gomock.Any(), postgres.UpsertDispatchTargetParams{Attempts: 1,
			DispatchID: id,
			Channel:    string(notificationTypes.NotificationChannelInApp),
			Status:     "done",
			Error:      "",
		},
	).Return(nil)
	repo.EXPECT().
		SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "done"}).
		Return(nil)

	require.NoError(
		t, uc.ProcessNotification(
			context.Background(), dispatchModel.ProcessInput{
				DispatchID: id,
				UserID:     uid,
				Type:       string(notificationTypes.NotificationTypeFlagAccepted),
			},
		),
	)
}

// TestProcess_JournalsDeliveryNote asserts the channel's delivery note (email
// route, address, Event SMTP fallback) reaches the journal target row.
func TestProcess_JournalsDeliveryNote(t *testing.T) {
	ctrl, repo, handler, uc := setupFull(t)
	defer ctrl.Finish()

	id, uid, eventID := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	recipient := userModel.User{ID: uid, Email: "p@b.test"}

	repo.EXPECT().SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "started"}).Return(nil)
	handler.EXPECT().Handle(gomock.Any(), recipient, notificationTypes.NotificationTypeFlagAccepted, gomock.Any(), nil, &eventID).
		DoAndReturn(func(ctx context.Context, _ userModel.User, _ notificationTypes.NotificationType, _ map[string]any, _ *uuid.UUID, _ ...*uuid.UUID) error {
			note := dispatchModel.DeliveryNoteFrom(ctx)
			require.NotNil(t, note)
			note.Transport, note.Recipient, note.FallbackError, note.ExtraAttempts = "platform", "p@b.test", "event smtp down", 1
			return nil
		})
	repo.EXPECT().UpsertDispatchTarget(gomock.Any(), postgres.UpsertDispatchTargetParams{
		DispatchID: id, Channel: string(notificationTypes.NotificationChannelInApp), Status: "done",
		Attempts: 2, Transport: "platform", Recipient: "p@b.test", FallbackError: "event smtp down",
	}).Return(nil)
	repo.EXPECT().SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "done"}).Return(nil)

	require.NoError(t, uc.ProcessNotification(context.Background(), dispatchModel.ProcessInput{
		DispatchID: id, UserID: uid, Type: string(notificationTypes.NotificationTypeFlagAccepted),
		Recipient: &recipient, ScopeEventID: &eventID,
		OverrideChannels: []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelInApp},
	}))
}

func TestProcess_DeferredEmail_RecordsDeferredAndSnoozes(t *testing.T) {
	ctrl, repo, handler, uc := setupFull(t)
	defer ctrl.Finish()

	id := tools.NewUUIDv7()
	uid := tools.NewUUIDv7()
	deferral := &dispatchModel.DeferredError{Message: dispatchModel.DeferredQuotaMessage, RetryAfter: 10 * time.Minute}

	repo.EXPECT().SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "started"}).Return(nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Email: "real@b.test"}, nil)
	repo.EXPECT().GetActiveChannels(gomock.Any(), gomock.Any()).
		Return([]string{string(notificationTypes.NotificationChannelInApp)}, nil)
	// Exactly one call: a send limit is waited out by the job, not by a retry round.
	handler.EXPECT().Handle(gomock.Any(), gomock.Any(), notificationTypes.NotificationTypeFlagAccepted, gomock.Any(), nil).
		Return(deferral).Times(1)
	repo.EXPECT().UpsertDispatchTarget(gomock.Any(), postgres.UpsertDispatchTargetParams{
		DispatchID: id, Channel: string(notificationTypes.NotificationChannelInApp),
		Status: "deferred", Error: dispatchModel.DeferredQuotaMessage, Attempts: 0,
	}).Return(nil)
	// The dispatch is not marked done: the job runs again.

	err := uc.ProcessNotification(context.Background(), dispatchModel.ProcessInput{
		DispatchID: id, UserID: uid, Type: string(notificationTypes.NotificationTypeFlagAccepted),
	})
	got, ok := dispatchModel.AsDeferred(err)
	require.True(t, ok, "the deferral reaches the worker: %v", err)
	require.Equal(t, 10*time.Minute, got.RetryAfter)
}
