package eventModel_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

var (
	stT0      = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	stEventID = uuid.Must(uuid.NewV7())
)

func at(h int) time.Time { return stT0.Add(time.Duration(h) * time.Hour) }

func stage(name string, open, close int, returnable bool) eventModel.Stage {
	s, err := eventModel.NewStage(stEventID, name, eventModel.StageDates{OpensAt: at(open), ClosesAt: at(close)}, returnable, at(0), nil, stT0)
	if err != nil {
		panic(err)
	}
	return s
}

func stageFinish() *time.Time { v := at(20); return &v }

func is(err error, want error) bool { return errors.Is(err, want) }

func TestPhaseOfAtEachBoundary(t *testing.T) {
	opens, closes := at(2), at(4)
	cases := []struct {
		name       string
		now        time.Time
		returnable bool
		want       eventModel.StagePhase
	}{
		{"before opens", opens.Add(-time.Nanosecond), false, eventModel.StagePhaseUpcoming},
		{"at opens", opens, false, eventModel.StagePhaseOpen},
		{"inside", at(3), true, eventModel.StagePhaseOpen},
		{"just before closes", closes.Add(-time.Nanosecond), false, eventModel.StagePhaseOpen},
		{"at closes, not returnable", closes, false, eventModel.StagePhaseClosed},
		{"at closes, returnable", closes, true, eventModel.StagePhaseEndedReturnable},
		{"long after, not returnable", at(40), false, eventModel.StagePhaseClosed},
		{"long after, returnable", at(40), true, eventModel.StagePhaseEndedReturnable},
	}
	for _, c := range cases {
		if got := eventModel.PhaseOf(opens, closes, c.returnable, c.now); got != c.want {
			t.Errorf("%s: phase = %d, want %d", c.name, got, c.want)
		}
	}
	if eventModel.StagePhaseAt(nil, at(1)) != eventModel.StagePhaseOpen {
		t.Fatal("a set without a stage is always open")
	}
	if !eventModel.StagePhaseOpen.Reachable() || !eventModel.StagePhaseEndedReturnable.Reachable() ||
		eventModel.StagePhaseClosed.Reachable() || eventModel.StagePhaseUpcoming.Reachable() {
		t.Fatal("only open and ended-returnable phases are reachable")
	}
	if eventModel.StagePhaseEndedReturnable.Rated() || !eventModel.StagePhaseOpen.Rated() {
		t.Fatal("only the open phase is rated")
	}
}

func TestStageState(t *testing.T) {
	s := stage("A", 2, 4, false)
	for now, want := range map[time.Time]eventModel.StageState{at(1): eventModel.StageUpcoming, at(2): eventModel.StageOpen, at(3): eventModel.StageOpen, at(4): eventModel.StageClosed} {
		if got := s.State(now); got != want {
			t.Errorf("State(%v) = %s, want %s", now, got, want)
		}
	}
	if next := s.NextChangeAt(at(1)); next == nil || !next.Equal(at(2)) {
		t.Fatalf("upcoming next change = %v", next)
	}
	if next := s.NextChangeAt(at(3)); next == nil || !next.Equal(at(4)) {
		t.Fatalf("open next change = %v", next)
	}
	if s.NextChangeAt(at(5)) != nil {
		t.Fatal("a closed stage has no next change")
	}
}

func TestValidateStagesOverlapAdjacentAndWindow(t *testing.T) {
	finish := at(10)
	a, b := stage("A", 0, 4, false), stage("B", 4, 10, false) // adjacent
	if err := eventModel.ValidateStages([]eventModel.Stage{b, a}, at(0), &finish, time.Time{}); err != nil {
		t.Fatalf("adjacent stages refused: %v", err)
	}
	overlap := stage("B", 3, 10, false)
	if err := eventModel.ValidateStages([]eventModel.Stage{a, overlap}, at(0), &finish, time.Time{}); !is(err, eventModel.ErrEventStageOverlap.Err()) {
		t.Fatalf("overlap: %v", err)
	}
	gap := stage("B", 6, 10, false) // a break is fine
	if err := eventModel.ValidateStages([]eventModel.Stage{a, gap}, at(0), &finish, time.Time{}); err != nil {
		t.Fatalf("a break refused: %v", err)
	}
	outside := stage("B", 6, 11, false)
	if err := eventModel.ValidateStages([]eventModel.Stage{a, outside}, at(0), &finish, time.Time{}); !is(err, eventModel.ErrEventStageOutsideEvent.Err()) {
		t.Fatalf("stage past the finish: %v", err)
	}
	if err := eventModel.ValidateStages([]eventModel.Stage{a, stage("b", 5, 10, false), stage("B ", 5, 10, false)}, at(0), &finish, time.Time{}); err == nil {
		t.Fatal("duplicate names accepted")
	}
	if err := eventModel.ValidateStages([]eventModel.Stage{a}, at(0), nil, time.Time{}); err != nil {
		t.Fatalf("a finish is optional: %v", err)
	}
	early := stage("A", 1, 10, false)
	if err := eventModel.ValidateStages([]eventModel.Stage{early}, at(0), &finish, time.Time{}); err != nil {
		t.Fatalf("the first stage may open after the event: %v", err)
	}
	short := stage("A", 0, 9, false)
	if err := eventModel.ValidateStages([]eventModel.Stage{short}, at(0), &finish, time.Time{}); err != nil {
		t.Fatalf("the last stage may close before the event: %v", err)
	}
	// a last stage already closed early keeps its time
	if err := eventModel.ValidateStages([]eventModel.Stage{short}, at(0), &finish, time.Time{}); err != nil {
		t.Fatalf("a last stage closed early: %v", err)
	}
}

func TestPlanCreateStageKeepsEveryExplicitWindowWithoutChangingNeighbors(t *testing.T) {
	finish, now := at(10), at(-1)
	candidate, _ := eventModel.NewStage(stEventID, "One", eventModel.StageDates{OpensAt: at(5), ClosesAt: at(6)}, true, at(0), nil, now)
	plan, err := eventModel.PlanCreateStage(nil, candidate, at(0), &finish, time.Time{}, now)
	if err != nil || !plan.Created.OpensAt.Equal(at(5)) || !plan.Created.ClosesAt.Equal(at(6)) {
		t.Fatalf("first explicit window: %+v err=%v", plan, err)
	}
	for _, times := range [][2]int{{7, 8}, {1, 2}, {3, 4}} {
		next, _ := eventModel.NewStage(stEventID, "Next", eventModel.StageDates{OpensAt: at(times[0]), ClosesAt: at(times[1])}, false, at(0), nil, now)
		got, err := eventModel.PlanCreateStage([]eventModel.Stage{plan.Created}, next, at(0), nil, time.Time{}, now)
		if err != nil || !got.Created.OpensAt.Equal(next.OpensAt) || !got.Created.ClosesAt.Equal(next.ClosesAt) {
			t.Fatalf("independent stage: %+v err=%v", got, err)
		}
	}
	clash, _ := eventModel.NewStage(stEventID, "Clash", eventModel.StageDates{OpensAt: at(4), ClosesAt: at(7)}, false, at(0), nil, now)
	if _, err := eventModel.PlanCreateStage([]eventModel.Stage{plan.Created}, clash, at(0), &finish, time.Time{}, now); !is(err, eventModel.ErrEventStageOverlap.Err()) {
		t.Fatalf("overlap must be refused, never repaired by trimming: %v", err)
	}
}

// the edit matrix: what each state of a stage allows
func TestStageApplyMatrix(t *testing.T) {
	str := func(v string) *string { return &v }
	tm := func(h int) *time.Time { v := at(h); return &v }
	yes := func(v bool) *bool { return &v }
	mid := func(open, close int) eventModel.Stage { return stage("Mid", open, close, false) }

	t.Run("upcoming allows everything", func(t *testing.T) {
		s := mid(5, 8)
		got, err := s.Apply(eventModel.StageEdit{Name: str("New"), OpensAt: tm(6), ClosesAt: tm(9), Returnable: yes(true)}, at(1), at(2), stageFinish())
		if err != nil || got.Name != "New" || !got.OpensAt.Equal(at(6)) || !got.ClosesAt.Equal(at(9)) || !got.Returnable {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})
	t.Run("upcoming opening cannot be silently backdated", func(t *testing.T) {
		s := mid(5, 8)
		got, err := s.Apply(eventModel.StageEdit{OpensAt: tm(0)}, at(2), at(2), stageFinish())
		if !is(err, eventModel.ErrEventStageInvalid.Err()) {
			t.Fatalf("past opening must be refused: %+v err=%v", got, err)
		}
		got, err = s.Apply(eventModel.StageEdit{OpensAt: tm(2)}, at(2), at(2), stageFinish())
		if err != nil || !got.OpensAt.Equal(at(2)) {
			t.Fatalf("explicit now must be allowed: %+v err=%v", got, err)
		}
	})
	t.Run("upcoming cannot be closed now", func(t *testing.T) {
		if _, err := mid(5, 8).Apply(eventModel.StageEdit{CloseNow: true}, at(1), at(2), stageFinish()); !is(err, eventModel.ErrEventStageNotOpen.Err()) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("first and last stage dates remain independent", func(t *testing.T) {
		first := stage("F", 0, 5, false)
		if got, err := first.Apply(eventModel.StageEdit{OpensAt: tm(1)}, at(-1), at(2), stageFinish()); err != nil || !got.OpensAt.Equal(at(1)) {
			t.Fatalf("first opens_at: %v", err)
		}
		last := stage("L", 15, 20, false)
		if got, err := last.Apply(eventModel.StageEdit{ClosesAt: tm(19)}, at(1), at(2), stageFinish()); err != nil || !got.ClosesAt.Equal(at(19)) {
			t.Fatalf("last closes_at: %v", err)
		}
	})
	t.Run("open: opens_at locked, rest editable", func(t *testing.T) {
		s := mid(5, 8)
		now := at(6)
		if _, err := s.Apply(eventModel.StageEdit{OpensAt: tm(4)}, now, at(2), stageFinish()); !is(err, eventModel.ErrEventStageOpenedLocked.Err()) {
			t.Fatalf("opens_at: %v", err)
		}
		got, err := s.Apply(eventModel.StageEdit{Name: str("X"), ClosesAt: tm(9), Returnable: yes(true)}, now, at(2), stageFinish())
		if err != nil || got.Name != "X" || !got.ClosesAt.Equal(at(9)) || !got.Returnable {
			t.Fatalf("got %+v err=%v", got, err)
		}
		if _, err = s.Apply(eventModel.StageEdit{ClosesAt: tm(6)}, now, at(2), stageFinish()); !is(err, eventModel.ErrEventStageInvalid.Err()) {
			t.Fatalf("closes_at must stay in the future: %v", err)
		}
	})
	t.Run("open: close now", func(t *testing.T) {
		s := mid(5, 8)
		got, err := s.Apply(eventModel.StageEdit{CloseNow: true}, at(6), at(2), stageFinish())
		if err != nil || !got.ClosesAt.Equal(at(6)) || got.State(at(6)) != eventModel.StageClosed {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})
	t.Run("closed: only the name", func(t *testing.T) {
		s := mid(5, 8)
		now := at(9)
		got, err := s.Apply(eventModel.StageEdit{Name: str("Renamed")}, now, at(2), stageFinish())
		if err != nil || got.Name != "Renamed" {
			t.Fatalf("name: %+v err=%v", got, err)
		}
		for name, edit := range map[string]eventModel.StageEdit{
			"opens_at": {OpensAt: tm(4)}, "closes_at": {ClosesAt: tm(12)}, "returnable": {Returnable: yes(true)}, "close now": {CloseNow: true},
		} {
			if _, err = s.Apply(edit, now, at(2), stageFinish()); !is(err, eventModel.ErrEventStageClosedLocked.Err()) {
				t.Errorf("%s of a closed stage: %v", name, err)
			}
		}
		// an unchanged value is not a change
		if _, err = s.Apply(eventModel.StageEdit{OpensAt: tm(5), Returnable: yes(false)}, now, at(2), stageFinish()); err != nil {
			t.Errorf("unchanged values refused: %v", err)
		}
	})
}

func TestSetMoveAndChangeRulesPerStageState(t *testing.T) {
	upcoming, open, closed := stage("U", 8, 9, false), stage("O", 2, 6, false), stage("C", 0, 1, false)
	now := at(3)
	// moving into stages
	if err := eventModel.CheckSetMove(nil, &upcoming, now); err != nil {
		t.Fatalf("into upcoming: %v", err)
	}
	if err := eventModel.CheckSetMove(nil, &open, now); err != nil {
		t.Fatalf("add a set to an open stage must be allowed: %v", err)
	}
	if err := eventModel.CheckSetMove(nil, &closed, now); !is(err, eventModel.ErrEventStageClosedLocked.Err()) {
		t.Fatalf("a closed stage accepts nothing: %v", err)
	}
	// moving out
	if err := eventModel.CheckSetMove(&upcoming, nil, now); err != nil {
		t.Fatalf("out of upcoming: %v", err)
	}
	other := stage("U2", 10, 11, false)
	if err := eventModel.CheckSetMove(&upcoming, &other, now); err != nil {
		t.Fatalf("between upcoming stages: %v", err)
	}
	if err := eventModel.CheckSetMove(&open, nil, now); !is(err, eventModel.ErrEventStageOpenedLocked.Err()) {
		t.Fatalf("out of an open stage: %v", err)
	}
	if err := eventModel.CheckSetMove(&open, &upcoming, now); !is(err, eventModel.ErrEventStageOpenedLocked.Err()) {
		t.Fatalf("open -> anywhere: %v", err)
	}
	if err := eventModel.CheckSetMove(&closed, nil, now); !is(err, eventModel.ErrEventStageOpenedLocked.Err()) {
		t.Fatalf("out of a closed stage: %v", err)
	}
	// tasks of a set
	if err := eventModel.CheckSetChange(&open, true, false, now); err != nil {
		t.Fatalf("add a task to an open stage: %v", err)
	}
	if err := eventModel.CheckSetChange(&open, false, true, now); !is(err, eventModel.ErrEventStageOpenedLocked.Err()) {
		t.Fatalf("remove a task from an opened stage's set: %v", err)
	}
	if err := eventModel.CheckSetChange(&closed, true, false, now); !is(err, eventModel.ErrEventStageClosedLocked.Err()) {
		t.Fatalf("add a task to a closed stage: %v", err)
	}
	if err := eventModel.CheckSetChange(&upcoming, true, true, now); err != nil {
		t.Fatalf("an upcoming stage is free: %v", err)
	}
	if err := eventModel.CheckSetChange(nil, true, true, now); err != nil {
		t.Fatalf("no stage: %v", err)
	}
}

func TestStageDeleteRequiresUpcomingAndEmpty(t *testing.T) {
	now := at(1)
	if err := eventModel.CheckStageDelete(stage("U", 5, 6, false), 0, now); err != nil {
		t.Fatalf("empty upcoming: %v", err)
	}
	if err := eventModel.CheckStageDelete(stage("U", 5, 6, false), 1, now); !is(err, eventModel.ErrEventStageNotDeletable.Err()) {
		t.Fatalf("with sets: %v", err)
	}
	if err := eventModel.CheckStageDelete(stage("O", 0, 6, false), 0, now); !is(err, eventModel.ErrEventStageNotDeletable.Err()) {
		t.Fatalf("an opened stage is never deletable: %v", err)
	}
}

func TestNewStageRequiresBothExplicitDatesAndPositiveWindow(t *testing.T) {
	for _, bounds := range [][2]time.Time{{{}, at(2)}, {at(1), {}}, {at(2), at(2)}, {at(3), at(2)}} {
		if _, err := eventModel.NewStage(stEventID, "Stage", eventModel.StageDates{OpensAt: bounds[0], ClosesAt: bounds[1]}, false, at(0), nil, at(0)); !is(err, eventModel.ErrEventStageInvalid.Err()) {
			t.Fatalf("invalid explicit bounds %v accepted: %v", bounds, err)
		}
	}
}

func TestStageSourceChangesRespectActualBoundaryLocks(t *testing.T) {
	yes, no := true, false
	upcoming := stage("Upcoming", 5, 8, false)
	got, err := upcoming.Apply(eventModel.StageEdit{OpensWithEvent: &yes, ClosesWithEvent: &yes}, at(1), at(2), stageFinish())
	if err != nil || !got.OpensWithEvent || !got.ClosesWithEvent {
		t.Fatalf("source selections were not persisted: %+v err=%v", got, err)
	}
	opened := upcoming
	opened.OpensWithEvent = true
	if _, err = opened.Apply(eventModel.StageEdit{OpensWithEvent: &no}, at(6), at(2), stageFinish()); !is(err, eventModel.ErrEventStageOpenedLocked.Err()) {
		t.Fatalf("opening source must lock with actual opening: %v", err)
	}
	closed := upcoming
	closed.ClosesWithEvent = true
	if _, err = closed.Apply(eventModel.StageEdit{ClosesWithEvent: &no}, at(9), at(2), stageFinish()); !is(err, eventModel.ErrEventStageClosedLocked.Err()) {
		t.Fatalf("closing source must lock with actual closing: %v", err)
	}
	open := upcoming
	open.ClosesWithEvent = true
	got, err = open.Apply(eventModel.StageEdit{CloseNow: true}, at(6), at(2), stageFinish())
	if err != nil || got.ClosesWithEvent || !got.ClosesAt.Equal(at(6)) {
		t.Fatalf("close-now must record an independent actual closing: %+v err=%v", got, err)
	}
}

func TestPlanStageLifecycleMovesOnlyChosenFutureSources(t *testing.T) {
	bound := stage("Bound", 5, 8, false)
	bound.OpensWithEvent, bound.ClosesWithEvent = true, true
	finish := at(20)
	changed, err := eventModel.PlanStageLifecycle([]eventModel.Stage{bound}, at(6), &finish, time.Time{}, at(1))
	if err != nil || len(changed) != 1 || !changed[0].OpensAt.Equal(at(6)) || !changed[0].ClosesAt.Equal(finish) {
		t.Fatalf("chosen future sources: %+v err=%v", changed, err)
	}
	if _, err = eventModel.PlanStageLifecycle([]eventModel.Stage{bound}, at(6), nil, time.Time{}, at(1)); !is(err, eventModel.ErrEventStageBoundFinishMissing.Err()) {
		t.Fatalf("missing bound finish must be explicit: %v", err)
	}
	closed := stage("Actual", 0, 5, false)
	closed.OpensWithEvent, closed.ClosesWithEvent = true, true
	if changed, err = eventModel.PlanStageLifecycle([]eventModel.Stage{closed}, at(-1), &finish, time.Time{}, at(6)); err != nil || len(changed) != 0 {
		t.Fatalf("historical actual dates changed: %+v err=%v", changed, err)
	}
	custom := stage("Custom", 0, 8, false)
	if changed, err = eventModel.PlanStageLifecycle([]eventModel.Stage{custom}, at(-1), &finish, time.Time{}, at(-2)); err != nil || len(changed) != 0 {
		t.Fatalf("matching timestamps must not invent a source: %+v err=%v", changed, err)
	}
	no := false
	if _, err = closed.Apply(eventModel.StageEdit{ClosesWithEvent: &no}, at(6), at(0), nil); !is(err, eventModel.ErrEventStageClosedLocked.Err()) {
		t.Fatalf("closed lock must dominate source validation: %v", err)
	}
	yes := true
	if got, err := closed.Apply(eventModel.StageEdit{ClosesWithEvent: &yes}, at(6), at(0), nil); err != nil || !got.ClosesAt.Equal(closed.ClosesAt) {
		t.Fatalf("unchanged historical source should remain readable: %+v err=%v", got, err)
	}
}

func TestStagePlatformAvailabilityBoundIsIndependentOfFinishSource(t *testing.T) {
	archive := at(10)
	inside, beyond := stage("Inside", 2, 10, false), stage("Beyond", 2, 11, false)
	if err := eventModel.ValidateStages([]eventModel.Stage{inside}, at(0), nil, archive); err != nil {
		t.Fatalf("half-open stage may close exactly at archive: %v", err)
	}
	if err := eventModel.ValidateStages([]eventModel.Stage{beyond}, at(0), nil, archive); !is(err, eventModel.ErrEventStageOutsideEvent.Err()) {
		t.Fatalf("custom closing after archive must fail: %v", err)
	}
	if err := eventModel.ValidateStages([]eventModel.Stage{beyond}, at(0), nil, time.Time{}); err != nil {
		t.Fatalf("without either finish or archive a custom stage remains valid: %v", err)
	}
	candidate, err := eventModel.NewStage(stEventID, "Bound finish", eventModel.StageDates{OpensAt: at(2), ClosesWithEvent: true}, false, at(0), nil, at(0))
	if !is(err, eventModel.ErrEventStageBoundFinishMissing.Err()) {
		t.Fatalf("archive must not substitute for source event finish: %+v err=%v", candidate, err)
	}
}
