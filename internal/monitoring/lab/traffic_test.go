package lab

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	labTraffic "github.com/cybericebox/daemon/internal/model/labTraffic"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

type fakeTrafficStore struct {
	members   map[uuid.UUID]bool
	memberHit int
	touches   []labTraffic.Touch
	coverage  []labTraffic.Coverage
}

func (s *fakeTrafficStore) IsUserInTeam(_ context.Context, _, _, user uuid.UUID) (bool, error) {
	s.memberHit++
	return s.members[user], nil
}
func (s *fakeTrafficStore) ApplyTouch(_ context.Context, t labTraffic.Touch) error {
	s.touches = append(s.touches, t)
	return nil
}
func (s *fakeTrafficStore) RecordCoverage(_ context.Context, _, _ uuid.UUID, _ labTraffic.Surface, _, _ string, span labTraffic.Coverage) error {
	s.coverage = append(s.coverage, span)
	return nil
}

type trafficFixture struct {
	event, team, challenge, user uuid.UUID
	group, lab                   string
}

func newTrafficFixture() trafficFixture {
	f := trafficFixture{event: uuid.Must(uuid.NewV7()), team: uuid.Must(uuid.NewV7()), challenge: uuid.Must(uuid.NewV7()), user: uuid.Must(uuid.NewV7())}
	f.group, _ = labBindingModel.GroupName(f.event, f.team)
	f.lab = labBindingModel.LabName(f.challenge, 2)
	return f
}

func (f trafficFixture) report(boot string, attempts int64) *labpb.TrafficReport {
	return &labpb.TrafficReport{
		LabGroupName: f.group, Source: "vpn", Kind: "vpn", BootId: boot, CoveredFromUnixMs: 1_000, CoveredToUnixMs: 9_000,
		Ledger: []*labpb.TrafficTouch{{
			Subject: labBindingModel.ParticipantClientName(f.user), LabName: f.lab, DstIp: "10.9.1.5", Proto: "tcp", DstPort: 22,
			Attempts: attempts, FirstSeenUnixMs: 2_000, LastSeenUnixMs: 3_000, FirstRespondedUnixMs: 2_500, BytesIn: 10,
		}},
	}
}

func TestTrafficIngestResolvesIdentitiesFromNames(t *testing.T) {
	f := newTrafficFixture()
	store := &fakeTrafficStore{members: map[uuid.UUID]bool{f.user: true}}
	if err := NewTrafficIngest(store).ApplyTraffic(context.Background(), []*labpb.TrafficReport{f.report("b1", 3)}); err != nil {
		t.Fatal(err)
	}
	if len(store.touches) != 1 {
		t.Fatalf("touches = %d", len(store.touches))
	}
	got := store.touches[0]
	if got.EventID != f.event || got.TeamID != f.team || got.UserID != f.user || got.EventChallengeID != f.challenge || got.Surface != labTraffic.SurfaceVPN {
		t.Fatalf("identity = %+v", got)
	}
	if got.Attempts != 3 || got.FirstRespondAt == nil {
		t.Fatalf("payload = %+v", got)
	}
	if len(store.coverage) != 1 || !store.coverage[0].From.Equal(time.UnixMilli(1_000).UTC()) || !store.coverage[0].To.Equal(time.UnixMilli(9_000).UTC()) {
		t.Fatalf("coverage = %+v", store.coverage)
	}
}

func TestTrafficIngestSkipsUnchangedStateAndCachesMembership(t *testing.T) {
	f := newTrafficFixture()
	store := &fakeTrafficStore{members: map[uuid.UUID]bool{f.user: true}}
	ingest := NewTrafficIngest(store)
	for i := 0; i < 3; i++ {
		if err := ingest.ApplyTraffic(context.Background(), []*labpb.TrafficReport{f.report("b1", 3)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.touches) != 1 || store.memberHit != 1 {
		t.Fatalf("touches=%d memberChecks=%d, want a single apply and one lookup", len(store.touches), store.memberHit)
	}
	if len(store.coverage) != 3 {
		t.Fatalf("coverage must advance on every report (heartbeat), got %d", len(store.coverage))
	}
	if err := ingest.ApplyTraffic(context.Background(), []*labpb.TrafficReport{f.report("b1", 4)}); err != nil {
		t.Fatal(err)
	}
	if len(store.touches) != 2 || store.touches[1].Attempts != 4 {
		t.Fatalf("a changed state must be applied: %+v", store.touches)
	}
}

func TestTrafficIngestDropsWhatIsNotAnEventParticipant(t *testing.T) {
	f := newTrafficFixture()
	store := &fakeTrafficStore{members: map[uuid.UUID]bool{}}
	tester := f.report("b1", 1)
	tester.Ledger[0].Subject = "tester"
	stray := f.report("b1", 1)
	stray.LabGroupName = "labgroup-test"
	unknownKind := f.report("b1", 1)
	unknownKind.Kind = "mystery"
	for _, r := range []*labpb.TrafficReport{f.report("b1", 1), tester, stray, unknownKind} {
		if err := NewTrafficIngest(store).ApplyTraffic(context.Background(), []*labpb.TrafficReport{r}); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.touches) != 0 {
		t.Fatalf("stored %d rows that cannot be attributed", len(store.touches))
	}
}

func TestProxyReportsAreWebTouchesOfALabGroupClient(t *testing.T) {
	f := newTrafficFixture()
	store := &fakeTrafficStore{members: map[uuid.UUID]bool{f.user: true}}
	r := f.report("b1", 5)
	r.Kind, r.Source = "proxy", "proxy-abc"
	r.Ledger[0].Subject, r.Ledger[0].Device, r.Ledger[0].DstIp, r.Ledger[0].DstPort = labBindingModel.ParticipantClientName(f.user), "web", "", 0
	if err := NewTrafficIngest(store).ApplyTraffic(context.Background(), []*labpb.TrafficReport{r}); err != nil {
		t.Fatal(err)
	}
	if len(store.touches) != 1 || store.touches[0].Surface != labTraffic.SurfaceProxy {
		t.Fatalf("touches = %+v", store.touches)
	}
}

// The proxy reports every group and client it serves; test deploys (t-<uuid>)
// and anything that is not an event team group are dropped here, not counted.
func TestProxyReportsOfATestDeployGroupAreNotCounted(t *testing.T) {
	f := newTrafficFixture()
	store := &fakeTrafficStore{members: map[uuid.UUID]bool{f.user: true}}
	r := f.report("b1", 5)
	r.Kind, r.Source = "proxy", "proxy-abc"
	r.LabGroupName = "t-" + uuid.Must(uuid.NewV7()).String()
	r.Ledger[0].Subject = labBindingModel.ParticipantClientName(f.user)
	bare := f.report("b1", 5)
	bare.Kind, bare.Source = "proxy", "proxy-abc"
	bare.Ledger[0].Subject = f.user.String() // a bare user id is not a client name
	if err := NewTrafficIngest(store).ApplyTraffic(context.Background(), []*labpb.TrafficReport{r, bare}); err != nil {
		t.Fatal(err)
	}
	if len(store.touches) != 0 {
		t.Fatalf("stored %d rows of a test group", len(store.touches))
	}
}

// Traffic is not an admin-visible observation: the runner hands it to the sink
// and never lets it into lab_monitoring_current or event_lab_observations.
func TestConsumeRoutesTrafficOnlyToTheSink(t *testing.T) {
	f := newTrafficFixture()
	store := &recordingStore{}
	sink := &recordingSink{}
	now := time.Now().UnixMilli()
	stream := &scriptedStream{updates: []*labpb.MonitoringUpdate{
		{AgentId: "a", Sequence: 1, ObservedAtUnixMs: now, SchemaVersion: 2, Snapshot: true,
			Groups: []*labpb.LabGroup{{Name: f.group}}, Traffic: []*labpb.TrafficReport{f.report("b1", 3)}},
		{AgentId: "a", Sequence: 2, ObservedAtUnixMs: now, SchemaVersion: 2, Traffic: []*labpb.TrafficReport{f.report("b1", 4)}},
	}}
	runner := NewRunner(nil, func(context.Context, *labpb.MonitoringRequest) (Stream, error) { return stream, nil }, store).WithTraffic(sink)
	if err := runner.consume(context.Background()); err != io.EOF {
		t.Fatalf("consume error = %v, want EOF", err)
	}
	if sink.calls != 2 {
		t.Fatalf("sink calls = %d, want both updates", sink.calls)
	}
	for _, observation := range store.appended {
		if strings.Contains(strings.ToLower(string(observation.Payload)), "traffic") || strings.Contains(string(observation.Payload), `"p-`) {
			t.Fatalf("an observation carries traffic: %s", observation.Payload)
		}
	}
	for _, applied := range store.applied {
		if strings.Contains(strings.ToLower(applied.payload), "traffic") {
			t.Fatalf("the current state carries traffic: %s", applied.payload)
		}
	}
}

type recordingSink struct{ calls int }

func (s *recordingSink) ApplyTraffic(context.Context, []*labpb.TrafficReport) error {
	s.calls++
	return nil
}
