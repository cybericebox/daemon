package eventAnalyticsModel

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofrs/uuid"
)

// The integrity signals of «Доброчесність» (docs/EVENT-ANALYTICS.md §6.6,
// docs/ANTI-CHEAT.md). A signal is a hint for a human to review, attached to
// one solve and computed on read from timing, access evidence, answer
// similarity and attempt patterns only: no IP or user agent (§9.3), no
// verdict, no penalty and no automatic action.

// IntegrityKind is the kind of a signal.
type IntegrityKind string

const (
	// IntegrityCrossFlag: the team submitted the flag another team owns for
	// some task of the event (a value that is not one of its own flags).
	// The strongest signal; it only exists where flags differ per team.
	IntegrityCrossFlag IntegrityKind = "cross_flag"
	// IntegrityNoAccess: the team solved a task it never opened, downloaded
	// from or unlocked a hint of.
	IntegrityNoAccess IntegrityKind = "no_access"
	// IntegrityNoLab: a laboratory task solved while the team never
	// connected the VPN.
	IntegrityNoLab IntegrityKind = "no_lab"
	// IntegrityTooFast: the solve came sooner after the team's first open
	// of the task than the floor of the task's level.
	IntegrityTooFast IntegrityKind = "too_fast"
	// IntegrityFirstTryHard: a first-attempt solve of a task that most
	// solving teams needed many attempts for.
	IntegrityFirstTryHard IntegrityKind = "first_try_hard"
	// IntegritySharedWrong: wrong answers before the solve that other teams
	// submitted too.
	IntegritySharedWrong IntegrityKind = "shared_wrong"
	// IntegrityBurst: several solves by one team within a very short window,
	// far quicker than its own usual pace.
	IntegrityBurst IntegrityKind = "burst"
	// IntegrityBruteForce: many attempts (and rate-limit rejections) of the
	// team on the task within a short window before the solve.
	IntegrityBruteForce IntegrityKind = "brute_force"
	// IntegrityFollowsSolve: the solve came right after another team's solve
	// of the same task.
	IntegrityFollowsSolve IntegrityKind = "follows_solve"
)

// IntegrityKinds lists every kind, in the order the UI shows them.
var IntegrityKinds = []IntegrityKind{
	IntegrityCrossFlag, IntegrityNoAccess, IntegrityNoLab, IntegrityTooFast, IntegrityFirstTryHard,
	IntegritySharedWrong, IntegrityBurst, IntegrityBruteForce, IntegrityFollowsSolve,
}

// ValidIntegrityKind reports whether s names a kind.
func ValidIntegrityKind(s string) bool {
	for _, k := range IntegrityKinds {
		if string(k) == s {
			return true
		}
	}
	return false
}

// Dismissible reports whether an organizer can dismiss a pattern of the kind
// (it has a task and a stable key). burst has no task, cross_flag no stable key.
func (k IntegrityKind) Dismissible() bool {
	switch k {
	case IntegritySharedWrong, IntegrityNoAccess, IntegrityNoLab, IntegrityTooFast,
		IntegrityFirstTryHard, IntegrityBruteForce, IntegrityFollowsSolve:
		return true
	}
	return false
}

// RateLimitReason is the event_activity rejection reason of a submission
// refused for being too frequent.
const RateLimitReason = "rate_limit"

// MaxIntegrityItems bounds a response; the total is reported beside it.
const MaxIntegrityItems = 500

// IntegrityLevels are the task difficulty levels, easiest first. A task
// without a known level counts as medium.
var IntegrityLevels = []string{"elementary", "trivial", "easy", "medium", "hard", "insane"}

const defaultIntegrityLevel = "medium"

// NormalizeLevel returns level when known, medium otherwise.
func NormalizeLevel(level string) string {
	if levelRank(level) < 0 {
		return defaultIntegrityLevel
	}
	return level
}

func levelRank(level string) int {
	for i, l := range IntegrityLevels {
		if l == level {
			return i
		}
	}
	return -1
}

// Fixed detector parameters. Only the level floors and the two attempt
// windows are offered in the UI.
const (
	solveBurstMinSolves    = 3
	solveBurstWindow       = 2 * time.Minute
	solveBurstFactor       = 3
	solveBurstMinHistory   = 6
	firstTryMinTeams       = 5
	firstTryMedianAttempts = 4
	noLabGrace             = 10 * time.Minute
	maxSharedTeams         = 5
	maxCrossTimes          = 20
)

// IntegrityThresholds tunes the detectors. Defaults come from
// DefaultIntegrityThresholds; Clamped keeps caller values in safe ranges.
type IntegrityThresholds struct {
	// Floors: the shortest plausible time from the team's first open of a
	// task to its solve, per level. Zero switches the signal off for the
	// level. It is a rough estimate, not a rule.
	Floors map[string]time.Duration
	// SharedMinLength: shorter wrong answers are too common to compare.
	SharedMinLength int
	// BruteForceAttempts within BruteForceWindow (attempts plus rate-limit
	// rejections of one team on one task) make a brute_force signal.
	BruteForceAttempts int
	BruteForceWindow   time.Duration
	// FollowGap: a solve within this time after another team's solve of the
	// same task makes a follows_solve signal.
	FollowGap time.Duration
}

// DefaultIntegrityThresholds are the defaults offered in the UI.
func DefaultIntegrityThresholds() IntegrityThresholds {
	return IntegrityThresholds{
		Floors: map[string]time.Duration{
			"elementary": 0, "trivial": 5 * time.Second, "easy": 20 * time.Second,
			"medium": time.Minute, "hard": 2 * time.Minute, "insane": 4 * time.Minute,
		},
		SharedMinLength:    6,
		BruteForceAttempts: 15, BruteForceWindow: time.Minute,
		FollowGap: time.Minute,
	}
}

// Clamped returns the thresholds with every value inside its allowed range;
// a missing floor takes its default.
func (t IntegrityThresholds) Clamped() IntegrityThresholds {
	defaults := DefaultIntegrityThresholds()
	floors := make(map[string]time.Duration, len(IntegrityLevels))
	for _, level := range IntegrityLevels {
		floor, ok := t.Floors[level]
		if !ok {
			floor = defaults.Floors[level]
		}
		floors[level] = clampDuration(floor, 0, time.Hour)
	}
	t.Floors = floors
	t.SharedMinLength = clampInt(t.SharedMinLength, 1, 100)
	t.BruteForceAttempts = clampInt(t.BruteForceAttempts, 3, 1000)
	t.BruteForceWindow = clampDuration(t.BruteForceWindow, 10*time.Second, time.Hour)
	t.FollowGap = clampDuration(t.FollowGap, 5*time.Second, time.Hour)
	return t
}

func clampInt(v, lo, hi int) int { return min(max(v, lo), hi) }

func clampDuration(v, lo, hi time.Duration) time.Duration { return min(max(v, lo), hi) }

// FlagKind tells whether the teams of a task share one flag.
type FlagKind int

const (
	FlagUnknown FlagKind = iota
	FlagStatic
	FlagDynamic
)

type (
	IntegrityTeam struct {
		ID   uuid.UUID
		Name string
	}

	// IntegritySolve is a solve with the access evidence of the solving team.
	// A nil moment means «never happened». Level is the declared difficulty.
	IntegritySolve struct {
		TeamChallengeID uuid.UUID
		TeamID          uuid.UUID
		TeamName        string
		ChallengeID     uuid.UUID
		ChallengeName   string
		// ExerciseID and TaskID name the catalog task (stable across events).
		ExerciseID      uuid.UUID
		TaskID          uuid.UUID
		Level           string
		AttachmentCount int
		SolvedAt        time.Time
		// HasLab: the task runs a laboratory for the team.
		HasLab bool
		// Lab is the lab-traffic verdict for the team's contact with the lab before
		// the solve: "touched", "untouched", or empty when unknown or not asked.
		// Only "untouched" is a fact; anything else falls back on the VPN sessions.
		// LabAttempted: it knocked, the lab never answered (first at LabFirstSeen).
		Lab          string
		LabAttempted bool
		LabFirstSeen *time.Time
		Flag         FlagKind
		FirstOpen    *time.Time
		FirstFile    *time.Time
		FirstHint    *time.Time
		FirstVPN     *time.Time
	}

	// IntegrityAttempt is an answer of a team to a task (rejected ones
	// excluded).
	IntegrityAttempt struct {
		TeamChallengeID uuid.UUID
		TeamID          uuid.UUID
		ChallengeID     uuid.UUID
		Answer          string
		Correct         bool
		At              time.Time
	}

	// IntegrityRejection is a submission the platform refused.
	IntegrityRejection struct {
		TeamID      uuid.UUID
		ChallengeID uuid.UUID
		Reason      string
		At          time.Time
	}

	// IntegrityCrossFlag is one submission of a value equal to the expected
	// flag of another team's task. Only teams visible in the results.
	IntegrityCrossSubmission struct {
		TeamChallengeID uuid.UUID
		TeamID          uuid.UUID
		TeamName        string
		ChallengeID     uuid.UUID
		ChallengeName   string
		ExerciseID      uuid.UUID
		TaskID          uuid.UUID
		Level           string
		At              time.Time
		OwnerTeam       IntegrityTeam
		OwnerChallenge  uuid.UUID
		OwnerName       string
	}

	// IntegrityFacts are the inputs of the detectors. Only teams that are
	// visible in the results are in them.
	IntegrityFacts struct {
		CrossFlags []IntegrityCrossSubmission
		Solves     []IntegritySolve
		Attempts   []IntegrityAttempt
		Rejections []IntegrityRejection
		// CollectorStart is the first task open the event recorded (nil: none).
		// A solve before it cannot be judged by access.
		CollectorStart *time.Time
	}

	// IntegritySubmission is a team's first submission of a value.
	IntegritySubmission struct {
		Team IntegrityTeam
		At   time.Time
	}

	// SharedAnswer is a wrong value several teams submitted, with who sent it
	// first, second, ... (Order). Value is trimmed and lower-cased: it is also
	// the key a dismissal uses.
	SharedAnswer struct {
		Value string
		Order []IntegritySubmission
	}

	// IntegrityCrossOwner is the team and task the submitted flag belongs to.
	IntegrityCrossOwner struct {
		Team          IntegrityTeam
		ChallengeID   uuid.UUID
		ChallengeName string
		// SameTask: the flag belongs to the very task the team submitted to.
		SameTask bool
	}

	// IntegritySignal is one reason with its evidence. The meaning of the
	// numbers depends on Kind:
	//   cross_flag     Count = such submissions, At = the last, Owner = whose flag and which task
	//   no_access      nothing
	//   no_lab         Extra = 1 when the team knocked but the lab never answered (At = first knock)
	//   too_fast       Seconds = time from the first open, Baseline = floor
	//   first_try_hard Count = solving teams compared, Baseline = their median attempts
	//   shared_wrong   Count = shared values, Answers = the values with the order of submission;
	//                  Info = the task has a static flag, so common paths are expected
	//   burst          Count = solves in the window, Seconds = the window, Baseline = own median gap (s)
	//   brute_force    Count = attempts, Extra = rate-limit rejections, Seconds = the window
	//   follows_solve  Seconds = gap, Count = own attempts before, Teams = the earlier team
	IntegritySignal struct {
		Kind     IntegrityKind
		Count    int
		Extra    int
		Seconds  int64
		Baseline int64
		Teams    []IntegrityTeam
		Answers  []SharedAnswer
		Owner    *IntegrityCrossOwner
		At       time.Time
		// Times: every cross_flag submission (capped), for the attempts journal.
		Times []time.Time
		// Info marks a low-weight signal.
		Info bool
	}

	// FlaggedSolve is a solve (or, for cross_flag, a team's task) with at
	// least one signal. Solved is false for a task flagged before any solve.
	FlaggedSolve struct {
		TeamChallengeID uuid.UUID
		Team            IntegrityTeam
		ChallengeID     uuid.UUID
		ChallengeName   string
		ExerciseID      uuid.UUID
		TaskID          uuid.UUID
		Level           string
		Solved          bool
		// At is the solve, or the last cross_flag submission when unsolved.
		At      time.Time
		Signals []IntegritySignal
	}
)

// Weight of a signal, for ordering: cross_flag is the strongest, info the weakest.
func (s IntegritySignal) Weight() int {
	switch {
	case s.Kind == IntegrityCrossFlag:
		return 4
	case s.Info:
		return 1
	}
	return 2
}

// Score is the total weight of a flagged solve.
func (f FlaggedSolve) Score() int {
	n := 0
	for _, s := range f.Signals {
		n += s.Weight()
	}
	return n
}

// DetectIntegrity runs the detectors over the solves whose time falls in
// [from, to) (cross_flag: over the submissions in it) and returns the flagged
// ones: heaviest first, then newest. now is the moment of the read (a solve
// too fresh for its VPN rollup is not judged by the lab).
func DetectIntegrity(facts IntegrityFacts, th IntegrityThresholds, from, to, now time.Time) []FlaggedSolve {
	th = th.Clamped()
	idx := newIntegrityIndex(facts, th.SharedMinLength)
	inPeriod := func(at time.Time) bool { return !at.Before(from) && at.Before(to) }
	var out []FlaggedSolve
	byTeamChallenge := map[uuid.UUID]int{}
	for _, s := range facts.Solves {
		if !inPeriod(s.SolvedAt) {
			continue
		}
		var signals []IntegritySignal
		add := func(sig IntegritySignal, ok bool) {
			if ok {
				signals = append(signals, sig)
			}
		}
		add(detectNoAccess(s, facts.CollectorStart))
		add(detectNoLab(s, now))
		add(detectTooFast(s, th))
		add(idx.firstTryHard(s))
		add(idx.sharedWrong(s, th))
		add(idx.solveBurst(s))
		add(idx.bruteForce(s, th))
		add(idx.followsSolve(s, th))
		if len(signals) == 0 {
			continue
		}
		byTeamChallenge[s.TeamChallengeID] = len(out)
		out = append(out, FlaggedSolve{
			TeamChallengeID: s.TeamChallengeID, Team: IntegrityTeam{ID: s.TeamID, Name: s.TeamName},
			ChallengeID: s.ChallengeID, ChallengeName: s.ChallengeName, ExerciseID: s.ExerciseID, TaskID: s.TaskID,
			Level: NormalizeLevel(s.Level), Solved: true, At: s.SolvedAt, Signals: signals,
		})
	}
	out = mergeCrossFlags(out, byTeamChallenge, facts, inPeriod)
	sortFlagged(out)
	return out
}

func sortFlagged(out []FlaggedSolve) {
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := out[i].Score(), out[j].Score(); a != b {
			return a > b
		}
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].TeamChallengeID.String() < out[j].TeamChallengeID.String()
	})
}

// mergeCrossFlags adds one cross_flag signal per team task to the flagged
// solve of that task, or opens an unsolved item for it.
func mergeCrossFlags(out []FlaggedSolve, byTeamChallenge map[uuid.UUID]int, facts IntegrityFacts, inPeriod func(time.Time) bool) []FlaggedSolve {
	solvedAt := map[uuid.UUID]time.Time{}
	for _, s := range facts.Solves {
		solvedAt[s.TeamChallengeID] = s.SolvedAt
	}
	sorted := append([]IntegrityCrossSubmission(nil), facts.CrossFlags...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })
	type group struct {
		first, last IntegrityCrossSubmission
		count       int
		times       []time.Time
	}
	groups := map[uuid.UUID]*group{}
	var order []uuid.UUID
	for _, c := range sorted {
		if !inPeriod(c.At) {
			continue
		}
		g, ok := groups[c.TeamChallengeID]
		if !ok {
			g = &group{first: c}
			groups[c.TeamChallengeID] = g
			order = append(order, c.TeamChallengeID)
		}
		g.last = c
		g.count++
		if len(g.times) < maxCrossTimes {
			g.times = append(g.times, c.At)
		}
	}
	for _, id := range order {
		g := groups[id]
		sig := IntegritySignal{
			Kind: IntegrityCrossFlag, Count: g.count, At: g.last.At, Times: g.times,
			Owner: &IntegrityCrossOwner{
				Team: g.first.OwnerTeam, ChallengeID: g.first.OwnerChallenge, ChallengeName: g.first.OwnerName,
				SameTask: g.first.OwnerChallenge == g.first.ChallengeID,
			},
		}
		if i, ok := byTeamChallenge[id]; ok {
			out[i].Signals = append([]IntegritySignal{sig}, out[i].Signals...)
			continue
		}
		c := g.first
		item := FlaggedSolve{
			TeamChallengeID: c.TeamChallengeID, Team: IntegrityTeam{ID: c.TeamID, Name: c.TeamName},
			ChallengeID: c.ChallengeID, ChallengeName: c.ChallengeName, ExerciseID: c.ExerciseID, TaskID: c.TaskID,
			Level: NormalizeLevel(c.Level), At: g.last.At, Signals: []IntegritySignal{sig},
		}
		if at, solved := solvedAt[id]; solved {
			item.Solved, item.At = true, at
		}
		out = append(out, item)
	}
	return out
}

func notAfter(moment *time.Time, limit time.Time) bool { return moment != nil && !moment.After(limit) }

func detectNoAccess(s IntegritySolve, collectorStart *time.Time) (IntegritySignal, bool) {
	if collectorStart == nil || s.SolvedAt.Before(*collectorStart) {
		return IntegritySignal{}, false
	}
	if notAfter(s.FirstOpen, s.SolvedAt) || notAfter(s.FirstFile, s.SolvedAt) || notAfter(s.FirstHint, s.SolvedAt) {
		return IntegritySignal{}, false
	}
	return IntegritySignal{Kind: IntegrityNoAccess}, true
}

// LabUntouched and LabTouched are the lab-traffic verdicts detectNoLab reads.
const (
	LabTouched   = "touched"
	LabUntouched = "untouched"
)

func detectNoLab(s IntegritySolve, now time.Time) (IntegritySignal, bool) {
	if !s.HasLab || now.Sub(s.SolvedAt) < noLabGrace {
		return IntegritySignal{}, false
	}
	switch s.Lab {
	case LabTouched:
		return IntegritySignal{}, false
	case LabUntouched:
		sig := IntegritySignal{Kind: IntegrityNoLab}
		if s.LabAttempted {
			sig.Extra = 1
			if s.LabFirstSeen != nil {
				sig.At = *s.LabFirstSeen
			}
		}
		return sig, true
	}
	// No per-lab answer (no collector, unknown coverage): the VPN sessions.
	if notAfter(s.FirstVPN, s.SolvedAt) {
		return IntegritySignal{}, false
	}
	return IntegritySignal{Kind: IntegrityNoLab}, true
}

func detectTooFast(s IntegritySolve, th IntegrityThresholds) (IntegritySignal, bool) {
	if !notAfter(s.FirstOpen, s.SolvedAt) {
		return IntegritySignal{}, false
	}
	floor := th.Floors[NormalizeLevel(s.Level)]
	elapsed := s.SolvedAt.Sub(*s.FirstOpen)
	if floor <= 0 || elapsed >= floor {
		return IntegritySignal{}, false
	}
	return IntegritySignal{Kind: IntegrityTooFast, Seconds: int64(elapsed / time.Second), Baseline: int64(floor / time.Second)}, true
}

type teamTask struct{ team, challenge uuid.UUID }

type integrityIndex struct {
	solves []IntegritySolve
	// attempts per solve (team challenge), oldest first.
	byTeamChallenge map[uuid.UUID][]IntegrityAttempt
	// solves per task, oldest first; per team, oldest first.
	solvesByTask map[uuid.UUID][]IntegritySolve
	solvesByTeam map[uuid.UUID][]IntegritySolve
	// wrong answers: task + normalized answer -> teams that submitted it.
	wrongTeams map[wrongKey]map[uuid.UUID]wrongFirst
	// attempt moments and rate-limit rejections per team and task.
	moments map[teamTask][]moment
}

type wrongKey struct {
	challenge uuid.UUID
	answer    string
}

// wrongFirst is a team's first submission of a wrong value.
type wrongFirst struct {
	name string
	at   time.Time
}

type moment struct {
	at       time.Time
	rejected bool
}

// NormalizeAnswer is the form in which wrong answers are compared and in which
// a shared_wrong dismissal keeps its value.
func NormalizeAnswer(answer string) string { return normalizeAnswer(answer) }

func normalizeAnswer(answer string) string { return strings.ToLower(strings.TrimSpace(answer)) }

func newIntegrityIndex(facts IntegrityFacts, sharedMinLength int) *integrityIndex {
	idx := &integrityIndex{
		solves:          facts.Solves,
		byTeamChallenge: map[uuid.UUID][]IntegrityAttempt{},
		solvesByTask:    map[uuid.UUID][]IntegritySolve{},
		solvesByTeam:    map[uuid.UUID][]IntegritySolve{},
		wrongTeams:      map[wrongKey]map[uuid.UUID]wrongFirst{},
		moments:         map[teamTask][]moment{},
	}
	teamNames := map[uuid.UUID]string{}
	for _, s := range facts.Solves {
		idx.solvesByTask[s.ChallengeID] = append(idx.solvesByTask[s.ChallengeID], s)
		idx.solvesByTeam[s.TeamID] = append(idx.solvesByTeam[s.TeamID], s)
		teamNames[s.TeamID] = s.TeamName
	}
	for _, list := range idx.solvesByTask {
		sortSolves(list)
	}
	for _, list := range idx.solvesByTeam {
		sortSolves(list)
	}
	for _, a := range facts.Attempts {
		idx.byTeamChallenge[a.TeamChallengeID] = append(idx.byTeamChallenge[a.TeamChallengeID], a)
		key := teamTask{a.TeamID, a.ChallengeID}
		idx.moments[key] = append(idx.moments[key], moment{at: a.At})
	}
	for _, r := range facts.Rejections {
		if r.Reason == RateLimitReason {
			key := teamTask{r.TeamID, r.ChallengeID}
			idx.moments[key] = append(idx.moments[key], moment{at: r.At, rejected: true})
		}
	}
	for _, list := range idx.byTeamChallenge {
		sort.SliceStable(list, func(i, j int) bool { return list[i].At.Before(list[j].At) })
		for _, a := range list {
			norm := normalizeAnswer(a.Answer)
			if a.Correct || utf8.RuneCountInString(norm) < sharedMinLength {
				continue
			}
			key := wrongKey{a.ChallengeID, norm}
			if idx.wrongTeams[key] == nil {
				idx.wrongTeams[key] = map[uuid.UUID]wrongFirst{}
			}
			if first, ok := idx.wrongTeams[key][a.TeamID]; !ok || a.At.Before(first.at) {
				idx.wrongTeams[key][a.TeamID] = wrongFirst{name: teamNames[a.TeamID], at: a.At}
			}
		}
	}
	for _, list := range idx.moments {
		sort.SliceStable(list, func(i, j int) bool { return list[i].at.Before(list[j].at) })
	}
	return idx
}

func sortSolves(list []IntegritySolve) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].SolvedAt.Before(list[j].SolvedAt) })
}

// attemptsUntil counts the attempts of a solve up to and including the
// solving one.
func (x *integrityIndex) attemptsUntil(s IntegritySolve) int {
	n := 0
	for _, a := range x.byTeamChallenge[s.TeamChallengeID] {
		if !a.At.After(s.SolvedAt) {
			n++
		}
	}
	return n
}

func (x *integrityIndex) firstTryHard(s IntegritySolve) (IntegritySignal, bool) {
	if levelRank(NormalizeLevel(s.Level)) < levelRank(defaultIntegrityLevel) || x.attemptsUntil(s) != 1 {
		return IntegritySignal{}, false
	}
	var others []int
	for _, o := range x.solvesByTask[s.ChallengeID] {
		if o.TeamID == s.TeamID {
			continue
		}
		if n := x.attemptsUntil(o); n > 0 {
			others = append(others, n)
		}
	}
	if len(others) < firstTryMinTeams {
		return IntegritySignal{}, false
	}
	sort.Ints(others)
	median := others[len(others)/2]
	if median < firstTryMedianAttempts {
		return IntegritySignal{}, false
	}
	return IntegritySignal{Kind: IntegrityFirstTryHard, Count: len(others), Baseline: int64(median)}, true
}

func (x *integrityIndex) sharedWrong(s IntegritySolve, th IntegrityThresholds) (IntegritySignal, bool) {
	var answers []SharedAnswer
	others := map[uuid.UUID]string{}
	seen := map[string]bool{}
	for _, a := range x.byTeamChallenge[s.TeamChallengeID] {
		if a.Correct || a.At.After(s.SolvedAt) {
			continue
		}
		norm := normalizeAnswer(a.Answer)
		if seen[norm] || utf8.RuneCountInString(norm) < th.SharedMinLength {
			continue
		}
		seen[norm] = true
		teams := x.wrongTeams[wrongKey{a.ChallengeID, norm}]
		shared := false
		for team := range teams {
			shared = shared || team != s.TeamID
		}
		if !shared {
			continue
		}
		order := make([]IntegritySubmission, 0, len(teams))
		for team, sub := range teams {
			order = append(order, IntegritySubmission{Team: IntegrityTeam{ID: team, Name: sub.name}, At: sub.at})
			if team != s.TeamID {
				others[team] = sub.name
			}
		}
		sort.Slice(order, func(i, j int) bool {
			if !order[i].At.Equal(order[j].At) {
				return order[i].At.Before(order[j].At)
			}
			return order[i].Team.ID.String() < order[j].Team.ID.String()
		})
		if len(order) > maxSharedTeams {
			order = trimAroundTeam(order, s.TeamID, maxSharedTeams)
		}
		answers = append(answers, SharedAnswer{Value: norm, Order: order})
	}
	if len(answers) == 0 {
		return IntegritySignal{}, false
	}
	sort.Slice(answers, func(i, j int) bool { return answers[i].Value < answers[j].Value })
	return IntegritySignal{
		Kind: IntegritySharedWrong, Count: len(answers), Teams: sortedTeams(others, maxSharedTeams),
		Answers: answers, Info: s.Flag == FlagStatic,
	}, true
}

// trimAroundTeam keeps the first limit submissions, making sure the team's
// own is among them.
func trimAroundTeam(order []IntegritySubmission, team uuid.UUID, limit int) []IntegritySubmission {
	head := order[:limit]
	for _, o := range head {
		if o.Team.ID == team {
			return head
		}
	}
	for _, o := range order[limit:] {
		if o.Team.ID == team {
			return append(append([]IntegritySubmission(nil), head[:limit-1]...), o)
		}
	}
	return head
}

func sortedTeams(teams map[uuid.UUID]string, limit int) []IntegrityTeam {
	out := make([]IntegrityTeam, 0, len(teams))
	for id, name := range teams {
		out = append(out, IntegrityTeam{ID: id, Name: name})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// solveBurst: at least solveBurstMinSolves solves of the team within
// solveBurstWindow, at a pace solveBurstFactor times quicker than its own
// median gap between solves.
func (x *integrityIndex) solveBurst(s IntegritySolve) (IntegritySignal, bool) {
	list := x.solvesByTeam[s.TeamID]
	if len(list) < solveBurstMinHistory {
		return IntegritySignal{}, false
	}
	gaps := make([]time.Duration, 0, len(list)-1)
	for i := 1; i < len(list); i++ {
		gaps = append(gaps, list[i].SolvedAt.Sub(list[i-1].SolvedAt))
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	median := gaps[len(gaps)/2]
	for i := range list {
		j := i
		for j+1 < len(list) && list[j+1].SolvedAt.Sub(list[i].SolvedAt) <= solveBurstWindow {
			j++
		}
		count := j - i + 1
		if count < solveBurstMinSolves {
			continue
		}
		var member bool
		for k := i; k <= j; k++ {
			member = member || list[k].TeamChallengeID == s.TeamChallengeID
		}
		span := list[j].SolvedAt.Sub(list[i].SolvedAt)
		if member && span*solveBurstFactor/time.Duration(count-1) <= median {
			return IntegritySignal{Kind: IntegrityBurst, Count: count, Seconds: int64(span / time.Second), Baseline: int64(median / time.Second)}, true
		}
	}
	return IntegritySignal{}, false
}

func (x *integrityIndex) bruteForce(s IntegritySolve, th IntegrityThresholds) (IntegritySignal, bool) {
	ms := x.moments[teamTask{s.TeamID, s.ChallengeID}]
	for i := 0; i < len(ms) && !ms[i].at.After(s.SolvedAt); {
		j := i
		for j+1 < len(ms) && ms[j+1].at.Sub(ms[i].at) <= th.BruteForceWindow {
			j++
		}
		if j-i+1 >= th.BruteForceAttempts {
			rejected := 0
			for _, m := range ms[i : j+1] {
				if m.rejected {
					rejected++
				}
			}
			return IntegritySignal{
				Kind: IntegrityBruteForce, Count: j - i + 1 - rejected, Extra: rejected,
				Seconds: int64(ms[j].at.Sub(ms[i].at) / time.Second),
			}, true
		}
		i++
	}
	return IntegritySignal{}, false
}

func (x *integrityIndex) followsSolve(s IntegritySolve, th IntegrityThresholds) (IntegritySignal, bool) {
	var prev *IntegritySolve
	for _, o := range x.solvesByTask[s.ChallengeID] {
		if o.TeamID != s.TeamID && o.SolvedAt.Before(s.SolvedAt) {
			o := o
			prev = &o
		}
	}
	if prev == nil {
		return IntegritySignal{}, false
	}
	gap := s.SolvedAt.Sub(prev.SolvedAt)
	if gap > th.FollowGap {
		return IntegritySignal{}, false
	}
	return IntegritySignal{
		Kind: IntegrityFollowsSolve, Seconds: int64(gap / time.Second), Count: x.attemptsUntil(s),
		Teams: []IntegrityTeam{{ID: prev.TeamID, Name: prev.TeamName}},
	}, true
}

// DismissScope says how far a dismissal reaches.
type DismissScope string

const (
	// DismissEvent: this event only.
	DismissEvent DismissScope = "event"
	// DismissExercise: every event that uses the catalog exercise.
	DismissExercise DismissScope = "exercise"
)

// Dismissal is an organizer's «do not highlight such cases»: a signal kind on
// a catalog task, with the key that makes the case (for shared_wrong the
// normalized value, for the other kinds empty). The caller passes only the
// dismissals that apply to the event (its own and its exercises').
type Dismissal struct {
	ExerciseID uuid.UUID
	TaskID     uuid.UUID
	Kind       IntegrityKind
	Key        string
}

// ApplyDismissals removes the dismissed signals (and the dismissed values of a
// shared_wrong) and drops the solves left with none.
func ApplyDismissals(flagged []FlaggedSolve, dismissals []Dismissal) []FlaggedSolve {
	if len(dismissals) == 0 {
		return flagged
	}
	type key struct {
		exercise, task uuid.UUID
		kind           IntegrityKind
		value          string
	}
	set := make(map[key]bool, len(dismissals))
	for _, d := range dismissals {
		set[key{d.ExerciseID, d.TaskID, d.Kind, d.Key}] = true
	}
	out := make([]FlaggedSolve, 0, len(flagged))
	for _, f := range flagged {
		kept := make([]IntegritySignal, 0, len(f.Signals))
		for _, sig := range f.Signals {
			if sig.Kind == IntegritySharedWrong {
				answers := make([]SharedAnswer, 0, len(sig.Answers))
				for _, a := range sig.Answers {
					if !set[key{f.ExerciseID, f.TaskID, sig.Kind, a.Value}] {
						answers = append(answers, a)
					}
				}
				if len(answers) == 0 {
					continue
				}
				sig.Answers, sig.Count = answers, len(answers)
			} else if sig.Kind.Dismissible() && set[key{f.ExerciseID, f.TaskID, sig.Kind, ""}] {
				continue
			}
			kept = append(kept, sig)
		}
		if len(kept) == 0 {
			continue
		}
		f.Signals = kept
		out = append(out, f)
	}
	sortFlagged(out)
	return out
}
