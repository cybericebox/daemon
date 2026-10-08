package labTraffic

import (
	"math"
	"testing"
	"time"
)

func TestClassifyLabOnlyDoesNotInventParticipantActivity(t *testing.T) {
	q := Question{Before: at(30), Since: at(0), Surfaces: []Surface{SurfaceVPN}}
	coverage := map[Surface][]Coverage{SurfaceVPN: {{From: at(0), To: at(30), Explicit: true}}}
	row := Aggregate{Surface: SurfaceVPN, LabInitiatedAttempts: 2, PacketsOut: 3, PacketsIn: 4, BytesOut: 100, BytesIn: 200}
	got := Classify(q, []Aggregate{row}, coverage)
	if got.Verdict != Untouched || got.Attempted || got.FirstSeenAt != nil || got.FirstRespondAt != nil || got.BytesIn != 0 {
		t.Fatalf("lab-only traffic invented participant activity: %+v", got)
	}
	// Even malformed lab-only response metadata is not evidence of a participant start.
	row.FirstRespondAt = ptr(at(2))
	got = Classify(q, []Aggregate{row}, coverage)
	if got.Verdict != Untouched || got.FirstRespondAt != nil {
		t.Fatalf("lab-only response invented a participant touch: %+v", got)
	}
}

func TestClassifyResponseBytesSaturateAcrossSurfaces(t *testing.T) {
	rows := []Aggregate{
		{Surface: SurfaceVPN, Attempts: 1, FirstSeenAt: at(1), FirstRespondAt: ptr(at(2)), BytesIn: math.MaxInt64},
		{Surface: SurfaceProxy, Attempts: 1, FirstSeenAt: at(1), FirstRespondAt: ptr(at(2)), BytesIn: math.MaxInt64},
	}
	got := Classify(Question{Before: at(30), Surfaces: []Surface{SurfaceVPN, SurfaceProxy}}, rows, nil)
	if got.Verdict != Touched || got.BytesIn != math.MaxInt64 {
		t.Fatalf("response-byte sum overflowed: %+v", got)
	}
}

func TestClassifyMissingResponseWithParticipantEvidenceIsUnknown(t *testing.T) {
	q := Question{Before: at(30), Since: at(0), Surfaces: []Surface{SurfaceVPN}}
	coverage := map[Surface][]Coverage{SurfaceVPN: {{From: at(0), To: at(30), Explicit: true}}}
	cases := []Aggregate{
		{Surface: SurfaceVPN, Attempts: 1, FirstSeenAt: at(1), PacketsOut: 1},
		{Surface: SurfaceVPN, Attempts: 1, FirstSeenAt: at(1), PacketsIn: 1},
		{Surface: SurfaceVPN, Attempts: 1, FirstSeenAt: at(1), BytesOut: 1},
		{Surface: SurfaceVPN, Attempts: 1, FirstSeenAt: at(1), BytesIn: 1},
		{Surface: SurfaceVPN, PacketsOut: 1},
		{Surface: SurfaceVPN, BytesIn: 1},
		{Surface: SurfaceVPN, Attempts: 1},
		{Surface: SurfaceVPN, Attempts: 1, FirstSeenAt: at(1), FirstRespondAt: ptr(time.Time{}), BytesIn: 1},
	}
	for i, row := range cases {
		got := Classify(q, []Aggregate{row}, coverage)
		if got.Verdict != Unknown || (row.FirstSeenAt.IsZero() && got.FirstSeenAt != nil) {
			t.Errorf("case %d: incomplete participant observation classified %+v", i, got)
		}
	}
	// A positive observed response still proves a touch despite an incomplete ledger elsewhere.
	got := Classify(q, append(cases, Aggregate{Surface: SurfaceVPN, Attempts: 1, FirstSeenAt: at(2), FirstRespondAt: ptr(at(3))}), nil)
	if got.Verdict != Touched {
		t.Fatalf("observed response lost: %+v", got)
	}
}

func TestCoveredExplicitSpansPreserveEveryGap(t *testing.T) {
	cases := []struct {
		name  string
		spans []Coverage
		want  bool
	}{
		{"touching", []Coverage{{From: at(0), To: at(10), Explicit: true}, {From: at(10), To: at(30), Explicit: true}}, true},
		{"one second gap", []Coverage{{From: at(0), To: at(10), Explicit: true}, {From: at(10).Add(time.Second), To: at(30), Explicit: true}}, false},
		{"one second missing start", []Coverage{{From: at(0).Add(time.Second), To: at(30), Explicit: true}}, false},
		{"one second missing end", []Coverage{{From: at(0), To: at(30).Add(-time.Second), Explicit: true}}, false},
		{"partial replica", []Coverage{{From: at(0), To: at(30), Explicit: true}, {From: at(10), To: at(11), Partial: true, Explicit: true}}, false},
		{"partial legacy replica", []Coverage{{From: at(0), To: at(30)}, {From: at(10), To: at(11), Partial: true}}, false},
		{"legacy cannot conceal an explicit gap", []Coverage{{From: at(0), To: at(10)}, {From: at(10).Add(time.Second), To: at(30), Explicit: true}}, false},
		{"partial outside question", []Coverage{{From: at(0), To: at(30), Explicit: true}, {From: at(30), To: at(40), Partial: true, Explicit: true}}, true},
		{"invalid healthy interval", []Coverage{{From: at(20), To: at(10), Explicit: true}}, false},
		{"missing interval start", []Coverage{{To: at(30), Explicit: true}}, false},
		{"nothing within legacy tolerance", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Covered(tc.spans, at(0), at(30)); got != tc.want {
				t.Fatalf("Covered=%v, want %v", got, tc.want)
			}
		})
	}
	if Covered([]Coverage{{From: at(0), To: at(30)}}, at(30), at(0)) {
		t.Fatal("reversed question cannot establish observation")
	}
	if Covered(nil, at(0), at(1)) {
		t.Fatal("no observations cannot establish even a short legacy window")
	}
}
