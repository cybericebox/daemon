package eventModel

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofrs/uuid"
)

// Event stages (docs/specs/event-stages.md). A stage only controls the availability window of the exercise sets
// attached to it; everything else stays event-level. Order is time: stages never overlap, so opens_at is the order.

const stageNameMaxLen = 100

// StagePhase is where a set stands relative to its stage at a moment. It mirrors the SQL function
// event_stage_phase, which every query uses; the two are tested against each other at each boundary.
type StagePhase int16

const (
	// StagePhaseUpcoming: before opens_at, the tasks are hidden and unreachable.
	StagePhaseUpcoming StagePhase = iota
	// StagePhaseOpen: inside the window (also every set without a stage).
	StagePhaseOpen
	// StagePhaseEndedReturnable: after closes_at of a returnable stage: reachable until the event finishes, but
	// no longer rated.
	StagePhaseEndedReturnable
	// StagePhaseClosed: after closes_at of a stage that is not returnable: visible, no submissions, hints or labs.
	StagePhaseClosed
)

// Reachable says a team may submit, unlock hints and use the lab in this phase.
func (p StagePhase) Reachable() bool { return p == StagePhaseOpen || p == StagePhaseEndedReturnable }

// Hidden says the task is not shown to participants at all.
func (p StagePhase) Hidden() bool { return p == StagePhaseUpcoming }

// Rated says a solve in this phase counts for the rating.
func (p StagePhase) Rated() bool { return p == StagePhaseOpen }

// PhaseOf is the phase of a stage window at a moment.
func PhaseOf(opensAt, closesAt time.Time, returnable bool, at time.Time) StagePhase {
	switch {
	case at.Before(opensAt):
		return StagePhaseUpcoming
	case at.Before(closesAt):
		return StagePhaseOpen
	case returnable:
		return StagePhaseEndedReturnable
	default:
		return StagePhaseClosed
	}
}

// StageState is the computed state of a stage itself (never stored).
type StageState string

const (
	StageUpcoming StageState = "upcoming"
	StageOpen     StageState = "open"
	StageClosed   StageState = "closed"
)

// Stage is one time-boxed stage of an event.
type Stage struct {
	LabRetentionMinutes *int32
	ID                  uuid.UUID
	EventID             uuid.UUID
	Name                string
	OpensAt             time.Time
	ClosesAt            time.Time
	Returnable          bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// StagePhaseAt is the phase of a set at a moment: a set without a stage is always open.
func StagePhaseAt(stage *Stage, at time.Time) StagePhase {
	if stage == nil {
		return StagePhaseOpen
	}
	return PhaseOf(stage.OpensAt, stage.ClosesAt, stage.Returnable, at)
}

// State is upcoming before opens_at, open until closes_at, closed after.
func (s Stage) State(now time.Time) StageState {
	switch {
	case now.Before(s.OpensAt):
		return StageUpcoming
	case now.Before(s.ClosesAt):
		return StageOpen
	default:
		return StageClosed
	}
}

// Opened says the stage is open or closed (it has been opened once).
func (s Stage) Opened(now time.Time) bool { return !now.Before(s.OpensAt) }

// NextChangeAt is the nearest boundary of the stage after now, nil when it has none.
func (s Stage) NextChangeAt(now time.Time) *time.Time {
	switch s.State(now) {
	case StageUpcoming:
		at := s.OpensAt
		return &at
	case StageOpen:
		at := s.ClosesAt
		return &at
	default:
		return nil
	}
}

// micro drops what the database cannot store, so an instant read back compares equal to the one written.
func micro(t time.Time) time.Time { return t.Truncate(time.Microsecond) }

func sameInstant(a, b time.Time) bool { return micro(a).Equal(micro(b)) }

func cleanStageName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > stageNameMaxLen {
		return "", ErrEventStageInvalid.Err()
	}
	return name, nil
}

// NewStage builds a candidate stage for a creation request. Its times are placed by PlanCreateStage: the first
// stage takes the event window whatever was asked, and the final times are validated there.
func NewStage(eventID uuid.UUID, name string, opensAt, closesAt time.Time, returnable bool, now time.Time) (Stage, error) {
	name, err := cleanStageName(name)
	if err != nil {
		return Stage{}, err
	}
	opensAt, closesAt = micro(opensAt), micro(closesAt)
	return Stage{ID: uuid.Must(uuid.NewV7()), EventID: eventID, Name: name, OpensAt: opensAt, ClosesAt: closesAt,
		Returnable: returnable, CreatedAt: now, UpdatedAt: now}, nil
}

func sortedStages(stages []Stage) []Stage {
	out := slices.Clone(stages)
	slices.SortFunc(out, func(a, b Stage) int {
		if c := a.OpensAt.Compare(b.OpensAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	return out
}

// ValidateStages checks a complete list of stages against the event window: valid names and windows, no overlap
// (adjacent is fine), unique names, the first stage opening at the event start and the last closing at the event
// finish. A last stage that was already closed early keeps its time, so the finish anchor is not asked of it.
func ValidateStages(stages []Stage, startAt time.Time, finishAt *time.Time, now time.Time) error {
	if len(stages) == 0 {
		return nil
	}
	if finishAt == nil {
		return ErrEventStageNeedsFinish.Err()
	}
	ordered := sortedStages(stages)
	names := make(map[string]struct{}, len(ordered))
	for i, stage := range ordered {
		if _, err := cleanStageName(stage.Name); err != nil || !stage.OpensAt.Before(stage.ClosesAt) {
			return ErrEventStageInvalid.Err()
		}
		key := strings.ToLower(strings.TrimSpace(stage.Name))
		if _, dup := names[key]; dup {
			return ErrEventStageNameExists.Err()
		}
		names[key] = struct{}{}
		if i > 0 && stage.OpensAt.Before(ordered[i-1].ClosesAt) {
			return ErrEventStageOverlap.Err()
		}
		if stage.OpensAt.Before(startAt) || stage.ClosesAt.After(*finishAt) {
			return ErrEventStageAnchor.Err()
		}
	}
	first, last := ordered[0], ordered[len(ordered)-1]
	if !sameInstant(first.OpensAt, startAt) {
		return ErrEventStageAnchor.Err()
	}
	if !sameInstant(last.ClosesAt, *finishAt) && last.ClosesAt.After(now) {
		return ErrEventStageAnchor.Err()
	}
	return nil
}

// StageCreatePlan is the outcome of adding a stage: the stage as stored, and the previous last stage when it had
// to end where the new one opens.
type StageCreatePlan struct {
	Created Stage
	Trimmed *Stage
}

// PlanCreateStage places a new stage. The boundaries are not free input: the first stage of an event is the
// whole event window (it opens at the start and closes at the finish) and a later stage opens where asked but
// closes at the event finish, ending the previous last stage where it opens. A stage placed between the first
// and the last keeps its own times and must fit a gap. A stage cannot open before or with the first one.
func PlanCreateStage(existing []Stage, candidate Stage, startAt time.Time, finishAt *time.Time, now time.Time) (StageCreatePlan, error) {
	if finishAt == nil {
		return StageCreatePlan{}, ErrEventStageNeedsFinish.Err()
	}
	if !finishAt.After(now) {
		return StageCreatePlan{}, ErrEventStageInvalid.Err()
	}
	ordered := sortedStages(existing)
	created := candidate
	var trimmed *Stage
	if len(ordered) == 0 {
		created.OpensAt, created.ClosesAt = micro(startAt), micro(*finishAt)
	} else {
		first, last := ordered[0], ordered[len(ordered)-1]
		if created.OpensAt.Before(now) {
			created.OpensAt = micro(now)
		}
		if !created.OpensAt.After(first.OpensAt) {
			return StageCreatePlan{}, ErrEventStageAnchor.Err()
		}
		if created.OpensAt.After(last.OpensAt) {
			created.ClosesAt = micro(*finishAt)
			if !last.ClosesAt.After(now) {
				return StageCreatePlan{}, ErrEventStageClosedLocked.Err()
			}
			cut := last
			cut.ClosesAt, cut.UpdatedAt = created.OpensAt, now
			trimmed = &cut
		}
	}
	if !created.OpensAt.Before(created.ClosesAt) || !created.ClosesAt.After(now) {
		return StageCreatePlan{}, ErrEventStageInvalid.Err()
	}
	all := make([]Stage, 0, len(ordered)+1)
	for _, stage := range ordered {
		if trimmed != nil && stage.ID == trimmed.ID {
			stage = *trimmed
		}
		all = append(all, stage)
	}
	all = append(all, created)
	if err := ValidateStages(all, startAt, finishAt, now); err != nil {
		return StageCreatePlan{}, err
	}
	return StageCreatePlan{Created: created, Trimmed: trimmed}, nil
}

// StageEdit is a partial update of a stage.
type StageEdit struct {
	Name       *string
	OpensAt    *time.Time
	ClosesAt   *time.Time
	Returnable *bool
	// CloseNow is «Закрити зараз» on an open stage: closes_at becomes now.
	CloseNow bool
}

// Apply returns the stage after the edit, by what its state allows:
//   - upcoming: everything; opens_at may be set to now; the first stage's opens_at and the last stage's closes_at
//     are the event's own and cannot be changed;
//   - open: name, returnable and closes_at (which stays in the future); opens_at is locked;
//   - closed: only the name; times and returnable are locked for good.
//
// Overlap with the neighbours is checked by ValidateStages on the whole list.
func (s Stage) Apply(edit StageEdit, now time.Time, first, last bool, startAt time.Time, finishAt *time.Time) (Stage, error) {
	next := s
	next.UpdatedAt = now
	if edit.Name != nil {
		name, err := cleanStageName(*edit.Name)
		if err != nil {
			return Stage{}, err
		}
		next.Name = name
	}
	changesOpens := edit.OpensAt != nil && !sameInstant(*edit.OpensAt, s.OpensAt)
	changesCloses := edit.ClosesAt != nil && !sameInstant(*edit.ClosesAt, s.ClosesAt)
	changesReturnable := edit.Returnable != nil && *edit.Returnable != s.Returnable
	switch s.State(now) {
	case StageClosed:
		if changesOpens || changesCloses || changesReturnable || edit.CloseNow {
			return Stage{}, ErrEventStageClosedLocked.Err()
		}
		return next, nil
	case StageOpen:
		if changesOpens {
			return Stage{}, ErrEventStageOpenedLocked.Err()
		}
		switch {
		case edit.CloseNow:
			next.ClosesAt = micro(now)
		case changesCloses:
			if !edit.ClosesAt.After(now) {
				return Stage{}, ErrEventStageInvalid.Err()
			}
			if last && (finishAt == nil || !sameInstant(*edit.ClosesAt, *finishAt)) {
				return Stage{}, ErrEventStageAnchor.Err()
			}
			next.ClosesAt = micro(*edit.ClosesAt)
		}
	default:
		if edit.CloseNow {
			return Stage{}, ErrEventStageNotOpen.Err()
		}
		if changesOpens {
			if first && !sameInstant(*edit.OpensAt, startAt) {
				return Stage{}, ErrEventStageAnchor.Err()
			}
			opens := micro(*edit.OpensAt)
			if opens.Before(now) {
				opens = micro(now)
			}
			next.OpensAt = opens
		}
		if changesCloses {
			if last && (finishAt == nil || !sameInstant(*edit.ClosesAt, *finishAt)) {
				return Stage{}, ErrEventStageAnchor.Err()
			}
			next.ClosesAt = micro(*edit.ClosesAt)
		}
	}
	if changesReturnable {
		next.Returnable = *edit.Returnable
	}
	if !next.OpensAt.Before(next.ClosesAt) {
		return Stage{}, ErrEventStageInvalid.Err()
	}
	return next, nil
}

// CheckStageDelete: a stage that has opened is history, and one that still holds sets cannot go.
func CheckStageDelete(stage Stage, sets int, now time.Time) error {
	if stage.Opened(now) || sets > 0 {
		return ErrEventStageNotDeletable.Err()
	}
	return nil
}

// AnchorAfterDelete re-derives the boundary stages after one was removed: the stage that became first opens at the
// event start, the one that became last closes at the event finish. A stage that stops being first or last keeps
// its time. It returns only the stages that changed.
func AnchorAfterDelete(remaining []Stage, startAt time.Time, finishAt *time.Time, now time.Time) []Stage {
	if len(remaining) == 0 || finishAt == nil {
		return nil
	}
	ordered := sortedStages(remaining)
	var changed []Stage
	touch := func(i int, mutate func(*Stage)) {
		stage := ordered[i]
		mutate(&stage)
		stage.UpdatedAt = now
		ordered[i] = stage
		for j := range changed {
			if changed[j].ID == stage.ID {
				changed[j] = stage
				return
			}
		}
		changed = append(changed, stage)
	}
	if first := ordered[0]; !sameInstant(first.OpensAt, startAt) && !first.Opened(now) {
		touch(0, func(s *Stage) { s.OpensAt = micro(startAt) })
	}
	if last := ordered[len(ordered)-1]; !sameInstant(last.ClosesAt, *finishAt) && last.ClosesAt.After(now) {
		touch(len(ordered)-1, func(s *Stage) { s.ClosesAt = micro(*finishAt) })
	}
	return changed
}

// AnchorToLifecycle moves the first stage's opens_at and the last stage's closes_at with an edit of the event
// start and finish. It is refused when the first stage has opened or the event has no scheduled finish, and
// returns the stages that changed. A last stage that was closed early keeps its time.
func AnchorToLifecycle(stages []Stage, startAt time.Time, finishAt *time.Time, now time.Time) ([]Stage, error) {
	if len(stages) == 0 {
		return nil, nil
	}
	if finishAt == nil {
		return nil, ErrEventStageNeedsFinish.Err()
	}
	ordered := sortedStages(stages)
	var changed []Stage
	set := func(i int, mutate func(*Stage)) {
		stage := ordered[i]
		mutate(&stage)
		stage.UpdatedAt = now
		ordered[i] = stage
		for j := range changed {
			if changed[j].ID == stage.ID {
				changed[j] = stage
				return
			}
		}
		changed = append(changed, stage)
	}
	if first := ordered[0]; !sameInstant(first.OpensAt, startAt) {
		if first.Opened(now) {
			return nil, ErrEventStageOpenedLocked.Err()
		}
		set(0, func(s *Stage) { s.OpensAt = micro(startAt) })
	}
	if last := ordered[len(ordered)-1]; !sameInstant(last.ClosesAt, *finishAt) && last.ClosesAt.After(now) {
		set(len(ordered)-1, func(s *Stage) { s.ClosesAt = micro(*finishAt) })
	}
	if err := ValidateStages(ordered, startAt, finishAt, now); err != nil {
		return nil, err
	}
	return changed, nil
}

// CheckSetMove is the rule for moving an exercise set between stages (nil is «Весь захід»): nothing leaves a stage
// that has opened, and a closed stage accepts nothing new. Before a stage opens everything is free.
func CheckSetMove(from, to *Stage, now time.Time) error {
	if from != nil && from.Opened(now) {
		return ErrEventStageOpenedLocked.Err()
	}
	if to != nil && to.State(now) == StageClosed {
		return ErrEventStageClosedLocked.Err()
	}
	return nil
}

// CheckSetChange is the rule for changing the tasks of a set of a stage: a task cannot be removed once the stage
// has opened and cannot be added once it has closed.
func CheckSetChange(stage *Stage, adds, removes bool, now time.Time) error {
	if stage == nil {
		return nil
	}
	if removes && stage.Opened(now) {
		return ErrEventStageOpenedLocked.Err()
	}
	if adds && stage.State(now) == StageClosed {
		return ErrEventStageClosedLocked.Err()
	}
	return nil
}

func (s *Stage) SetLabRetentionMinutes(minutes *int32, now time.Time) error {
	if minutes != nil && (*minutes < 0 || *minutes > 10080) {
		return ErrEventStageInvalid.Err()
	}
	if minutes == nil {
		s.LabRetentionMinutes = nil
	} else {
		n := *minutes
		s.LabRetentionMinutes = &n
	}
	s.UpdatedAt = now
	return nil
}
