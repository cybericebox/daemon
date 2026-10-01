package eventAnalyticsModel_test

import (
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

var t0 = time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)

func sequentialIDs() func() uuid.UUID {
	n := byte(0)
	return func() uuid.UUID {
		n++
		return uuid.UUID{15: n}
	}
}

func sample(team uuid.UUID, client string, handshakeMin int, rx, tx int64) eventAnalyticsModel.VPNSample {
	h := t0.Add(time.Duration(handshakeMin) * time.Minute)
	return eventAnalyticsModel.VPNSample{TeamID: team, Client: client, ObservedAt: h.Add(10 * time.Second), Handshake: h, Rx: rx, Tx: tx}
}

func TestMergeVPNSessions_SplitsOnGapsAndCountsBytes(t *testing.T) {
	team, user := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	client := labBindingModel.ParticipantClientName(user)
	samples := []eventAnalyticsModel.VPNSample{
		sample(team, client, 0, 100, 10),
		sample(team, client, 2, 400, 40),
		sample(team, client, 4, 900, 90),
		// 20 minutes idle: a new session.
		sample(team, client, 24, 1000, 100),
		sample(team, client, 26, 1500, 150),
		// Never connected: ignored.
		{TeamID: team, Client: client, ObservedAt: t0},
	}
	changed, absorbed := eventAnalyticsModel.MergeVPNSessions(nil, samples, sequentialIDs())
	if len(absorbed) != 0 || len(changed) != 2 {
		t.Fatalf("changed=%+v absorbed=%v", changed, absorbed)
	}
	first, second := changed[0], changed[1]
	if !first.StartedAt.Equal(t0) || !first.EndedAt.Equal(t0.Add(4*time.Minute)) || first.RxBytes() != 800 || first.TxBytes() != 80 {
		t.Fatalf("first session: %+v", first)
	}
	if !second.StartedAt.Equal(t0.Add(24*time.Minute)) || second.RxBytes() != 500 {
		t.Fatalf("second session: %+v", second)
	}
	if first.UserID == nil || *first.UserID != user || first.TeamID != team {
		t.Fatalf("the participant comes from the client name: %+v", first)
	}
}

func TestMergeVPNSessions_ExtendsKnownAndIsIdempotent(t *testing.T) {
	team := uuid.Must(uuid.NewV7())
	client := labBindingModel.ParticipantClientName(uuid.Must(uuid.NewV7()))
	batch := []eventAnalyticsModel.VPNSample{sample(team, client, 0, 0, 0), sample(team, client, 2, 50, 5)}
	first, _ := eventAnalyticsModel.MergeVPNSessions(nil, batch, sequentialIDs())
	if len(first) != 1 {
		t.Fatalf("first pass: %+v", first)
	}

	// The next pass extends the stored session...
	next, absorbed := eventAnalyticsModel.MergeVPNSessions(first, []eventAnalyticsModel.VPNSample{sample(team, client, 4, 80, 8)}, sequentialIDs())
	if len(next) != 1 || len(absorbed) != 0 || next[0].ID != first[0].ID || !next[0].EndedAt.Equal(t0.Add(4*time.Minute)) || next[0].RxBytes() != 80 {
		t.Fatalf("extension: %+v absorbed=%v", next, absorbed)
	}
	// ...and re-reading the same observations changes nothing.
	again, absorbed := eventAnalyticsModel.MergeVPNSessions(next, batch, sequentialIDs())
	if len(again) != 0 || len(absorbed) != 0 {
		t.Fatalf("re-read must be a no-op: changed=%+v absorbed=%v", again, absorbed)
	}
}

// A late observation that bridges two stored sessions joins them into the
// earlier one; the later one is absorbed.
func TestMergeVPNSessions_BridgingJoinsSessions(t *testing.T) {
	team := uuid.Must(uuid.NewV7())
	client := labBindingModel.ParticipantClientName(uuid.Must(uuid.NewV7()))
	ids := sequentialIDs()
	stored, _ := eventAnalyticsModel.MergeVPNSessions(nil, []eventAnalyticsModel.VPNSample{
		sample(team, client, 0, 10, 1), sample(team, client, 6, 90, 9),
	}, ids)
	if len(stored) != 2 {
		t.Fatalf("setup: %+v", stored)
	}
	changed, absorbed := eventAnalyticsModel.MergeVPNSessions(stored, []eventAnalyticsModel.VPNSample{sample(team, client, 3, 40, 4)}, ids)
	if len(changed) != 1 || len(absorbed) != 1 || absorbed[0] != stored[1].ID || changed[0].ID != stored[0].ID {
		t.Fatalf("changed=%+v absorbed=%v", changed, absorbed)
	}
	if !changed[0].EndedAt.Equal(t0.Add(6*time.Minute)) || changed[0].RxBytes() != 80 {
		t.Fatalf("joined session: %+v", changed[0])
	}
}

func TestMergeVPNSessions_KeepsClientsApart(t *testing.T) {
	team := uuid.Must(uuid.NewV7())
	a, b := labBindingModel.ParticipantClientName(uuid.Must(uuid.NewV7())), "moderator"
	changed, _ := eventAnalyticsModel.MergeVPNSessions(nil, []eventAnalyticsModel.VPNSample{sample(team, a, 0, 1, 1), sample(team, b, 0, 1, 1)}, sequentialIDs())
	if len(changed) != 2 {
		t.Fatalf("one session per client: %+v", changed)
	}
	for _, s := range changed {
		if s.Client == b && s.UserID != nil {
			t.Fatalf("a client that is not a participant has no user: %+v", s)
		}
	}
}

func TestClientUser(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	if got, ok := eventAnalyticsModel.ClientUser(labBindingModel.ParticipantClientName(id)); !ok || got != id {
		t.Fatalf("participant client: %s %v", got, ok)
	}
	for _, name := range []string{"", "p-", "p-nope", id.String(), "m-" + id.String()} {
		if _, ok := eventAnalyticsModel.ClientUser(name); ok {
			t.Fatalf("%q is not a participant client", name)
		}
	}
}

func TestVPNOnline_UsesTheHandshakeWindowOnly(t *testing.T) {
	now := t0.Add(time.Hour)
	cases := map[string]struct {
		last time.Time
		want bool
	}{
		"never connected": {time.Time{}, false},
		"unix epoch":      {time.Unix(0, 0), false},
		"just now":        {now, true},
		"one rekey ago":   {now.Add(-2 * time.Minute), true},
		"at the window":   {now.Add(-eventAnalyticsModel.VPNOnlineWindow), true},
		"past the window": {now.Add(-eventAnalyticsModel.VPNOnlineWindow - time.Second), false},
		"hours ago":       {now.Add(-3 * time.Hour), false},
	}
	for name, c := range cases {
		if got := eventAnalyticsModel.VPNOnline(c.last, now); got != c.want {
			t.Errorf("%s: online=%v want %v", name, got, c.want)
		}
	}
}

// Two handshakes exactly one session gap apart are one session; one second
// more is a new session.
func TestMergeVPNSessions_GapEqualsOnlineWindow(t *testing.T) {
	team := uuid.Must(uuid.NewV7())
	client := labBindingModel.ParticipantClientName(uuid.Must(uuid.NewV7()))
	at := func(d time.Duration) eventAnalyticsModel.VPNSample {
		return eventAnalyticsModel.VPNSample{TeamID: team, Client: client, Handshake: t0.Add(d), ObservedAt: t0.Add(d)}
	}
	joined, _ := eventAnalyticsModel.MergeVPNSessions(nil, []eventAnalyticsModel.VPNSample{at(0), at(eventAnalyticsModel.VPNSessionGap)}, sequentialIDs())
	if len(joined) != 1 {
		t.Fatalf("a gap of exactly the window stays one session: %+v", joined)
	}
	split, _ := eventAnalyticsModel.MergeVPNSessions(nil, []eventAnalyticsModel.VPNSample{at(0), at(eventAnalyticsModel.VPNSessionGap + time.Second)}, sequentialIDs())
	if len(split) != 2 {
		t.Fatalf("a longer gap is a new session: %+v", split)
	}
}
