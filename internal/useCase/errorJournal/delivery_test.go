package errorJournalUseCase

import (
	"context"
	"errors"
	"testing"
	"time"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

func TestDeliveryRetriesWithoutAnotherErrorAndSurvivesJournalRestart(t *testing.T) {
	h := newHarness(DefaultConfig())
	h.repo.afterRecord = nil
	h.tg.errs["100"] = errors.New("network unreachable")
	rec, err := h.j.Record(context.Background(), errorJournal.Event{Kind: errorJournal.KindPanic, Message: "database outage"})
	require.NoError(t, err)
	require.Zero(t, h.tg.count("100"), "capture must never invoke the network")
	h.deliverAll()
	rows, err := h.repo.ListDeliveries(context.Background(), rec.Group.ID)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, d := range rows {
		if d.Channel == ChannelTelegram {
			require.Equal(t, 1, d.Attempts)
			require.Equal(t, "network unreachable", d.LastError)
			require.Nil(t, d.DeliveredAt)
		}
	}
	require.Len(t, h.mail.sent, 1, "healthy channel completes even when Telegram fails")
	h.j = New(Dependencies{Repo: h.repo, Telegram: h.tg, Mailer: h.mail, Config: DefaultConfig()})
	h.j.now = func() time.Time { return h.now }
	delete(h.tg.errs, "100")
	h.now = h.now.Add(time.Minute)
	h.deliverAll()
	require.Equal(t, 1, h.tg.count("100"))
	require.Len(t, h.mail.sent, 1, "restart and retry must not resend successful email")
	rows, err = h.repo.ListDeliveries(context.Background(), rec.Group.ID)
	require.NoError(t, err)
	for _, d := range rows {
		require.NotNil(t, d.DeliveredAt)
		require.Empty(t, d.LastError)
	}
}

func TestFailedChannelsDoNotSetLastNotifiedAndCaptureKeepsWorking(t *testing.T) {
	h := newHarness(DefaultConfig())
	h.repo.afterRecord = nil
	h.j.mail = nil
	h.tg.errs["100"] = errors.New("network unreachable")
	rec, err := h.j.Record(context.Background(), errorJournal.Event{Kind: errorJournal.KindPanic, Message: "network outage"})
	require.NoError(t, err)
	h.deliverAll()
	g, err := h.repo.GetGroup(context.Background(), rec.Group.ID)
	require.NoError(t, err)
	require.Nil(t, g.LastNotifiedAt)
	for range 30 {
		_, err = h.j.Record(context.Background(), errorJournal.Event{Kind: errorJournal.KindPanic, Message: "network outage"})
		require.NoError(t, err)
	}
	g, err = h.repo.GetGroup(context.Background(), rec.Group.ID)
	require.NoError(t, err)
	require.EqualValues(t, 31, g.Occurrences)
	rows, err := h.repo.ListDeliveries(context.Background(), rec.Group.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "pending batch coalesces repeats")
}

func TestQueuedEmailRechecksEligibilityBeforeSending(t *testing.T) {
	h := newHarness(DefaultConfig())
	h.repo.afterRecord = nil
	r := &recipientRepo{fakeRepo: h.repo, users: []RecipientUser{{Email: "ops@example.org", Role: "super_admin", Status: "active", EmailConfirmed: true}}}
	h.j.repo = r
	rec, err := h.j.Record(context.Background(), errorJournal.Event{Kind: errorJournal.KindPanic, Message: "boom"})
	require.NoError(t, err)
	roots, err := h.repo.ListDeliveries(context.Background(), rec.Group.ID)
	require.NoError(t, err)
	require.Len(t, roots, 1)
	require.NoError(t, h.j.ProcessJournalDelivery(context.Background(), roots[0].ID))
	r.users[0].Status = "blocked"
	h.deliverAll()
	require.Empty(t, h.mail.sent)
	rows, err := h.repo.ListDeliveries(context.Background(), rec.Group.ID)
	require.NoError(t, err)
	for _, d := range rows {
		if d.Channel == ChannelEmail {
			require.Nil(t, d.DeliveredAt)
			require.NotNil(t, d.CancelledAt)
			require.Contains(t, d.LastError, "no longer")
		}
	}
}
func TestManualNotificationTestDoesNotHideRecipientLookupFailure(t *testing.T) {
	h := newHarness(DefaultConfig())
	h.j.repo = &recipientRepo{fakeRepo: h.repo, lookupErr: errors.New("database unavailable")}
	_, err := h.j.SendErrorJournalTest(context.Background())
	require.Error(t, err)
	require.Zero(t, h.tg.count("100"))
	require.Empty(t, h.mail.sent)
}

func TestPurgedDeliveryReplayCompletesWithoutSendingOrRetry(t *testing.T) {
	h := newHarness(DefaultConfig())
	require.NoError(t, h.j.ProcessJournalDelivery(context.Background(), uuid.Must(uuid.NewV7())))
	require.Empty(t, h.mail.sent)
	require.Zero(t, h.tg.count("100"))
}

type stalledTelegram struct{ entered chan struct{} }

func (t stalledTelegram) Enabled() bool { return true }
func (t stalledTelegram) Send(ctx context.Context, _, _ string) error {
	close(t.entered)
	<-ctx.Done()
	return ctx.Err()
}
func TestCaptureContinuesWhileRiverDeliveryIsBlockedInNetwork(t *testing.T) {
	h := newHarness(DefaultConfig())
	h.repo.afterRecord = nil
	h.j.mail = nil
	transport := stalledTelegram{entered: make(chan struct{})}
	h.j.tg = transport
	rec, err := h.j.Record(context.Background(), errorJournal.Event{Kind: errorJournal.KindPanic, Message: "network outage"})
	require.NoError(t, err)
	roots, err := h.repo.ListDeliveries(context.Background(), rec.Group.ID)
	require.NoError(t, err)
	require.NoError(t, h.j.ProcessJournalDelivery(context.Background(), roots[0].ID))
	targets, err := h.repo.ListDeliveries(context.Background(), rec.Group.ID)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deliveryDone := make(chan struct{})
	go func() { defer close(deliveryDone); _ = h.j.ProcessJournalDelivery(ctx, targets[0].ID) }()
	select {
	case <-transport.entered:
	case <-time.After(time.Second):
		t.Fatal("sender never started")
	}
	captured := make(chan error, 1)
	go func() {
		_, err := h.j.Record(context.Background(), errorJournal.Event{Kind: errorJournal.KindPanic, Message: "a different failure"})
		captured <- err
	}()
	select {
	case err := <-captured:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("network stalled error capture")
	}
	cancel()
	<-deliveryDone
}
