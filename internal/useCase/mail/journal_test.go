package mailUseCase

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
)

// expectJournaled stubs the three journal writes of one test send and
// captures the dispatch and its email target.
func expectJournaled(repo *postgresMocks.MockQuerier) (*postgres.CreateDispatchParams, *postgres.UpsertDispatchTargetParams) {
	created, target := &postgres.CreateDispatchParams{}, &postgres.UpsertDispatchTargetParams{}
	repo.EXPECT().CreateDispatch(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateDispatchParams) (postgres.NotificationDispatch, error) {
			*created = arg
			return postgres.NotificationDispatch{}, nil
		})
	repo.EXPECT().UpsertDispatchTarget(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpsertDispatchTargetParams) error {
			*target = arg
			return nil
		})
	repo.EXPECT().SetDispatchStatus(gomock.Any(), gomock.Any()).Return(nil)
	return created, target
}

func TestTestPlatformSMTP_JournalsSuccessAsPlatformTest(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderEmail: "notifications@mail.cybericebox.com"}
	uc, repo, _ := newTestUseCase(t, env, nil)
	userID := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(postgres.User{ID: userID, Email: "admin@example.org"}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).AnyTimes()
	expectPlatformIdentity(repo, nil)
	created, target := expectJournaled(repo)

	res, err := uc.TestPlatformProvider(context.Background(), nil, nil, userID)
	require.NoError(t, err)
	require.True(t, res.Sent)
	require.Equal(t, dispatchModel.TypeSMTPTest, created.NotificationType)
	require.Equal(t, userID, created.RecipientUserID)
	require.False(t, created.ScopeEventID.Valid, "platform tests have no Event scope")
	require.Equal(t, "email", target.Channel)
	require.Equal(t, string(dispatchModel.TargetStatusDone), target.Status)
	require.Equal(t, "env", target.Transport)
	require.Equal(t, "admin@example.org", target.Recipient)
	require.Empty(t, target.Error)
}

func TestTestPlatformSMTP_JournalsFailureWithError(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderEmail: "notifications@mail.cybericebox.com"}
	uc, repo, smtp := newTestUseCase(t, env, nil)
	smtp.fail["env.smtp"] = errors.New("554 message rejected")
	userID := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(postgres.User{ID: userID, Email: "admin@example.org"}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).AnyTimes()
	expectPlatformIdentity(repo, nil)
	_, target := expectJournaled(repo)

	res, err := uc.TestPlatformProvider(context.Background(), nil, nil, userID)
	require.NoError(t, err)
	require.False(t, res.Sent)
	require.Equal(t, string(dispatchModel.TargetStatusError), target.Status)
	require.Equal(t, "554 message rejected", target.Error)
}

func TestTestPlatformSMTP_JournalsNotConfigured(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	userID := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(postgres.User{ID: userID, Email: "admin@example.org"}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).AnyTimes()
	expectPlatformIdentity(repo, nil)
	_, target := expectJournaled(repo)

	res, err := uc.TestPlatformProvider(context.Background(), nil, nil, userID)
	require.NoError(t, err)
	require.False(t, res.Sent)
	require.Equal(t, string(dispatchModel.TargetStatusError), target.Status)
	require.Equal(t, "platform", target.Transport)
	require.Equal(t, ErrNotConfigured.Error(), target.Error)
}

func TestTestEventSMTP_JournalsScopedToTheEvent(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	smtp.fail["event.smtp"] = errors.New("535 authentication failed")
	repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(postgres.User{ID: userID, Email: "org@example.org"}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil).AnyTimes()
	expectPlatformIdentity(repo, &platformIdentityRow)
	expectEvent(repo, eventID, postgres.MailIdentity{}, &postgres.MailSmtpConfig{ID: uuid.Must(uuid.NewV7()), Host: "event.smtp", Port: 587, TlsMode: "starttls"})
	created, target := expectJournaled(repo)

	res, err := uc.TestEventSMTP(context.Background(), eventID, nil, userID)
	require.NoError(t, err)
	require.False(t, res.Sent)
	require.Equal(t, dispatchModel.TypeSMTPTest, created.NotificationType)
	require.Equal(t, uuid.NullUUID{UUID: eventID, Valid: true}, created.ScopeEventID)
	require.Equal(t, "event", target.Transport)
	require.Equal(t, "org@example.org", target.Recipient)
	require.Equal(t, "535 authentication failed", target.Error)
}

// An administrator cannot fire test emails in a burst: the second one inside the cooldown is refused with the
// seconds to wait, another administrator is not held up, and the first may try again once the cooldown passed.
func TestTestPlatformProvider_CooldownPerAdministrator(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderEmail: "notifications@mail.cybericebox.com"}
	uc, repo, _ := newTestUseCase(t, env, nil)
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	uc.now = func() time.Time { return clock }
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, id := range []uuid.UUID{first, second} {
		repo.EXPECT().GetUserByID(gomock.Any(), id).Return(postgres.User{ID: id, Email: id.String() + "@example.org"}, nil).AnyTimes()
	}
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).AnyTimes()
	expectPlatformIdentity(repo, nil)
	repo.EXPECT().CreateDispatch(gomock.Any(), gomock.Any()).Return(postgres.NotificationDispatch{}, nil).AnyTimes()
	repo.EXPECT().UpsertDispatchTarget(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	repo.EXPECT().SetDispatchStatus(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	_, err := uc.TestPlatformProvider(context.Background(), nil, nil, first)
	require.NoError(t, err)

	clock = clock.Add(3 * time.Second)
	_, err = uc.TestPlatformProvider(context.Background(), nil, nil, first)
	require.True(t, mailModel.ErrTestMailTooSoon.Err().Is(err), "%v", err)
	require.Equal(t, http.StatusTooManyRequests, mailModel.ErrTestMailTooSoon.Err().StatusCode().HTTPCode())

	_, err = uc.TestPlatformProvider(context.Background(), nil, nil, second)
	require.NoError(t, err, "the cooldown is per administrator")

	clock = clock.Add(8 * time.Second)
	_, err = uc.TestPlatformProvider(context.Background(), nil, nil, first)
	require.NoError(t, err)
}
