package eventModel

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

func TestScoringProfileExposesLifecycleEligibility(t *testing.T) {
	if _, ok := reflect.TypeOf(ScoringProfile{}).MethodByName("ValidateFor"); !ok {
		t.Fatal("scoring profile must validate whether an event lifecycle permits it")
	}
}

func TestScoringProfileEligibilityAndForcedPrecedence(t *testing.T) {
	publishAt := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	startAt := publishAt.Add(time.Hour)
	rolling, err := NewLifecycle(JoinPolicyRolling, publishAt, startAt, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	popularity := ScoringProfile{Mode: ScoringPopularityCurve, MinPoints: 100, MaxPoints: 500, FloorAtPercent: 50}
	if err := popularity.ValidateFor(rolling); !errors.Is(err, ErrEventScoringProfileInvalid.Err()) {
		t.Fatalf("rolling event must reject popularity scoring, got %v", err)
	}
	local := ScoringProfile{Mode: ScoringTimeDecay, MinPoints: 50, MaxPoints: 200, FloorAtPercent: 100}
	if got := ResolveScoringProfile(popularity, &local, false); got != local {
		t.Fatalf("local profile must override event default: got %+v", got)
	}
	if got := ResolveScoringProfile(popularity, &local, true); got != popularity {
		t.Fatalf("forced event profile must win: got %+v", got)
	}
}

func TestPopularityCurveReachesConfiguredBounds(t *testing.T) {
	profile := ScoringProfile{Mode: ScoringPopularityCurve, MinPoints: 100, MaxPoints: 500, FloorAtPercent: 40}
	if got := profile.AwardPopularity(0, 100); got != 500 {
		t.Fatalf("zero solves = %d, want 500", got)
	}
	if got := profile.AwardPopularity(40, 100); got != 100 {
		t.Fatalf("40 percent solves = %d, want 100", got)
	}
}

func TestFirstSolvesLadderFixesAwardByRank(t *testing.T) {
	profile := ScoringProfile{Mode: ScoringFirstSolvesLadder, MinPoints: 100, MaxPoints: 500, FloorAtPercent: 50}
	if got := profile.AwardFirstSolve(1, 100); got != 500 {
		t.Fatalf("first solve = %d, want 500", got)
	}
	if got := profile.AwardFirstSolve(50, 100); got != 100 {
		t.Fatalf("floor rank = %d, want 100", got)
	}
}

func TestFirstSolvesLadderReachesMinimumAtConfiguredPercentile(t *testing.T) {
	profile := ScoringProfile{Mode: ScoringFirstSolvesLadder, MinPoints: 100, MaxPoints: 1000, FloorAtPercent: 50}
	if got := profile.AwardFirstSolve(50, 100); got != 100 {
		t.Fatalf("rank at 50 percent = %d, want exact floor 100", got)
	}
}

func TestTimeDecayUsesEventDuration(t *testing.T) {
	profile := ScoringProfile{Mode: ScoringTimeDecay, MinPoints: 100, MaxPoints: 500}
	duration := 2 * time.Hour
	if got := profile.AwardTime(time.Duration(0), duration); got != 500 {
		t.Fatalf("at start = %d, want 500", got)
	}
	if got := profile.AwardTime(duration, duration); got != 100 {
		t.Fatalf("at finish = %d, want 100", got)
	}
}

func TestUpdateScoringProfileStaticPoints(t *testing.T) {
	e := Event{}
	zero, hundred := int32(0), int32(100)
	if err := e.UpdateScoringProfile(ScoringProfile{Mode: ScoringStatic}, false, &zero, uuid.Nil, fixedNow); err == nil {
		t.Fatal("static points 0 must be rejected")
	}
	if e.StaticPoints != nil {
		t.Fatal("a rejected update must not change the event")
	}
	if err := e.UpdateScoringProfile(ScoringProfile{Mode: ScoringStatic}, true, &hundred, uuid.Nil, fixedNow); err != nil {
		t.Fatalf("static points 100: %v", err)
	}
	if e.StaticPoints == nil || *e.StaticPoints != 100 || !e.ForceEventScoring || !e.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("unexpected event: %+v", e)
	}
	if err := e.UpdateScoringProfile(ScoringProfile{Mode: ScoringStatic}, false, nil, uuid.Nil, fixedNow); err == nil {
		t.Fatal("static scoring without points must be rejected")
	}
	dynamic := ScoringProfile{Mode: ScoringPopularityCurve, MinPoints: 10, MaxPoints: 100, FloorAtPercent: 50}
	if err := e.UpdateScoringProfile(dynamic, false, nil, uuid.Nil, fixedNow); err != nil || e.StaticPoints != nil {
		t.Fatalf("dynamic scoring does not need static points: err=%v points=%v", err, e.StaticPoints)
	}
}

func TestDynamicProfileMayDecayToZero(t *testing.T) {
	joinClosed := Lifecycle{JoinPolicy: JoinPolicyLockedAtStart}
	if err := (ScoringProfile{Mode: ScoringPopularityCurve, MinPoints: 0, MaxPoints: 100, FloorAtPercent: 50}).ValidateFor(joinClosed); err != nil {
		t.Fatalf("min 0 must be allowed: %v", err)
	}
	for _, profile := range []ScoringProfile{
		{Mode: ScoringPopularityCurve, MinPoints: -1, MaxPoints: 100, FloorAtPercent: 50},
		{Mode: ScoringPopularityCurve, MinPoints: 0, MaxPoints: 0, FloorAtPercent: 50},
	} {
		if err := profile.ValidateFor(joinClosed); err == nil {
			t.Fatalf("profile %+v must be rejected", profile)
		}
	}
}

func TestDynamicScoringProfilePointsRange(t *testing.T) {
	finish := fixedNow.Add(time.Hour)
	lifecycle := Lifecycle{JoinPolicy: JoinPolicyLockedAtStart, FinishAt: &finish}
	for _, mode := range []ScoringMode{ScoringPopularityCurve, ScoringFirstSolvesLadder, ScoringTimeDecay} {
		for _, bounds := range [][2]int32{{0, 999}, {998, 999}, {0, 1000}, {999, 1000}, {1000, 1001}} {
			profile := ScoringProfile{Mode: mode, MinPoints: bounds[0], MaxPoints: bounds[1], FloorAtPercent: 50}
			err := profile.ValidateFor(lifecycle)
			if bounds[1] > 999 {
				if !errors.Is(err, ErrEventScoringProfileInvalid.Err()) {
					t.Errorf("mode %d bounds %v must be rejected, got %v", mode, bounds, err)
				}
			} else if err != nil {
				t.Errorf("mode %d bounds %v must be accepted: %v", mode, bounds, err)
			}
		}
	}
}

func TestUpdateScoringProfilePointsRange(t *testing.T) {
	for _, mode := range []ScoringMode{ScoringStatic, ScoringPopularityCurve} {
		for _, points := range []int32{999, 1000} {
			previous := int32(100)
			e := Event{StaticPoints: &previous, UpdatedAt: fixedNow.Add(-time.Hour)}
			profile := ScoringProfile{Mode: mode, MinPoints: 0, MaxPoints: 999, FloorAtPercent: 50}
			err := e.UpdateScoringProfile(profile, true, &points, uuid.Nil, fixedNow)
			if points > 999 {
				if !errors.Is(err, ErrEventStaticPointsInvalid.Err()) || *e.StaticPoints != 100 || !e.UpdatedAt.Equal(fixedNow.Add(-time.Hour)) || e.ForceEventScoring {
					t.Errorf("mode %d static points %d must be rejected without mutation: event=%+v err=%v", mode, points, e, err)
				}
			} else if err != nil || *e.StaticPoints != 999 {
				t.Errorf("mode %d static points 999 must be accepted: event=%+v err=%v", mode, e, err)
			}
		}
	}
}

func TestUpdateScoringProfileRejectsOversizeDynamicPointsWithoutMutation(t *testing.T) {
	previous := int32(100)
	e := Event{StaticPoints: &previous, UpdatedAt: fixedNow.Add(-time.Hour)}
	profile := ScoringProfile{Mode: ScoringPopularityCurve, MinPoints: 0, MaxPoints: 1000, FloorAtPercent: 50}
	err := e.UpdateScoringProfile(profile, true, nil, uuid.Nil, fixedNow)
	if !errors.Is(err, ErrEventScoringProfileInvalid.Err()) || *e.StaticPoints != 100 || !e.UpdatedAt.Equal(fixedNow.Add(-time.Hour)) || e.ForceEventScoring {
		t.Fatalf("oversize dynamic score must be rejected without mutation: event=%+v err=%v", e, err)
	}
}

func TestDynamicScoringPreservesOnlyUnchangedDormantLegacyStaticPoints(t *testing.T) {
	for _, points := range []int32{999, 1500, 1501} {
		previous := int32(1500)
		e := Event{StaticPoints: &previous}
		profile := ScoringProfile{Mode: ScoringPopularityCurve, MinPoints: 0, MaxPoints: 999, FloorAtPercent: 50}
		err := e.UpdateScoringProfile(profile, false, &points, uuid.Nil, fixedNow)
		if points == 1501 {
			if !errors.Is(err, ErrEventStaticPointsInvalid.Err()) || *e.StaticPoints != 1500 {
				t.Fatalf("a changed oversize dormant value must be rejected: event=%+v err=%v", e, err)
			}
		} else if err != nil || *e.StaticPoints != points {
			t.Fatalf("a valid replacement or unchanged dormant value must be accepted: points=%d event=%+v err=%v", points, e, err)
		}
	}
}

func TestLegacyScoringValuesAreNotClamped(t *testing.T) {
	points := int32(1500)
	e := Event{ScoringProfile: ScoringProfile{Mode: ScoringStatic}, StaticPoints: &points}
	if got := e.EffectiveStaticPoints(100, nil); got != 1500 {
		t.Fatalf("stored static score = %d, want 1500", got)
	}
	profile := ScoringProfile{Mode: ScoringTimeDecay, MinPoints: 100, MaxPoints: 1500}
	if got := AwardPoints(profile, 100, 1, 1, 0, 0, time.Hour); got != 1500 {
		t.Fatalf("stored dynamic award = %d, want 1500", got)
	}
}

func TestEffectiveStaticPoints(t *testing.T) {
	static := int32(100)
	local := ScoringProfile{Mode: ScoringTimeDecay}
	staticEvent := Event{ScoringProfile: ScoringProfile{Mode: ScoringStatic}, StaticPoints: &static}
	for _, tc := range []struct {
		name  string
		event Event
		local *ScoringProfile
		want  int32
	}{
		{"task follows a static event", staticEvent, nil, 100},
		{"task with its own profile keeps its points", staticEvent, &local, 250},
		{"forced event scoring overrides the task profile", Event{ScoringProfile: staticEvent.ScoringProfile, StaticPoints: &static, ForceEventScoring: true}, &local, 100},
		{"dynamic event keeps the task points", Event{ScoringProfile: ScoringProfile{Mode: ScoringTimeDecay}, StaticPoints: &static}, nil, 250},
		{"no event static value keeps the task points", Event{ScoringProfile: ScoringProfile{Mode: ScoringStatic}}, nil, 250},
	} {
		if got := tc.event.EffectiveStaticPoints(250, tc.local); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
