package eventAnalyticsModel

import (
	"sort"
	"time"

	"github.com/gofrs/uuid"
)

// Stand and task-lab status codes as stored in event_stand_transitions
// (the eventStand and labBinding models).
const (
	StandSourceStand = "stand"
	StandSourceLab   = "lab"

	standCreating int16 = 1
	standReady    int16 = 2
	standFailed   int16 = 3

	labReady  int16 = 1
	labFailed int16 = 2
)

type (
	// StandTransition is one row of the status log.
	StandTransition struct {
		TeamID        uuid.UUID
		Source        string
		ChallengeID   *uuid.UUID
		ChallengeName string
		Generation    int32
		To            int16
		Reason        string
		At            time.Time
	}

	// StandFailure is one failure of a stand or a task lab and how it ended.
	StandFailure struct {
		Source        string
		ChallengeName string
		At            time.Time
		Reason        string
		// RecoveredAt is nil while the failure is unresolved.
		RecoveredAt *time.Time
	}

	// StandHistory is what the log says about one team.
	StandHistory struct {
		// DeploySeconds: from the stand's creation to its first ready of the
		// latest generation that got ready (nil: never ready).
		DeploySeconds *int64
		Generations   int
		Failures      []StandFailure
	}
)

// RecoverySeconds is the time a resolved failure took.
func (f StandFailure) RecoverySeconds() (int64, bool) {
	if f.RecoveredAt == nil {
		return 0, false
	}
	return int64(f.RecoveredAt.Sub(f.At) / time.Second), true
}

// AnalyzeStandTransitions folds the status log into a history per team.
// A failure opens on a failed status of a stand (or of one task lab) and is
// resolved by the next ready of the same stand (or lab); a teardown does not
// resolve it.
func AnalyzeStandTransitions(log []StandTransition) map[uuid.UUID]StandHistory {
	sorted := append([]StandTransition(nil), log...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	type track struct {
		source    string
		challenge uuid.UUID
	}
	type generation struct {
		created time.Time
		ready   *time.Time
	}
	histories := map[uuid.UUID]*StandHistory{}
	generations := map[uuid.UUID]map[int32]*generation{}
	open := map[uuid.UUID]map[track]int{}
	history := func(team uuid.UUID) *StandHistory {
		h, ok := histories[team]
		if !ok {
			h = &StandHistory{}
			histories[team] = h
			generations[team] = map[int32]*generation{}
			open[team] = map[track]int{}
		}
		return h
	}

	for _, tr := range sorted {
		h := history(tr.TeamID)
		k := track{source: tr.Source}
		if tr.ChallengeID != nil {
			k.challenge = *tr.ChallengeID
		}
		failedCode, readyCode := standFailed, standReady
		if tr.Source == StandSourceLab {
			failedCode, readyCode = labFailed, labReady
		}

		if tr.Source == StandSourceStand {
			g := generations[tr.TeamID][tr.Generation]
			if g == nil && tr.To == standCreating {
				g = &generation{created: tr.At}
				generations[tr.TeamID][tr.Generation] = g
			}
			if g != nil && g.ready == nil && tr.To == standReady {
				at := tr.At
				g.ready = &at
			}
		}

		switch {
		case tr.To == failedCode:
			if _, isOpen := open[tr.TeamID][k]; !isOpen {
				h.Failures = append(h.Failures, StandFailure{Source: tr.Source, ChallengeName: tr.ChallengeName, At: tr.At, Reason: tr.Reason})
				open[tr.TeamID][k] = len(h.Failures) - 1
			}
		case tr.To == readyCode:
			if idx, isOpen := open[tr.TeamID][k]; isOpen {
				at := tr.At
				h.Failures[idx].RecoveredAt = &at
				delete(open[tr.TeamID], k)
			}
		}
	}

	out := make(map[uuid.UUID]StandHistory, len(histories))
	for team, h := range histories {
		h.Generations = len(generations[team])
		var latest int32 = -1
		for n, g := range generations[team] {
			if g.ready != nil && n > latest {
				latest = n
			}
		}
		if latest >= 0 {
			g := generations[team][latest]
			seconds := int64(g.ready.Sub(g.created) / time.Second)
			h.DeploySeconds = &seconds
		}
		out[team] = *h
	}
	return out
}
