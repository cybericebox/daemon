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
