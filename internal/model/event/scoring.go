package eventModel

import (
	"math"
	"time"
)

type ScoringMode int16

const (
	ScoringStatic ScoringMode = iota
	ScoringPopularityCurve
	ScoringFirstSolvesLadder
	ScoringTimeDecay
)

// ScoringProfile describes one complete dynamic scoring configuration.
// FloorAtPercent is the share of the fixed population at which the score
// reaches MinPoints.
type ScoringProfile struct {
	Mode                 ScoringMode
	MinPoints, MaxPoints int32
	FloorAtPercent       int32
}

// ResolveScoringProfile applies the non-destructive precedence rule: an event
// force flag wins over a local override, but never removes that override.
func ResolveScoringProfile(eventProfile ScoringProfile, localProfile *ScoringProfile, forceEvent bool) ScoringProfile {
	if forceEvent || localProfile == nil {
		return eventProfile
	}
	return *localProfile
}

// Normalized drops parameters a mode does not use: time decay ignores
// FloorAtPercent (stored as 100), so a client may omit it (E7).
func (p ScoringProfile) Normalized() ScoringProfile {
	if p.Mode == ScoringTimeDecay && (p.FloorAtPercent < 1 || p.FloorAtPercent > 100) {
		p.FloorAtPercent = 100
	}
	return p
}

// ValidateFor verifies both the profile's own parameters and whether its
// algorithm can be applied to the configured event lifecycle.
func (p ScoringProfile) ValidateFor(lifecycle Lifecycle) error {
	if p.Mode == ScoringStatic {
		return nil
	}
	if p.Mode < ScoringPopularityCurve || p.Mode > ScoringTimeDecay || p.MinPoints < 0 ||
		p.MaxPoints <= p.MinPoints || p.FloorAtPercent < 1 || p.FloorAtPercent > 100 {
		return ErrEventScoringProfileInvalid.Err()
	}
	if (p.Mode == ScoringPopularityCurve || p.Mode == ScoringFirstSolvesLadder) && lifecycle.JoinPolicy != JoinPolicyLockedAtStart {
		return ErrEventScoringProfileInvalid.Err()
	}
	if p.Mode == ScoringTimeDecay && lifecycle.FinishAt == nil {
		return ErrEventScoringProfileInvalid.Err()
	}
	return nil
}

// AwardPoints centralizes the three dynamic algorithms. Static scoring keeps
// the event-challenge value supplied by the caller.
func AwardPoints(profile ScoringProfile, staticPoints, population, solveRank, solveCount int32, elapsed, duration time.Duration) int32 {
	switch profile.Mode {
	case ScoringPopularityCurve:
		return profile.AwardPopularity(solveCount, population)
	case ScoringFirstSolvesLadder:
		return profile.AwardFirstSolve(solveRank, population)
	case ScoringTimeDecay:
		return profile.AwardTime(elapsed, duration)
	default:
		return staticPoints
	}
}

func (p ScoringProfile) AwardPopularity(solves, population int32) int32 {
	return p.interpolate(progress(solves, population, p.FloorAtPercent))
}

func (p ScoringProfile) AwardFirstSolve(rank, population int32) int32 {
	if rank < 1 {
		rank = 1
	}
	if population <= 0 || p.FloorAtPercent <= 0 {
		return p.MinPoints
	}
	floorUnits := int32(math.Ceil(float64(population) * float64(p.FloorAtPercent) / 100))
	if floorUnits <= 1 {
		return p.MinPoints
	}
	return p.interpolate(float64(rank-1) / float64(floorUnits-1))
}

func (p ScoringProfile) AwardTime(elapsed, duration time.Duration) int32 {
	if duration <= 0 {
		return p.MinPoints
	}
	return p.interpolate(float64(elapsed) / float64(duration))
}

func (p ScoringProfile) interpolate(value float64) int32 {
	value = math.Max(0, math.Min(1, value))
	// Smoothstep avoids an abrupt first and final-point change.
	value = value * value * (3 - 2*value)
	return int32(math.Round(float64(p.MaxPoints) - float64(p.MaxPoints-p.MinPoints)*value))
}

func progress(numerator, population, floorPercent int32) float64 {
	if population <= 0 || floorPercent <= 0 {
		return 1
	}
	floorUnits := float64(population) * float64(floorPercent) / 100
	if floorUnits <= 0 {
		return 1
	}
	return float64(numerator) / floorUnits
}
