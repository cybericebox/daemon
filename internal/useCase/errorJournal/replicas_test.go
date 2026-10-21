package errorJournalUseCase

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
)

const testPoll = 5 * time.Millisecond

// secondReplica is another journal over the same repository (the shared database).
func secondReplica(h *harness) *Journal {
	cfg := DefaultConfig()
	cfg.StreamPollEvery = testPoll
	j := New(Dependencies{Repo: h.repo, Telegram: h.tg, Mailer: h.mail, Config: cfg})
	j.now = func() time.Time { return h.now }
	return j
}

func pollingHarness() *harness {
	cfg := DefaultConfig()
	cfg.StreamPollEvery = testPoll
	return newHarness(cfg)
}

func TestARecordedErrorReachesTheStreamOfTheOtherReplicaThroughThePoller(t *testing.T) {
	h := pollingHarness()
	other := secondReplica(h)
	here, endHere := h.j.ErrorJournalStream()
	defer endHere()
	there, endThere := other.ErrorJournalStream()
	defer endThere()
	time.Sleep(4 * testPoll) // both pollers have their cursor

	rec, err := h.j.Record(ctx, fiveXX("boom"))
	require.NoError(t, err)

	select {
	case e := <-there:
		assert.Equal(t, rec.Group.ID, e.Group.ID)
		assert.True(t, e.New)
		require.NotNil(t, e.Sample, "the sample is read from the database on the receiving replica")
		assert.Equal(t, "boom", e.Sample.Message)
	case <-time.After(2 * time.Second):
		t.Fatal("the other replica's stream never saw the error")
	}
	select {
	case e := <-here:
		assert.Equal(t, rec.Group.ID, e.Group.ID)
	case <-time.After(time.Second):
		t.Fatal("the own stream never saw the error")
	}
	// The recording replica shows it once: its own change is skipped by the poller.
	select {
	case <-here:
		t.Fatal("the own stream got the event twice")
	case <-time.After(10 * testPoll):
	}
}

func TestAStreamDoesNotReplayWhatHappenedBeforeItOpened(t *testing.T) {
	h := pollingHarness()
	other := secondReplica(h)
	_, err := h.j.Record(ctx, fiveXX("old"))
	require.NoError(t, err)
	there, end := other.ErrorJournalStream()
	defer end()
	select {
	case <-there:
		t.Fatal("history must not be replayed into a new stream")
	case <-time.After(10 * testPoll):
	}
}

func TestAGroupStatusChangeReachesTheStreamsOfEveryReplica(t *testing.T) {
	h := pollingHarness()
	other := secondReplica(h)
	rec, err := h.j.Record(ctx, fiveXX("boom"))
	require.NoError(t, err)
	there, end := other.ErrorJournalStream()
	defer end()
	time.Sleep(4 * testPoll)

	_, err = h.j.SetErrorGroupStatus(ctx, rec.Group.ID, errorJournal.StatusResolved)
	require.NoError(t, err)
	select {
	case e := <-there:
		assert.Equal(t, errorJournal.StatusResolved, e.Group.Status)
		assert.Nil(t, e.Sample)
	case <-time.After(2 * time.Second):
		t.Fatal("the status change never reached the other replica")
	}
}

func TestNothingIsPolledWithoutAStream(t *testing.T) {
	h := pollingHarness()
	_, err := h.j.Record(ctx, fiveXX("boom"))
	require.NoError(t, err)
	time.Sleep(10 * testPoll)
	assert.Zero(t, h.repo.reads(), "no stream, no reads of the change log")

	_, end := h.j.ErrorJournalStream()
	require.Eventually(t, func() bool { return h.repo.reads() > 0 }, time.Second, testPoll)
	end()
	time.Sleep(3 * testPoll) // the poller notices the end
	reads := h.repo.reads()
	time.Sleep(10 * testPoll)
	assert.Equal(t, reads, h.repo.reads(), "the poller stops with the last stream")
}

func TestTwoReplicasSendOneMessageForTheSameError(t *testing.T) {
	h := newHarness(DefaultConfig())
	other := secondReplica(h)
	_, err := h.j.Record(ctx, fiveXX("boom"))
	require.NoError(t, err)
	_, err = other.Record(ctx, fiveXX("boom"))
	require.NoError(t, err)
	assert.Equal(t, 1, h.tg.count("100"), "the message is claimed in the database, not per replica")
	assert.Len(t, h.mail.sent, 1)
}

func TestTwoReplicasSendOneMessagePerCooldownForANoisyKind(t *testing.T) {
	h := newHarness(DefaultConfig())
	other := secondReplica(h)
	forbidden := func(route string) errorJournal.Event {
		return errorJournal.Event{
			Kind:       errorJournal.KindHTTP403,
			Source:     route,
			Route:      route,
			Method:     "GET",
			HTTPStatus: 403,
			Message:    "forbidden",
			Notify:     errorJournal.NotifyAlways,
		}
	}
	_, err := h.j.Record(ctx, forbidden("/api/a"))
	require.NoError(t, err)
	_, err = other.Record(ctx, forbidden("/api/b"))
	require.NoError(t, err)
	assert.Equal(
		t,
		1,
		h.tg.count("100"),
		"one message per cooldown for the kind, whichever replica saw it",
	)

	h.now = h.now.Add(h.j.cfg.NotifyCooldown + time.Second)
	_, err = other.Record(ctx, forbidden("/api/c"))
	require.NoError(t, err)
	assert.Equal(t, 2, h.tg.count("100"))
}
