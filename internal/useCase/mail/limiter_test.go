package mailUseCase

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/pkg/email"
)

var t0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// recordingLimiter is a limiter on a frozen clock whose sleeps only record how
// long each caller was told to wait.
func recordingLimiter() (*sendLimiter, func() []time.Duration) {
	var mu sync.Mutex
	var waits []time.Duration
	l := newSendLimiter(func() time.Time { return t0 })
	l.sleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		waits = append(waits, d)
		return nil
	}
	return l, func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		out := append([]time.Duration(nil), waits...)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
}

func TestLimiter_ConcurrentWorkersGetDistinctSlotsAtTheRate(t *testing.T) {
	l, waits := recordingLimiter()
	interval := mailModel.Limits{PerSecond: 14}.Interval()

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, l.wait(context.Background(), "platform", interval))
		}()
	}
	wg.Wait()

	got := waits()
	require.Len(t, got, 100)
	require.Zero(t, got[0], "the first message goes out at once")
	for i := 1; i < len(got); i++ {
		require.GreaterOrEqual(t, got[i]-got[i-1], interval, "slots are at least one interval apart")
	}
	for i := 0; i+14 < len(got); i++ {
		require.GreaterOrEqual(t, got[i+14]-got[i], time.Second-time.Microsecond, "15 messages never fit in one second")
	}
}

func TestLimiter_KeysAreIndependent(t *testing.T) {
	l, waits := recordingLimiter()
	require.NoError(t, l.wait(context.Background(), "platform", time.Second))
	require.NoError(t, l.wait(context.Background(), "event:a", time.Second))
	require.Equal(t, []time.Duration{0, 0}, waits())
}

func TestLimiter_FractionalRate(t *testing.T) {
	require.Equal(t, 2*time.Second, mailModel.Limits{PerSecond: 0.5}.Interval())
	require.Zero(t, mailModel.Limits{}.Interval())
}

func TestLimiter_LongQueueDefersInsteadOfBlocking(t *testing.T) {
	l, waits := recordingLimiter()
	var deferred int
	for range 30 {
		err := l.wait(context.Background(), "env", time.Second)
		if d, ok := dispatchModel.AsDeferred(err); ok {
			deferred++
			require.Greater(t, d.RetryAfter, maxRateWait)
			continue
		}
		require.NoError(t, err)
	}
	require.Greater(t, deferred, 0)
	require.Less(t, len(waits()), 30)
	for _, w := range waits() {
		require.LessOrEqual(t, w, maxRateWait)
	}
}

func TestLimiter_UnlimitedNeverWaits(t *testing.T) {
	l, waits := recordingLimiter()
	for range 5 {
		require.NoError(t, l.wait(context.Background(), "env", 0))
	}
	require.Empty(t, waits())
}

func TestLimiter_CancelledContextStopsWaiting(t *testing.T) {
	l := newSendLimiter(time.Now)
	require.NoError(t, l.wait(context.Background(), "k", 10*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, l.wait(ctx, "k", 10*time.Second), context.Canceled)
}

func TestResolveLimits_SavedThenEnvThenNone(t *testing.T) {
	env := mailModel.Limits{PerSecond: 14, DailyQuota: 50000}

	got, src := mailModel.ResolveLimits(mailModel.Limits{PerSecond: 5}, env)
	require.Equal(t, mailModel.Limits{PerSecond: 5, DailyQuota: 50000}, got)
	require.Equal(t, mailModel.LimitSources{PerSecond: mailModel.LimitSaved, DailyQuota: mailModel.LimitEnv}, src)

	got, src = mailModel.ResolveLimits(mailModel.Limits{}, mailModel.Limits{})
	require.True(t, got.Unlimited())
	require.Equal(t, mailModel.LimitSources{PerSecond: mailModel.LimitNone, DailyQuota: mailModel.LimitNone}, src)
}

func TestNormalizeLimits(t *testing.T) {
	f, n := 14.5, 50000
	perSecond, daily, err := mailModel.NormalizeLimits(&f, &n)
	require.NoError(t, err)
	require.Equal(t, 14.5, *perSecond)
	require.Equal(t, 50000, *daily)

	zero, zeroInt := 0.0, 0
	perSecond, daily, err = mailModel.NormalizeLimits(&zero, &zeroInt)
	require.NoError(t, err)
	require.Nil(t, perSecond)
	require.Nil(t, daily)

	neg, negInt := -1.0, -5
	_, _, err = mailModel.NormalizeLimits(&neg, nil)
	require.Error(t, err)
	_, _, err = mailModel.NormalizeLimits(nil, &negInt)
	require.Error(t, err)
}

func platformRowWithLimits(perSecond *float64, quota *int) postgres.MailSmtpConfig {
	row := platformRow
	if perSecond != nil {
		row.MaxPerSecond.Float64, row.MaxPerSecond.Valid = *perSecond, true
	}
	if quota != nil {
		row.DailyQuota.Int32, row.DailyQuota.Valid = int32(*quota), true
	}
	return row
}

func TestDeliver_DailyQuotaDefersInsteadOfFailing(t *testing.T) {
	uc, repo, smtp := newBareTestUseCase(t, config.SMTPConfig{}, nil)
	quota := 2
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRowWithLimits(nil, &quota)}, nil).AnyTimes()
	expectPlatformIdentity(repo, &platformIdentityRow)
	// The provider row has room for one more message, then the claim finds none.
	day := pgtype.Date{Time: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC), Valid: true}
	gomock.InOrder(
		repo.EXPECT().ReservePlatformSMTPSend(gomock.Any(), postgres.ReservePlatformSMTPSendParams{Day: day, ID: platformRow.ID}).Return(int32(2), nil),
		repo.EXPECT().ReservePlatformSMTPSend(gomock.Any(), postgres.ReservePlatformSMTPSendParams{Day: day, ID: platformRow.ID}).Return(int32(0), pgx.ErrNoRows),
	)
	repo.EXPECT().MarkPlatformSMTPUsed(gomock.Any(), gomock.Any()).Return(nil)

	msg := email.Message{To: "u@example.org"}
	require.NoError(t, uc.Deliver(context.Background(), nil, msg), "one below the quota is sent")

	err := uc.Deliver(context.Background(), nil, msg)
	d, ok := dispatchModel.AsDeferred(err)
	require.True(t, ok, "the quota defers, it does not fail: %v", err)
	require.Equal(t, dispatchModel.DeferredQuotaMessage, d.Message)
	require.Len(t, smtp.sends, 1, "nothing was sent past the quota")
}

func TestPlatformRoute_EnvLimitsApplyWhenNoProvidersAreSaved(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderEmail: "n@cybericebox.com", MaxPerSecond: 14, DailyQuota: 50000}
	uc, repo, _ := newTestUseCase(t, env, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)

	route, err := uc.platformRoute(context.Background())
	require.NoError(t, err)
	require.True(t, route.configured)
	require.Len(t, route.transports, 1)
	tr := route.transports[0]
	require.Equal(t, "env", tr.source)
	require.Nil(t, tr.providerID)
	require.Equal(t, mailModel.Limits{PerSecond: 14, DailyQuota: 50000}, tr.limits)
}

func TestPlatformRoute_ProviderCountsItsDailyLimitOnItsRow(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	perSecond, quota := 3.5, 100
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRowWithLimits(&perSecond, &quota)}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)

	route, err := uc.platformRoute(context.Background())
	require.NoError(t, err)
	require.Len(t, route.transports, 1)
	tr := route.transports[0]
	require.NotNil(t, tr.providerID)
	require.Equal(t, mailModel.Limits{PerSecond: 3.5}, tr.limits, "the daily limit is claimed on the row, not counted from the journal")
}

func TestDeliver_EventFallbackToExhaustedPlatformDefers(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	quota := 10
	full := platformRowWithLimits(nil, &quota)
	full.UsageDay = pgtype.Date{Time: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC), Valid: true}
	full.SentToday = 10
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{full}, nil).AnyTimes()
	expectPlatformIdentity(repo, &platformIdentityRow)
	expectEvent(repo, eventID, postgres.MailIdentity{}, &postgres.MailSmtpConfig{ID: uuid.Must(uuid.NewV7()), Host: "event.smtp", Port: 587, TlsMode: "starttls"})
	smtp.fail["event.smtp"] = errors.New("535 authentication failed")

	err := uc.Deliver(context.Background(), &eventID, email.Message{To: "p@example.org"})
	_, deferred := dispatchModel.AsDeferred(err)
	require.True(t, deferred, "the fallback found every provider over its daily limit: %v", err)
}

func TestDeliver_EventQuotaDefersWithoutPlatformFallback(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	quota := 5
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	eventRow := postgres.MailSmtpConfig{ID: uuid.Must(uuid.NewV7()), Host: "event.smtp", Port: 587, TlsMode: "starttls"}
	eventRow.DailyQuota.Int32, eventRow.DailyQuota.Valid = int32(quota), true
	expectEvent(repo, eventID, postgres.MailIdentity{}, &eventRow)
	repo.EXPECT().CountEmailDeliveredSince(gomock.Any(), postgres.CountEmailDeliveredSinceParams{
		Transport: "event", Since: uc.now().Add(-24 * time.Hour), EventID: uuid.NullUUID{UUID: eventID, Valid: true},
	}).Return(int64(5), nil)

	err := uc.Deliver(context.Background(), &eventID, email.Message{To: "p@example.org"})
	_, deferred := dispatchModel.AsDeferred(err)
	require.True(t, deferred, "%v", err)
	require.Empty(t, smtp.sends, "a deferred Event message does not leak to the platform transport")
}

func TestPlatformRoute_SavedProvidersWinOverEnv(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderEmail: "n@cybericebox.com"}
	uc, repo, smtp := newTestUseCase(t, env, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	repo.EXPECT().MarkPlatformSMTPUsed(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"}))
	require.Equal(t, "smtp-relay.brevo.com", smtp.sends[0].conn.Host, "a saved provider exists: env is ignored")
}

func TestPlatformRoute_AllProvidersDisabledFallsBackToEnv(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderEmail: "n@cybericebox.com"}
	uc, repo, smtp := newTestUseCase(t, env, nil)
	off := platformRow
	off.Enabled = false
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{off}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)

	note := &dispatchModel.DeliveryNote{}
	require.NoError(t, uc.Deliver(dispatchModel.WithDeliveryNote(context.Background(), note), nil, email.Message{To: "u@example.org"}))
	require.Len(t, smtp.sends, 1)
	require.Equal(t, "env.smtp", smtp.sends[0].conn.Host)
	require.Equal(t, "env", note.Transport)
}

func TestPlatformRoute_AllProvidersDisabledWithoutEnvIsNotConfigured(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	off := platformRow
	off.Enabled = false
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{off}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)

	require.ErrorIs(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"}), ErrNotConfigured)
	require.Empty(t, smtp.sends)
}
