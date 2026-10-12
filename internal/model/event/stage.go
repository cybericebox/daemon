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
	OpensWithEvent      bool
	ClosesWithEvent     bool
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

// StageDates selects each boundary independently. A bound boundary uses the scheduled event date.
type StageDates struct {
	OpensAt, ClosesAt               time.Time
	OpensWithEvent, ClosesWithEvent bool
}

func resolveStageDates(dates StageDates, startAt time.Time, finishAt *time.Time) (StageDates, error) {
	if dates.OpensWithEvent {
		dates.OpensAt = startAt
	}
	if dates.ClosesWithEvent {
		if finishAt == nil {
			return StageDates{}, ErrEventStageBoundFinishMissing.Err()
		}
		dates.ClosesAt = *finishAt
	}
	dates.OpensAt, dates.ClosesAt = micro(dates.OpensAt), micro(dates.ClosesAt)
	return dates, nil
}

// NewStage resolves explicitly selected sources, then builds the stage without changing its neighbors.
func NewStage(eventID uuid.UUID, name string, dates StageDates, returnable bool, startAt time.Time, finishAt *time.Time, now time.Time) (Stage, error) {
	name, err := cleanStageName(name)
	if err != nil {
		return Stage{}, err
	}
	dates, err = resolveStageDates(dates, startAt, finishAt)
	if err != nil {
		return Stage{}, err
	}
	if dates.OpensAt.IsZero() || dates.ClosesAt.IsZero() || !dates.OpensAt.Before(dates.ClosesAt) {
		return Stage{}, ErrEventStageInvalid.Err()
	}
	return Stage{ID: uuid.Must(uuid.NewV7()), EventID: eventID, Name: name, OpensAt: dates.OpensAt, ClosesAt: dates.ClosesAt,
		OpensWithEvent: dates.OpensWithEvent, ClosesWithEvent: dates.ClosesWithEvent, Returnable: returnable, CreatedAt: now, UpdatedAt: now}, nil
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

// ValidateStages checks valid, distinct names and non-overlapping windows within the event's configured bounds.
// A custom closing needs no event finish, but every window remains inside platform availability.
func ValidateStages(stages []Stage, startAt time.Time, finishAt *time.Time, archiveAt time.Time) error {
	ordered := sortedStages(stages)
	names := make(map[string]struct{}, len(ordered))
	for i, stage := range ordered {
		if _, err := cleanStageName(stage.Name); err != nil || stage.OpensAt.IsZero() || stage.ClosesAt.IsZero() || !stage.OpensAt.Before(stage.ClosesAt) {
			return ErrEventStageInvalid.Err()
		}
		key := strings.ToLower(strings.TrimSpace(stage.Name))
		if _, duplicate := names[key]; duplicate {
			return ErrEventStageNameExists.Err()
		}
		names[key] = struct{}{}
		if i > 0 && stage.OpensAt.Before(ordered[i-1].ClosesAt) {
			return ErrEventStageOverlap.Err()
		}
		if stage.OpensAt.Before(startAt) || finishAt != nil && stage.ClosesAt.After(*finishAt) || !archiveAt.IsZero() && stage.ClosesAt.After(archiveAt) {
			return ErrEventStageOutsideEvent.Err()
		}
	}
	return nil
}

// StageCreatePlan contains the new stage, retaining its requested window.
type StageCreatePlan struct {
	Created Stage
}

// PlanCreateStage checks a new stage without changing the event or any other stage.
func PlanCreateStage(existing []Stage, candidate Stage, startAt time.Time, finishAt *time.Time, archiveAt, now time.Time) (StageCreatePlan, error) {
	if !candidate.OpensAt.Before(candidate.ClosesAt) || !candidate.ClosesAt.After(now) {
		return StageCreatePlan{}, ErrEventStageInvalid.Err()
	}
	all := append(slices.Clone(existing), candidate)
	if err := ValidateStages(all, startAt, finishAt, archiveAt); err != nil {
		return StageCreatePlan{}, err
	}
	return StageCreatePlan{Created: candidate}, nil
}

// StageEdit is a partial update of a stage.
type StageEdit struct {
	Name            *string
	OpensAt         *time.Time
	ClosesAt        *time.Time
	Returnable      *bool
	OpensWithEvent  *bool
	ClosesWithEvent *bool
	// CloseNow is «Закрити зараз» on an open stage: closes_at becomes now.
	CloseNow bool
}

// Apply returns the stage after the edit, by what its state allows:
//   - upcoming: everything; a changed opens_at must be now or in the future;
//   - open: name, returnable and closes_at (which stays in the future); opens_at is locked;
//   - closed: only the name; times and returnable are locked for good.
//
// Overlap with the neighbours is checked by ValidateStages on the whole list.
func (s Stage) Apply(edit StageEdit, now time.Time, startAt time.Time, finishAt *time.Time) (Stage, error) {
	next := s
	next.UpdatedAt = now
	if edit.Name != nil {
		name, err := cleanStageName(*edit.Name)
		if err != nil {
			return Stage{}, err
		}
		next.Name = name
	}
	dates := StageDates{OpensAt: s.OpensAt, ClosesAt: s.ClosesAt, OpensWithEvent: s.OpensWithEvent, ClosesWithEvent: s.ClosesWithEvent}
	if edit.OpensAt != nil {
		dates.OpensAt = *edit.OpensAt
	}
	if edit.ClosesAt != nil {
		dates.ClosesAt = *edit.ClosesAt
	}
	if edit.OpensWithEvent != nil {
		dates.OpensWithEvent = *edit.OpensWithEvent
	}
	if edit.ClosesWithEvent != nil {
		dates.ClosesWithEvent = *edit.ClosesWithEvent
	}
	state := s.State(now)
	changesOpenSource := dates.OpensWithEvent != s.OpensWithEvent
	changesCloseSource := dates.ClosesWithEvent != s.ClosesWithEvent
	if state == StageClosed && (changesOpenSource || changesCloseSource) {
		return Stage{}, ErrEventStageClosedLocked.Err()
	}
	if state != StageUpcoming && changesOpenSource {
		return Stage{}, ErrEventStageOpenedLocked.Err()
	}
	// Resolve only requested boundaries. Unrelated writes never revisit historical scheduled sources.
	requested := dates
	requested.OpensWithEvent = state == StageUpcoming && dates.OpensWithEvent && (edit.OpensAt != nil || edit.OpensWithEvent != nil)
	requested.ClosesWithEvent = state != StageClosed && !edit.CloseNow && dates.ClosesWithEvent && (edit.ClosesAt != nil || edit.ClosesWithEvent != nil)
	resolved, err := resolveStageDates(requested, startAt, finishAt)
	if err != nil {
		return Stage{}, err
	}
	dates.OpensAt, dates.ClosesAt = resolved.OpensAt, resolved.ClosesAt
	changesOpens := !sameInstant(dates.OpensAt, s.OpensAt)
	changesCloses := !sameInstant(dates.ClosesAt, s.ClosesAt)
	changesReturnable := edit.Returnable != nil && *edit.Returnable != s.Returnable
	switch s.State(now) {
	case StageClosed:
		if changesOpens || changesCloses || changesOpenSource || changesCloseSource || changesReturnable || edit.CloseNow {
			return Stage{}, ErrEventStageClosedLocked.Err()
		}
		return next, nil
	case StageOpen:
		if changesOpens || changesOpenSource {
			return Stage{}, ErrEventStageOpenedLocked.Err()
		}
		switch {
		case edit.CloseNow:
			next.ClosesAt, dates.ClosesWithEvent = micro(now), false
		case changesCloses:
			if !dates.ClosesAt.After(micro(now)) {
				return Stage{}, ErrEventStageInvalid.Err()
			}
			next.ClosesAt = dates.ClosesAt
		}
	default:
		if edit.CloseNow {
			return Stage{}, ErrEventStageNotOpen.Err()
		}
		if changesOpens {
			opens := dates.OpensAt
			if opens.Before(micro(now)) {
				return Stage{}, ErrEventStageInvalid.Err()
			}
			next.OpensAt = opens
		}
		if changesCloses {
			next.ClosesAt = dates.ClosesAt
		}
	}
	next.OpensWithEvent, next.ClosesWithEvent = dates.OpensWithEvent, dates.ClosesWithEvent
	if changesReturnable {
		next.Returnable = *edit.Returnable
	}
	if !next.OpensAt.Before(next.ClosesAt) {
		return Stage{}, ErrEventStageInvalid.Err()
	}
	return next, nil
}

// PlanStageLifecycle follows selected scheduled sources prospectively. Actual boundaries that have
// already occurred remain immutable; custom dates and sources never depend on position or equality.
func PlanStageLifecycle(stages []Stage, startAt time.Time, finishAt *time.Time, archiveAt, now time.Time) ([]Stage, error) {
	next := slices.Clone(stages)
	changed := []Stage{}
	yes := true
	for i, stage := range stages {
		edit := StageEdit{}
		if stage.OpensWithEvent && now.Before(stage.OpensAt) && !sameInstant(stage.OpensAt, startAt) {
			edit.OpensWithEvent = &yes
		}
		if stage.ClosesWithEvent && now.Before(stage.ClosesAt) && (finishAt == nil || !sameInstant(stage.ClosesAt, *finishAt)) {
			edit.ClosesWithEvent = &yes
		}
		if edit.OpensWithEvent == nil && edit.ClosesWithEvent == nil {
			continue
		}
		updated, err := stage.Apply(edit, now, startAt, finishAt)
		if err != nil {
			return nil, err
		}
		next[i] = updated
		changed = append(changed, updated)
	}
	if err := ValidateStages(next, startAt, finishAt, archiveAt); err != nil {
		return nil, err
	}
	return changed, nil
}

// CheckStageDelete: a stage that has opened is history, and one that still holds sets cannot go.
func CheckStageDelete(stage Stage, sets int, now time.Time) error {
	if stage.Opened(now) || sets > 0 {
		return ErrEventStageNotDeletable.Err()
	}
	return nil
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
