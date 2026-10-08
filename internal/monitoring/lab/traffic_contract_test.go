package lab

import (
	"context"
	"math"
	"testing"
	"time"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"github.com/gofrs/uuid"
)

func TestLabOnlyTrafficIsStoredWithoutParticipantActivity(t *testing.T) {
	f := newTrafficFixture()
	store := &fakeTrafficStore{members: map[uuid.UUID]bool{f.user: true}, challenges: map[string][]uuid.UUID{f.lab: {f.challenge}}}
	r := f.report("b", 0)
	r.Ledger[0].FirstSeenUnixMs, r.Ledger[0].LastSeenUnixMs, r.Ledger[0].FirstRespondedUnixMs = 0, 0, 0
	r.Ledger[0].LabInitiatedAttempts, r.Ledger[0].PacketsIn = 1, 300
	if err := NewTrafficIngest(store).ApplyTraffic(context.Background(), []*labpb.TrafficReport{r}); err != nil {
		t.Fatal(err)
	}
	if len(store.touches) != 1 {
		t.Fatalf("lab-only row discarded: %+v", store.touches)
	}
	row := store.touches[0]
	if row.Attempts != 0 || !row.FirstSeenAt.IsZero() || !row.LastSeenAt.IsZero() || row.FirstRespondAt != nil {
		t.Fatalf("invented participant activity: %+v", row)
	}
}

func TestTrafficReplayTracksEachDirectionAndEarliestTime(t *testing.T) {
	f := newTrafficFixture()
	store := &fakeTrafficStore{members: map[uuid.UUID]bool{f.user: true}, challenges: map[string][]uuid.UUID{f.lab: {f.challenge}}}
	ingest := NewTrafficIngest(store)
	r := f.report("b", 1)
	r.Ledger[0].PacketsIn, r.Ledger[0].PacketsOut = math.MaxInt64, 2
	if err := ingest.ApplyTraffic(context.Background(), []*labpb.TrafficReport{r}); err != nil {
		t.Fatal(err)
	}
	r.Ledger[0].PacketsIn, r.Ledger[0].PacketsOut = 2, math.MaxInt64
	if err := ingest.ApplyTraffic(context.Background(), []*labpb.TrafficReport{r}); err != nil {
		t.Fatal(err)
	}
	r.Ledger[0].FirstSeenUnixMs = 1_500
	if err := ingest.ApplyTraffic(context.Background(), []*labpb.TrafficReport{r}); err != nil {
		t.Fatal(err)
	}
	if len(store.touches) != 3 || !store.touches[2].FirstSeenAt.Equal(time.UnixMilli(1_500).UTC()) {
		t.Fatalf("changed direction/earliest time skipped: %+v", store.touches)
	}
}

func TestNegativeTrafficFactsAreNotStored(t *testing.T) {
	f := newTrafficFixture()
	for _, mutate := range []func(*labpb.TrafficTouch){
		func(r *labpb.TrafficTouch) { r.Attempts = -1 },
		func(r *labpb.TrafficTouch) { r.LabInitiatedAttempts = -1 },
		func(r *labpb.TrafficTouch) { r.PacketsIn = -1 },
		func(r *labpb.TrafficTouch) { r.PacketsOut = -1 },
		func(r *labpb.TrafficTouch) { r.BytesIn = -1 },
		func(r *labpb.TrafficTouch) { r.BytesOut = -1 },
		func(r *labpb.TrafficTouch) { r.FirstSeenUnixMs = -1 },
		func(r *labpb.TrafficTouch) { r.LastSeenUnixMs = -1 },
		func(r *labpb.TrafficTouch) { r.FirstRespondedUnixMs = -1 },
	} {
		store := &fakeTrafficStore{members: map[uuid.UUID]bool{f.user: true}, challenges: map[string][]uuid.UUID{f.lab: {f.challenge}}}
		r := f.report("b", 1)
		mutate(r.Ledger[0])
		if err := NewTrafficIngest(store).ApplyTraffic(context.Background(), []*labpb.TrafficReport{r}); err != nil {
			t.Fatal(err)
		}
		if len(store.touches) != 0 {
			t.Fatalf("negative input persisted: %+v", store.touches)
		}
	}
}

func TestInvalidLedgerCannotProvideCompleteCoverage(t *testing.T) {
	f := newTrafficFixture()
	store := &fakeTrafficStore{members: map[uuid.UUID]bool{f.user: true}, challenges: map[string][]uuid.UUID{f.lab: {f.challenge}}}
	r := f.report("b", 1)
	r.Ledger[0].BytesIn = -1
	if err := NewTrafficIngest(store).ApplyTraffic(context.Background(), []*labpb.TrafficReport{r}); err != nil {
		t.Fatal(err)
	}
	if len(store.coverage) != 1 || !store.coverage[0].Partial {
		t.Fatalf("invalid row became evidence of no activity: %+v", store.coverage)
	}
}

func TestExplicitCoverageDoesNotBecomeScalarEnvelope(t *testing.T) {
	f := newTrafficFixture()
	store := &fakeTrafficStore{}
	r := f.report("b", 0)
	r.Ledger = nil
	r.CoverageSpans = []*labpb.TrafficCoverageSpan{
		{FromUnixMs: 1_000, ToUnixMs: 3_000, Source: "old", Instance: "p", BootId: "old-b"},
		{FromUnixMs: 7_000, ToUnixMs: 9_000, Source: "new", Instance: "p", BootId: "new-b"},
	}
	if err := NewTrafficIngest(store).ApplyTraffic(context.Background(), []*labpb.TrafficReport{r}); err != nil {
		t.Fatal(err)
	}
	if len(store.coverage) != 2 || store.coverage[0].To.UnixMilli() != 3_000 || store.coverage[1].From.UnixMilli() != 7_000 {
		t.Fatalf("downtime filled by envelope: %+v", store.coverage)
	}
}

func TestTruncatedAndMalformedCoverageCannotProveUntouched(t *testing.T) {
	f := newTrafficFixture()
	for _, explicit := range []bool{false, true} {
		store := &fakeTrafficStore{}
		r := f.report("b", 0)
		r.Ledger = nil
		r.Truncated = true
		if explicit {
			r.CoverageSpans = []*labpb.TrafficCoverageSpan{{FromUnixMs: 1_000, ToUnixMs: 9_000}}
		}
		if err := NewTrafficIngest(store).ApplyTraffic(context.Background(), []*labpb.TrafficReport{r}); err != nil {
			t.Fatal(err)
		}
		if len(store.coverage) != 1 || !store.coverage[0].Partial {
			t.Fatalf("truncation became complete: %+v", store.coverage)
		}
	}
	store := &fakeTrafficStore{}
	r := f.report("b", 0)
	r.Ledger = nil
	r.CoverageSpans = []*labpb.TrafficCoverageSpan{{FromUnixMs: 10_000, ToUnixMs: 1_000}}
	if err := NewTrafficIngest(store).ApplyTraffic(context.Background(), []*labpb.TrafficReport{r}); err != nil {
		t.Fatal(err)
	}
	if len(store.coverage) == 0 || !store.coverage[0].Partial {
		t.Fatalf("malformed explicit span fell back to healthy scalar: %+v", store.coverage)
	}
}
