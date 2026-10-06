package labTraffic

import (
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

var t0 = time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

func at(min int) time.Time       { return t0.Add(time.Duration(min) * time.Minute) }
func ptr(t time.Time) *time.Time { return &t }

func TestUserFromSubject(t *testing.T) {
	id := uuid.Must(uuid.NewV4())
	cases := []struct {
		name    string
		surface Surface
		subject string
		ok      bool
	}{
		{"vpn participant", SurfaceVPN, labBindingModel.ParticipantClientName(id), true},
		{"vpn test client", SurfaceVPN, "tester", false},
		{"vpn bad uuid", SurfaceVPN, "p-nope", false},
		{"vpn bare uuid is not a client name", SurfaceVPN, id.String(), false},
		{"web client", SurfaceProxy, labBindingModel.ParticipantClientName(id), true},
		{"web bare user id is not a client name", SurfaceProxy, id.String(), false},
		{"web legacy token", SurfaceProxy, "", false},
	}
	for _, c := range cases {
		got, ok := UserFromSubject(c.surface, c.subject)
		if ok != c.ok || (ok && got != id) {
			t.Errorf("%s: got %v %v", c.name, got, ok)
		}
	}
}

func TestCovered(t *testing.T) {
	full := []Coverage{{From: at(0), To: at(10)}, {From: at(10), To: at(30)}}
	cases := []struct {
		name  string
		spans []Coverage
		from  time.Time
		to    time.Time
		want  bool
	}{
		{"one span", []Coverage{{From: at(0), To: at(30)}}, at(5), at(20), true},
		{"joined spans", full, at(2), at(28), true},
		{"gap inside", []Coverage{{From: at(0), To: at(10)}, {From: at(20), To: at(30)}}, at(2), at(28), false},
		{"starts late", []Coverage{{From: at(8), To: at(30)}}, at(2), at(28), false},
		{"ends early", []Coverage{{From: at(0), To: at(20)}}, at(2), at(28), false},
		{"small silence tolerated", []Coverage{{From: at(0), To: at(10)}, {From: t0.Add(10*time.Minute + 2*time.Minute), To: at(30)}}, at(2), at(28), true},
		{"partial does not count", []Coverage{{From: at(0), To: at(30), Partial: true}}, at(2), at(28), false},
		{"nothing", nil, at(2), at(28), false},
	}
	for _, c := range cases {
		if got := Covered(c.spans, c.from, c.to); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestClassify(t *testing.T) {
	q := Question{Before: at(60), Since: at(0), Surfaces: []Surface{SurfaceVPN}}
	covered := map[Surface][]Coverage{SurfaceVPN: {{From: at(0), To: at(120)}}}

	cases := []struct {
		name      string
		rows      []Aggregate
		coverage  map[Surface][]Coverage
		verdict   Verdict
		attempted bool
	}{
		{"answered before", []Aggregate{{Surface: SurfaceVPN, Attempts: 3, FirstSeenAt: at(10), FirstRespondAt: ptr(at(11)), BytesIn: 500}}, covered, Touched, true},
		{"answered only after", []Aggregate{{Surface: SurfaceVPN, Attempts: 3, FirstSeenAt: at(10), FirstRespondAt: ptr(at(70))}}, covered, Untouched, true},
		{"knocked, never answered", []Aggregate{{Surface: SurfaceVPN, Attempts: 4, FirstSeenAt: at(10)}}, covered, Untouched, true},
		{"nothing seen and fully covered", nil, covered, Untouched, false},
		{"nothing seen and a gap", nil, map[Surface][]Coverage{SurfaceVPN: {{From: at(0), To: at(20)}, {From: at(40), To: at(120)}}}, Unknown, false},
		{"nothing seen and no coverage at all", nil, nil, Unknown, false},
		{"first attempt after the deadline is ignored", []Aggregate{{Surface: SurfaceVPN, Attempts: 1, FirstSeenAt: at(61), FirstRespondAt: ptr(at(61))}}, covered, Untouched, false},
		{"touched needs no coverage", []Aggregate{{Surface: SurfaceVPN, Attempts: 1, FirstSeenAt: at(5), FirstRespondAt: ptr(at(5))}}, nil, Touched, true},
	}
	for _, c := range cases {
		got := Classify(q, c.rows, c.coverage)
		if got.Verdict != c.verdict || got.Attempted != c.attempted {
			t.Errorf("%s: verdict=%s attempted=%v, want %s %v", c.name, got.Verdict, got.Attempted, c.verdict, c.attempted)
		}
	}
}

func TestClassifyRequiresEverySurfaceCovered(t *testing.T) {
	q := Question{Before: at(60), Since: at(0), Surfaces: []Surface{SurfaceVPN, SurfaceProxy}}
	onlyVPN := map[Surface][]Coverage{SurfaceVPN: {{From: at(0), To: at(120)}}}
	if got := Classify(q, nil, onlyVPN); got.Verdict != Unknown {
		t.Fatalf("web unobserved must be unknown, got %s", got.Verdict)
	}
	both := map[Surface][]Coverage{SurfaceVPN: onlyVPN[SurfaceVPN], SurfaceProxy: {{From: at(0), To: at(120)}}}
	if got := Classify(q, nil, both); got.Verdict != Untouched {
		t.Fatalf("both observed must be untouched, got %s", got.Verdict)
	}
}

func TestClassifyWithoutSinceOrSurfacesIsUnknown(t *testing.T) {
	covered := map[Surface][]Coverage{SurfaceVPN: {{From: at(0), To: at(120)}}}
	if got := Classify(Question{Before: at(60), Surfaces: []Surface{SurfaceVPN}}, nil, covered); got.Verdict != Unknown {
		t.Fatalf("no Since must be unknown, got %s", got.Verdict)
	}
	if got := Classify(Question{Before: at(60), Since: at(0)}, nil, covered); got.Verdict != Unknown {
		t.Fatalf("no surfaces must be unknown, got %s", got.Verdict)
	}
}

// A proxy touch of a task that is offered over the VPN only is not a touch of
// the task: the VPN stays untouched.
func TestClassifyIgnoresSurfacesTheTaskDoesNotOffer(t *testing.T) {
	q := Question{Before: at(60), Since: at(0), Surfaces: []Surface{SurfaceVPN}}
	rows := []Aggregate{{Surface: SurfaceProxy, Attempts: 4, FirstSeenAt: at(5), FirstRespondAt: ptr(at(5))}}
	covered := map[Surface][]Coverage{SurfaceVPN: {{From: at(0), To: at(120)}}}
	if got := Classify(q, rows, covered); got.Verdict != Untouched || got.Attempted {
		t.Fatalf("proxy row of a VPN-only task must not count, got %+v", got)
	}
	q.Surfaces = []Surface{SurfaceVPN, SurfaceProxy}
	covered[SurfaceProxy] = []Coverage{{From: at(0), To: at(120)}}
	if got := Classify(q, rows, covered); got.Verdict != Touched {
		t.Fatalf("proxy row of a VPN and proxy task counts, got %s", got.Verdict)
	}
}
