// Package labTraffic owns per-user lab access accounting: one aggregate per
// event, team, user and lab target, plus the collector coverage that tells
// «did not touch» from «we could not see». It holds counts and times only,
// never payloads and never a user network address. What it attributes is a VPN
// config or a proxy token, not a person.
package labTraffic

import (
	"time"

	"github.com/gofrs/uuid"

	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

type Surface string

const (
	SurfaceVPN   Surface = "vpn"
	SurfaceProxy Surface = "proxy"
)

// CoverageTolerance is the largest silence between two reports of a healthy
// collector (reports are one minute apart) that still counts as continuous.
const CoverageTolerance = 150 * time.Second

// UserFromSubject returns the user of a LabGroupClient name. Both the VPN and
// the web proxy report the client (group, client, lab), so the surface does not
// change the mapping. Anything that is not a participant client (the shared
// «tester» client, a staff client of a group the platform does not count) is
// not attributable.
func UserFromSubject(_ Surface, subject string) (uuid.UUID, bool) {
	return labBindingModel.ParseParticipantClientName(subject)
}

// Touch is one final row: the cumulative totals of one participant's VPN config
// or proxy token against one lab (task), as the collector reports them. Every
// report carries the full current values and the store overwrites.
type Touch struct {
	EventID, TeamID, UserID, EventChallengeID uuid.UUID
	Surface                                   Surface
	Attempts                                  int64
	PacketsOut, PacketsIn                     int64
	BytesOut, BytesIn                         int64
	FirstSeenAt                               time.Time
	LastSeenAt                                time.Time
	FirstRespondAt                            *time.Time
}

// Coverage is a span the collector actually observed.
type Coverage struct {
	From, To time.Time
	Partial  bool
}

// Verdict is the tri-state answer. Missing data is never «no touch».
type Verdict string

const (
	Touched   Verdict = "touched"
	Untouched Verdict = "untouched"
	Unknown   Verdict = "unknown"
)

// Aggregate is the stored summary of the rows that match a question.
type Aggregate struct {
	Surface        Surface
	Attempts       int64
	FirstSeenAt    time.Time
	FirstRespondAt *time.Time
	BytesIn        int64
}

// Question asks whether User (or the whole team when User is nil) touched one
// task before Before. Since is the earliest moment a touch was possible (the
// lab deployment); Surfaces are the paths that exist for the task.
type Question struct {
	EventID, TeamID, EventChallengeID uuid.UUID
	UserID                            uuid.UUID // uuid.Nil = anybody in the team
	Before                            time.Time
	Since                             time.Time
	Surfaces                          []Surface
}

// Answer is the result of the question.
type Answer struct {
	Verdict Verdict
	// FirstSeenAt is the first attempt of any kind, FirstRespondAt the first
	// time the lab answered (the moment a touch counts). Both are nil when unset.
	FirstSeenAt    *time.Time
	FirstRespondAt *time.Time
	Surface        Surface
	// Attempted is set when there were attempts but the lab never answered
	// before Before (a closed port, a scan).
	Attempted bool
	BytesIn   int64
}

// Classify applies the tri-state rule. rows are the aggregates with an attempt
// before q.Before; coverage lists the collector spans of every surface.
//
//   - a row whose lab answered before q.Before: Touched;
//   - otherwise, if every required surface was observed without a gap from
//     q.Since to q.Before: Untouched (Attempted says if it knocked);
//   - otherwise: Unknown.
func Classify(q Question, rows []Aggregate, coverage map[Surface][]Coverage) Answer {
	var answer Answer
	for _, row := range rows {
		if !row.FirstSeenAt.Before(q.Before) {
			continue
		}
		answer.Attempted = answer.Attempted || row.Attempts > 0
		if answer.FirstSeenAt == nil || row.FirstSeenAt.Before(*answer.FirstSeenAt) {
			t := row.FirstSeenAt
			answer.FirstSeenAt = &t
		}
		if row.FirstRespondAt != nil && row.FirstRespondAt.Before(q.Before) {
			if answer.FirstRespondAt == nil || row.FirstRespondAt.Before(*answer.FirstRespondAt) {
				t := *row.FirstRespondAt
				answer.FirstRespondAt = &t
				answer.Surface = row.Surface
			}
			answer.BytesIn += row.BytesIn
		}
	}
	if answer.FirstRespondAt != nil {
		answer.Verdict = Touched
		return answer
	}
	answer.Verdict = Untouched
	for _, surface := range q.Surfaces {
		if !Covered(coverage[surface], q.Since, q.Before) {
			answer.Verdict = Unknown
			break
		}
	}
	if len(q.Surfaces) == 0 || q.Since.IsZero() {
		answer.Verdict = Unknown
	}
	return answer
}

// Covered reports whether the non-partial spans join, within CoverageTolerance,
// into one run that includes [from, to].
func Covered(spans []Coverage, from, to time.Time) bool {
	if !from.Before(to) {
		return len(spans) > 0
	}
	cursor := from
	remaining := make([]Coverage, 0, len(spans))
	for _, s := range spans {
		if !s.Partial {
			remaining = append(remaining, s)
		}
	}
	// Repeatedly extend the covered run; span counts are small.
	for progressed := true; progressed && cursor.Before(to); {
		progressed = false
		for _, s := range remaining {
			if !s.From.After(cursor.Add(CoverageTolerance)) && s.To.After(cursor) {
				cursor = s.To
				progressed = true
			}
		}
	}
	return !cursor.Before(to.Add(-CoverageTolerance))
}
