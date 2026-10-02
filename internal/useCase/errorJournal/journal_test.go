package errorJournalUseCase

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/pkg/telegram"
)

var ctx = context.Background()

func fiveXX(msg string) errorJournal.Event {
	return errorJournal.Event{Kind: errorJournal.KindHTTP5xx, Source: "/api/x/:id", Route: "/api/x/:id", Method: "GET", HTTPStatus: 500, Message: msg}
}

func TestRecordGroupsAlikeErrorsAndKeepsFewSamples(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SamplesPerGroup = 2
	h := newHarness(cfg)

	for i := 0; i < 5; i++ {
		h.now = h.now.Add(time.Second)
		_, err := h.j.Record(ctx, fiveXX("timeout after "+string(rune('1'+i))+"s for 0198c1f2-7b3a-7c11-9d2e-3f4a5b6c7d8e"))
		require.NoError(t, err)
	}
	groups, total, err := h.j.ListErrorGroups(ctx, GroupFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	assert.EqualValues(t, 5, groups[0].Occurrences)
	detail, err := h.j.GetErrorGroup(ctx, groups[0].ID)
	require.NoError(t, err)
	assert.Len(t, detail.Samples, 2)
}

func TestRecordScrubsMessageStackAndDetails(t *testing.T) {
	h := newHarness(DefaultConfig())
	uid := uuid.Must(uuid.NewV7())
	rec, err := h.j.Record(ctx, errorJournal.Event{
		Kind: errorJournal.KindHTTP5xx, Source: "/api/login", Message: "send to anna@example.org failed password=hunter2 from 10.0.0.7",
		Stack: "token=abcdef123456 at x.go:1", RequestID: "req-1", UserID: &uid,
		Details: map[string]string{"reason": "bearer abcdefghijklmnop12345"},
	})
	require.NoError(t, err)
	s := h.repo.samples[rec.Group.ID][0]
	for _, leak := range []string{"anna@example.org", "hunter2", "10.0.0.7", "abcdef123456", "abcdefghijklmnop12345"} {
		assert.NotContains(t, s.Message+s.Stack+s.Details["reason"]+rec.Group.Title, leak)
	}
	assert.Equal(t, "req-1", s.RequestID)
	assert.Equal(t, &uid, s.UserID)
	// the text sent to people is built from the scrubbed sample as well
	assert.NotContains(t, h.tg.last("100"), "hunter2")
}

func TestNewFingerprintNotifiesOnceInsideTheCooldown(t *testing.T) {
	h := newHarness(DefaultConfig())
	_, err := h.j.Record(ctx, fiveXX("boom"))
	require.NoError(t, err)
	assert.Equal(t, 1, h.tg.count("100"))
	assert.Len(t, h.mail.sent, 1)

	h.now = h.now.Add(time.Minute)
	_, err = h.j.Record(ctx, fiveXX("boom"))
	require.NoError(t, err)
	assert.Equal(t, 1, h.tg.count("100"), "a known 5xx below the spike threshold is silent")
}

func TestPanicsAlwaysNotifyButAStormIsOneMessageWithACount(t *testing.T) {
	cfg := DefaultConfig()
	cfg.NotifyCooldown = 10 * time.Minute
	h := newHarness(cfg)
	panicEvent := errorJournal.Event{Kind: errorJournal.KindPanic, Source: "/api/p", Message: "runtime error: index out of range [3] with length 2"}

	for i := 0; i < 50; i++ {
		h.now = h.now.Add(time.Second)
		_, err := h.j.Record(ctx, panicEvent)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, h.tg.count("100"), "a storm inside the cooldown is one message")

	h.now = h.now.Add(11 * time.Minute)
	_, err := h.j.Record(ctx, panicEvent)
	require.NoError(t, err)
	require.Equal(t, 2, h.tg.count("100"))
	assert.Contains(t, h.tg.last("100"), "since the last message")
	assert.Contains(t, h.tg.last("100"), "Total: 51")
}

func TestSpikeOfAKnownFingerprintNotifies(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SpikeThreshold, cfg.SpikeWindow, cfg.NotifyCooldown = 5, time.Minute, time.Minute
	h := newHarness(cfg)

	_, _ = h.j.Record(ctx, fiveXX("boom"))
	require.Equal(t, 1, h.tg.count("100"))
	h.now = h.now.Add(10 * time.Minute) // the first one is out of the spike window
	for i := 0; i < 3; i++ {
		h.now = h.now.Add(2 * time.Second)
		_, _ = h.j.Record(ctx, fiveXX("boom"))
	}
	assert.Equal(t, 1, h.tg.count("100"))
	for i := 0; i < 3; i++ {
		h.now = h.now.Add(2 * time.Second)
		_, _ = h.j.Record(ctx, fiveXX("boom"))
	}
	assert.Equal(t, 2, h.tg.count("100"), "the burst is told once")
}

func TestRefusalsNotifyOnlyForANewFingerprintAndRateLimitsOnlyOnASpike(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SpikeThreshold, cfg.NotifyCooldown = 3, time.Second
	h := newHarness(cfg)
	forbidden := errorJournal.Event{Kind: errorJournal.KindHTTP403, Route: "/api/a", Method: "GET", Role: "admin", Permission: "users.read", HTTPStatus: 403}
	_, _ = h.j.Record(ctx, forbidden)
	h.now = h.now.Add(time.Hour)
	_, _ = h.j.Record(ctx, forbidden)
	assert.Equal(t, 1, h.tg.count("100"))
	forbidden.Permission = "users.delete" // another permission is another fingerprint
	_, _ = h.j.Record(ctx, forbidden)
	assert.Equal(t, 2, h.tg.count("100"))

	limited := errorJournal.Event{Kind: errorJournal.KindHTTP429, Route: "/api/b", Method: "POST", Limiter: "per-user", HTTPStatus: 429}
	_, _ = h.j.Record(ctx, limited)
	_, _ = h.j.Record(ctx, limited)
	assert.Equal(t, 2, h.tg.count("100"), "a rare 429 is silent")
	_, _ = h.j.Record(ctx, limited)
	assert.Equal(t, 3, h.tg.count("100"), "a burst of 429 is told")
}

func TestIgnoredGroupsAreRecordedButSilent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.NotifyCooldown = time.Second
	h := newHarness(cfg)
	e := errorJournal.Event{Kind: errorJournal.KindJob, Source: "mail", Message: "smtp down"}
	rec, _ := h.j.Record(ctx, e)
	_, err := h.j.SetErrorGroupStatus(ctx, rec.Group.ID, errorJournal.StatusIgnored)
	require.NoError(t, err)
	h.now = h.now.Add(time.Hour)
	_, _ = h.j.Record(ctx, e)
	assert.Equal(t, 1, h.tg.count("100"))
	assert.EqualValues(t, 2, h.repo.groups[rec.Group.Fingerprint].Occurrences)
}

func TestAResolvedGroupThatComesBackNotifiesAgain(t *testing.T) {
	h := newHarness(DefaultConfig())
	e := errorJournal.Event{Kind: errorJournal.KindMail, Source: "email", Message: "smtp down"}
	rec, _ := h.j.Record(ctx, e)
	_, err := h.j.SetErrorGroupStatus(ctx, rec.Group.ID, errorJournal.StatusResolved)
	require.NoError(t, err)
	h.now = h.now.Add(time.Second)
	again, _ := h.j.Record(ctx, e)
	assert.True(t, again.Reopened)
	assert.Equal(t, 2, h.tg.count("100"))
}

func TestEmailGoesToTheListAndTheSuperAdminsWithoutDuplicates(t *testing.T) {
	h := newHarness(DefaultConfig())
	h.repo.settings.EmailToSuperAdmins = true
	h.repo.admins = []string{"Root@example.org", "ops@example.org"}
	_, _ = h.j.Record(ctx, errorJournal.Event{Kind: errorJournal.KindPanic, Source: "/x", Message: "boom"})
	var to []string
	for _, m := range h.mail.sent {
		to = append(to, m.To)
	}
	assert.ElementsMatch(t, []string{"ops@example.org", "root@example.org"}, to)
}

func TestBlockedChatIsMarkedFailingNotDropped(t *testing.T) {
	h := newHarness(DefaultConfig())
	h.repo.settings.TelegramChats = append(h.repo.settings.TelegramChats, errorJournal.TelegramChat{ChatID: "200"})
	h.tg.errs["200"] = telegram.ErrForbidden

	_, _ = h.j.Record(ctx, errorJournal.Event{Kind: errorJournal.KindPanic, Source: "/x", Message: "boom"})
	assert.Equal(t, 1, h.tg.count("100"), "the other chat still gets the message")
	require.Len(t, h.repo.settings.TelegramChats, 2)
	assert.True(t, h.repo.settings.TelegramChats[1].Failing)

	delete(h.tg.errs, "200") // the person pressed Start again
	results, err := h.j.SendErrorJournalTest(ctx)
	require.NoError(t, err)
	assert.False(t, h.repo.settings.TelegramChats[1].Failing)
	var ok int
	for _, r := range results {
		if r.OK {
			ok++
		}
	}
	assert.Equal(t, 3, ok) // two chats and one address
}

func TestSaveSettingsValidatesAndKeepsFailingMarks(t *testing.T) {
	h := newHarness(DefaultConfig())
	now := h.now
	h.repo.settings.TelegramChats = []errorJournal.TelegramChat{{ChatID: "100", Failing: true, FailingSince: &now}}

	view, err := h.j.SaveErrorJournalSettings(ctx, SettingsInput{
		Emails: []string{"A@Example.org", "a@example.org", ""}, EmailToSuperAdmins: true,
		TelegramChats: []ChatInput{{ChatID: "100", Label: "ops"}, {ChatID: "-1001"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a@example.org"}, view.Emails)
	require.Len(t, view.TelegramChats, 2)
	assert.True(t, view.TelegramChats[0].Failing)
	assert.False(t, view.TelegramChats[1].Failing)

	for _, bad := range []SettingsInput{
		{Emails: []string{"not an address"}},
		{Emails: []string{"Name <a@example.org>"}},
		{TelegramChats: []ChatInput{{ChatID: "abc"}}},
	} {
		_, err = h.j.SaveErrorJournalSettings(ctx, bad)
		assert.Error(t, err)
	}
}

func TestNotFoundCountersAreFlushedByRouteWithoutPaths(t *testing.T) {
	h := newHarness(DefaultConfig())
	h.j.CountNotFound("/api/events/:id")
	h.j.CountNotFound("/api/events/:id")
	h.j.CountNotFound("") // an unmatched path
	h.j.FlushNotFound(ctx)
	assert.EqualValues(t, 2, h.repo.notFound["2026-10-02|/api/events/:id"])
	assert.EqualValues(t, 1, h.repo.notFound["2026-10-02|"])
	for key := range h.repo.notFound {
		assert.False(t, strings.Contains(key, "wp-login"))
	}
	h.j.FlushNotFound(ctx)
	assert.EqualValues(t, 2, h.repo.notFound["2026-10-02|/api/events/:id"], "flushed counters are not written twice")
}

func TestPurgeUsesTheRetentionCutoff(t *testing.T) {
	h := newHarness(DefaultConfig())
	require.NoError(t, h.j.PurgeErrorJournal(ctx))
	assert.Equal(t, h.now.Add(-30*24*time.Hour), h.repo.purgedAt)
}

func TestReportIsNonBlockingAndDropsWhenFull(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BufferSize = 2
	h := newHarness(cfg)
	for i := 0; i < 10; i++ {
		h.j.Report(fiveXX("x"))
	}
	assert.EqualValues(t, 8, h.j.dropped.Load())
}

func TestRunRecordsQueuedEventsAndDrainsOnStop(t *testing.T) {
	h := newHarness(DefaultConfig())
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { h.j.Run(runCtx); close(done) }()
	h.j.Report(fiveXX("queued"))
	h.j.CountNotFound("/r")
	cancel()
	<-done
	assert.Len(t, h.repo.groups, 1)
	assert.EqualValues(t, 1, h.repo.notFound["2026-10-02|/r"])
}

func TestStreamPublishesRecordedErrors(t *testing.T) {
	h := newHarness(DefaultConfig())
	ch, cancel := h.j.ErrorJournalStream()
	defer cancel()
	_, _ = h.j.Record(ctx, fiveXX("live"))
	select {
	case ev := <-ch:
		assert.True(t, ev.New)
		assert.NotNil(t, ev.Sample)
	default:
		t.Fatal("no event on the stream")
	}
}
