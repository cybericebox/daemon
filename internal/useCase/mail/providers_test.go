package mailUseCase

import (
	"context"
	"fmt"
	"net/textproto"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/pkg/email"
)

var testDay = pgtype.Date{Time: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC), Valid: true}

func providerRow(name, host string, priority int32) postgres.MailSmtpConfig {
	return postgres.MailSmtpConfig{
		ID: uuid.NewV5(uuid.Nil, name), Name: name, Enabled: true, Priority: priority,
		Host: host, Port: 587, TlsMode: "starttls", CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
}

// reserveOK stubs a successful daily claim of one provider.
func reserveOK(repo *postgresMocks.MockQuerier, id uuid.UUID) *gomock.Call {
	return repo.EXPECT().ReservePlatformSMTPSend(gomock.Any(), postgres.ReservePlatformSMTPSendParams{Day: testDay, ID: id}).Return(int32(1), nil)
}

func TestDeliver_FailsOverToNextProviderOnAuthError(t *testing.T) {
	uc, repo, smtp := newBareTestUseCase(t, config.SMTPConfig{}, nil)
	a, b := providerRow("a", "smtp.a", 0), providerRow("b", "smtp.b", 1)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{b, a}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	smtp.fail["smtp.a"] = fmt.Errorf("email: failed to send: %w", &textproto.Error{Code: 535, Msg: "authentication failed"})

	repo.EXPECT().ReservePlatformSMTPSend(gomock.Any(), postgres.ReservePlatformSMTPSendParams{Day: testDay, ID: a.ID}).Return(int32(1), nil)
	repo.EXPECT().ReleasePlatformSMTPSend(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.ReleasePlatformSMTPSendParams) error {
		require.Equal(t, a.ID, p.ID, "the failing provider gets its slot back")
		require.Contains(t, p.Error, "535", "and keeps the error")
		require.Equal(t, testDay, p.Day)
		return nil
	})
	repo.EXPECT().ReservePlatformSMTPSend(gomock.Any(), postgres.ReservePlatformSMTPSendParams{Day: testDay, ID: b.ID}).Return(int32(1), nil)
	repo.EXPECT().MarkPlatformSMTPUsed(gomock.Any(), postgres.MarkPlatformSMTPUsedParams{At: uc.now(), ID: b.ID}).Return(nil)

	note := &dispatchModel.DeliveryNote{}
	require.NoError(t, uc.Deliver(dispatchModel.WithDeliveryNote(context.Background(), note), nil, email.Message{To: "u@example.org"}))
	require.Len(t, smtp.sends, 2)
	require.Equal(t, "smtp.a", smtp.sends[0].conn.Host, "the lower priority number goes first")
	require.Equal(t, "smtp.b", smtp.sends[1].conn.Host)
	require.Equal(t, int32(1), note.ExtraAttempts)
	require.Contains(t, note.FallbackError, "535")
}

func TestDeliver_RejectedRecipientDoesNotTryOtherProviders(t *testing.T) {
	uc, repo, smtp := newBareTestUseCase(t, config.SMTPConfig{}, nil)
	a, b := providerRow("a", "smtp.a", 0), providerRow("b", "smtp.b", 1)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{a, b}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	rejected := fmt.Errorf("email: failed to send: %w", &textproto.Error{Code: 550, Msg: "no such mailbox"})
	smtp.fail["smtp.a"] = rejected

	reserveOK(repo, a.ID)
	repo.EXPECT().ReleasePlatformSMTPSend(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.ReleasePlatformSMTPSendParams) error {
		require.Empty(t, p.Error, "a bad recipient is not the provider's error")
		return nil
	})

	err := uc.Deliver(context.Background(), nil, email.Message{To: "gone@example.org"})
	require.ErrorIs(t, err, rejected)
	require.Len(t, smtp.sends, 1, "the next provider would say the same")
}

func TestDeliver_ProviderOverDailyLimitIsSkipped(t *testing.T) {
	uc, repo, smtp := newBareTestUseCase(t, config.SMTPConfig{}, nil)
	full, open := providerRow("full", "smtp.full", 0), providerRow("open", "smtp.open", 1)
	full.DailyQuota.Int32, full.DailyQuota.Valid = 100, true
	full.UsageDay, full.SentToday = testDay, 100
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{full, open}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	reserveOK(repo, open.ID)
	repo.EXPECT().MarkPlatformSMTPUsed(gomock.Any(), gomock.Any()).Return(nil)

	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"}))
	require.Len(t, smtp.sends, 1)
	require.Equal(t, "smtp.open", smtp.sends[0].conn.Host, "the exhausted provider is not even tried")
}

func TestDeliver_ProviderWinsTheClaimRaceLoser_FallsThrough(t *testing.T) {
	uc, repo, smtp := newBareTestUseCase(t, config.SMTPConfig{}, nil)
	a, b := providerRow("a", "smtp.a", 0), providerRow("b", "smtp.b", 1)
	a.DailyQuota.Int32, a.DailyQuota.Valid = 5, true // 4 sent when read; another replica takes the last slot
	a.UsageDay, a.SentToday = testDay, 4
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{a, b}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	repo.EXPECT().ReservePlatformSMTPSend(gomock.Any(), postgres.ReservePlatformSMTPSendParams{Day: testDay, ID: a.ID}).Return(int32(0), pgx.ErrNoRows)
	reserveOK(repo, b.ID)
	repo.EXPECT().MarkPlatformSMTPUsed(gomock.Any(), gomock.Any()).Return(nil)

	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"}))
	require.Equal(t, "smtp.b", smtp.sends[0].conn.Host)
}

func TestDeliver_AllProvidersFailReturnsTheFailure(t *testing.T) {
	uc, repo, smtp := newBareTestUseCase(t, config.SMTPConfig{}, nil)
	a, b := providerRow("a", "smtp.a", 0), providerRow("b", "smtp.b", 1)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{a, b}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	quota := fmt.Errorf("email: failed to send: %w", &textproto.Error{Code: 552, Msg: "quota exceeded"})
	smtp.fail["smtp.a"], smtp.fail["smtp.b"] = quota, quota
	repo.EXPECT().ReservePlatformSMTPSend(gomock.Any(), gomock.Any()).Return(int32(1), nil).Times(2)
	repo.EXPECT().ReleasePlatformSMTPSend(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	err := uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"})
	require.ErrorIs(t, err, quota)
	_, deferred := dispatchModel.AsDeferred(err)
	require.False(t, deferred)
}

func TestDeliver_EveryProviderExhaustedDefers(t *testing.T) {
	uc, repo, smtp := newBareTestUseCase(t, config.SMTPConfig{}, nil)
	a := providerRow("a", "smtp.a", 0)
	a.DailyQuota.Int32, a.DailyQuota.Valid = 3, true
	a.UsageDay, a.SentToday = testDay, 3
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{a}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)

	err := uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"})
	d, ok := dispatchModel.AsDeferred(err)
	require.True(t, ok, "%v", err)
	require.Equal(t, dispatchModel.DeferredQuotaMessage, d.Message)
	require.Empty(t, smtp.sends)
}

func TestDeliver_CounterOfYesterdayDoesNotBlockToday(t *testing.T) {
	uc, repo, smtp := newBareTestUseCase(t, config.SMTPConfig{}, nil)
	a := providerRow("a", "smtp.a", 0)
	a.DailyQuota.Int32, a.DailyQuota.Valid = 3, true
	a.UsageDay, a.SentToday = pgtype.Date{Time: testDay.Time.Add(-24 * time.Hour), Valid: true}, 3
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{a}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	reserveOK(repo, a.ID)
	repo.EXPECT().MarkPlatformSMTPUsed(gomock.Any(), gomock.Any()).Return(nil)

	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"}))
	require.Len(t, smtp.sends, 1)
}

func TestDeliver_ProviderSenderOverridesPlatformSenderButNotEventSender(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	a := providerRow("a", "smtp.a", 0)
	a.FromAddress, a.FromName = "ses@verified.example", "Via SES"
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{a}, nil).Times(2)
	expectPlatformIdentity(repo, &platformIdentityRow)
	eventID := uuid.Must(uuid.NewV7())
	expectEvent(repo, eventID, postgres.MailIdentity{}, nil)

	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"}))
	require.Equal(t, email.Address{Name: "Via SES", Email: "ses@verified.example"}, smtp.sends[0].msg.From)
	require.Equal(t, "support@cybericebox.com", smtp.sends[0].msg.ReplyTo.Email, "empty provider Reply-To uses the platform one")

	require.NoError(t, uc.Deliver(context.Background(), &eventID, email.Message{To: "p@example.org"}))
	require.Equal(t, "olymp@mail.cybericebox.com", smtp.sends[1].msg.From.Email, "Event mail keeps the Event sender")
}

func TestGetPlatformMailSettings_ListsProvidersWithUsage(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderEmail: "n@cybericebox.com"}
	uc, repo, _ := newTestUseCase(t, env, nil)
	a := providerRow("a", "smtp.a", 0)
	a.DailyQuota.Int32, a.DailyQuota.Valid = 100, true
	a.UsageDay, a.SentToday, a.LastError = testDay, 40, "535 authentication failed"
	b := providerRow("b", "smtp.b", 1)
	b.UsageDay, b.SentToday = pgtype.Date{Time: testDay.Time.Add(-24 * time.Hour), Valid: true}, 9000
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{a, b}, nil)
	expectPlatformIdentity(repo, nil)

	view, err := uc.GetPlatformMailSettings(context.Background())
	require.NoError(t, err)
	require.False(t, view.EnvActive, "saved providers exist: env is not the transport in use")
	require.Equal(t, "database", view.Source)
	require.Len(t, view.Providers, 2)
	require.Equal(t, 40, view.Providers[0].SentToday)
	require.Equal(t, "535 authentication failed", view.Providers[0].LastError)
	require.Equal(t, 0, view.Providers[1].SentToday, "yesterday's count does not show today")
	require.False(t, view.Providers[0].Exhausted)
	require.Equal(t, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), view.Providers[0].ResetsAt)
}

func TestGetPlatformMailSettings_EnvIsInUseOnlyWithoutProviders(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderEmail: "n@cybericebox.com"}
	uc, repo, _ := newTestUseCase(t, env, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil)
	expectPlatformIdentity(repo, nil)

	view, err := uc.GetPlatformMailSettings(context.Background())
	require.NoError(t, err)
	require.True(t, view.EnvActive)
	require.Equal(t, "env", view.Source)
	require.NotNil(t, view.Env)
	require.Empty(t, view.Providers)
}

func TestReorderPlatformProviders_ListedFirstThenTheRest(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	a, b, c := providerRow("a", "h", 0), providerRow("b", "h", 1), providerRow("c", "h", 2)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{a, b, c}, nil).Times(2)
	expectPlatformIdentity(repo, &platformIdentityRow)
	repo.EXPECT().ReorderPlatformSMTPProviders(gomock.Any(), []uuid.UUID{c.ID, a.ID, b.ID}).Return(nil)

	_, err := uc.ReorderPlatformProviders(context.Background(), []uuid.UUID{c.ID, uuid.Must(uuid.NewV7())})
	require.NoError(t, err)
}

func TestSetPlatformProviderEnabled_UnknownIsNotFound(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().SetPlatformSMTPProviderEnabled(gomock.Any(), gomock.Any()).Return(postgres.MailSmtpConfig{}, pgx.ErrNoRows)
	_, err := uc.SetPlatformProviderEnabled(context.Background(), uuid.Must(uuid.NewV7()), true, uuid.Must(uuid.NewV7()))
	require.ErrorIs(t, err, mailModel.ErrProviderNotFound.Err())
}
