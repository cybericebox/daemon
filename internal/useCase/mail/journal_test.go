package mailUseCase

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
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
