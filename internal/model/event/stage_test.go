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
	s, err := eventModel.NewStage(stEventID, name, at(open), at(close), returnable, stT0)
	if err != nil {
		panic(err)
	}
	return s
}

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
	if err := eventModel.ValidateStages([]eventModel.Stage{b, a}, at(0), &finish, stT0); err != nil {
		t.Fatalf("adjacent stages refused: %v", err)
	}
	overlap := stage("B", 3, 10, false)
	if err := eventModel.ValidateStages([]eventModel.Stage{a, overlap}, at(0), &finish, stT0); !is(err, eventModel.ErrEventStageOverlap.Err()) {
		t.Fatalf("overlap: %v", err)
	}
	gap := stage("B", 6, 10, false) // a break is fine
	if err := eventModel.ValidateStages([]eventModel.Stage{a, gap}, at(0), &finish, stT0); err != nil {
		t.Fatalf("a break refused: %v", err)
	}
	outside := stage("B", 6, 11, false)
	if err := eventModel.ValidateStages([]eventModel.Stage{a, outside}, at(0), &finish, stT0); !is(err, eventModel.ErrEventStageAnchor.Err()) {
		t.Fatalf("stage past the finish: %v", err)
	}
	if err := eventModel.ValidateStages([]eventModel.Stage{a, stage("b", 5, 10, false), stage("B ", 5, 10, false)}, at(0), &finish, stT0); err == nil {
		t.Fatal("duplicate names accepted")
	}
	if err := eventModel.ValidateStages([]eventModel.Stage{a}, at(0), nil, stT0); !is(err, eventModel.ErrEventStageNeedsFinish.Err()) {
		t.Fatalf("stages need a scheduled finish: %v", err)
	}
	early := stage("A", 1, 10, false)
	if err := eventModel.ValidateStages([]eventModel.Stage{early}, at(0), &finish, stT0); !is(err, eventModel.ErrEventStageAnchor.Err()) {
		t.Fatalf("the first stage must open with the event: %v", err)
	}
	short := stage("A", 0, 9, false)
	if err := eventModel.ValidateStages([]eventModel.Stage{short}, at(0), &finish, stT0); !is(err, eventModel.ErrEventStageAnchor.Err()) {
		t.Fatalf("the last stage must close with the event: %v", err)
	}
	// a last stage already closed early keeps its time
	if err := eventModel.ValidateStages([]eventModel.Stage{short}, at(0), &finish, at(9)); err != nil {
		t.Fatalf("a last stage closed early: %v", err)
	}
}

func TestPlanCreateStageFirstIsTheWholeEventAndNextTrimsTheLast(t *testing.T) {
	finish := at(10)
	now := at(-1)
	cand, _ := eventModel.NewStage(stEventID, "One", at(5), at(6), true, now)
	plan, err := eventModel.PlanCreateStage(nil, cand, at(0), &finish, now)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Created.OpensAt.Equal(at(0)) || !plan.Created.ClosesAt.Equal(finish) || plan.Trimmed != nil {
		t.Fatalf("first stage is the event window: %+v", plan)
	}
	second, _ := eventModel.NewStage(stEventID, "Two", at(6), at(7), false, now)
	plan2, err := eventModel.PlanCreateStage([]eventModel.Stage{plan.Created}, second, at(0), &finish, now)
	if err != nil {
		t.Fatal(err)
	}
	if !plan2.Created.OpensAt.Equal(at(6)) || !plan2.Created.ClosesAt.Equal(finish) {
		t.Fatalf("the new last stage closes with the event: %+v", plan2.Created)
	}
	if plan2.Trimmed == nil || plan2.Trimmed.ID != plan.Created.ID || !plan2.Trimmed.ClosesAt.Equal(at(6)) {
		t.Fatalf("the previous last stage must end where the new one opens: %+v", plan2.Trimmed)
	}
	before, _ := eventModel.NewStage(stEventID, "Zero", at(0), at(1), false, now)
	if _, err = eventModel.PlanCreateStage([]eventModel.Stage{plan.Created}, before, at(0), &finish, now); !is(err, eventModel.ErrEventStageAnchor.Err()) {
		t.Fatalf("a stage cannot open with or before the first: %v", err)
	}
	if _, err = eventModel.PlanCreateStage(nil, cand, at(0), nil, now); !is(err, eventModel.ErrEventStageNeedsFinish.Err()) {
		t.Fatalf("no finish: %v", err)
	}
	// a middle stage keeps its own times and must fit a gap
	a, b := stage("A", 0, 3, false), stage("B", 7, 10, false)
	mid, _ := eventModel.NewStage(stEventID, "Mid", at(4), at(5), false, now)
	plan3, err := eventModel.PlanCreateStage([]eventModel.Stage{a, b}, mid, at(0), &finish, now)
	if err != nil || !plan3.Created.OpensAt.Equal(at(4)) || !plan3.Created.ClosesAt.Equal(at(5)) || plan3.Trimmed != nil {
		t.Fatalf("middle stage: %+v err=%v", plan3, err)
	}
	clash, _ := eventModel.NewStage(stEventID, "Clash", at(2), at(5), false, now)
	if _, err = eventModel.PlanCreateStage([]eventModel.Stage{a, b}, clash, at(0), &finish, now); !is(err, eventModel.ErrEventStageOverlap.Err()) {
		t.Fatalf("middle overlap: %v", err)
	}
}

// the edit matrix: what each state of a stage allows
func TestStageApplyMatrix(t *testing.T) {
	start, finish := at(0), at(20)
	str := func(v string) *string { return &v }
	tm := func(h int) *time.Time { v := at(h); return &v }
	yes := func(v bool) *bool { return &v }
	mid := func(open, close int) eventModel.Stage { return stage("Mid", open, close, false) }

	t.Run("upcoming allows everything", func(t *testing.T) {
		s := mid(5, 8)
		got, err := s.Apply(eventModel.StageEdit{Name: str("New"), OpensAt: tm(6), ClosesAt: tm(9), Returnable: yes(true)}, at(1), false, false, start, &finish)
		if err != nil || got.Name != "New" || !got.OpensAt.Equal(at(6)) || !got.ClosesAt.Equal(at(9)) || !got.Returnable {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})
	t.Run("upcoming opens now", func(t *testing.T) {
		s := mid(5, 8)
		got, err := s.Apply(eventModel.StageEdit{OpensAt: tm(0)}, at(2), false, false, start, &finish)
		if err != nil || !got.OpensAt.Equal(at(2)) {
			t.Fatalf("opens_at must clamp to now: %+v err=%v", got, err)
		}
	})
	t.Run("upcoming cannot be closed now", func(t *testing.T) {
		if _, err := mid(5, 8).Apply(eventModel.StageEdit{CloseNow: true}, at(1), false, false, start, &finish); !is(err, eventModel.ErrEventStageNotOpen.Err()) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("anchored boundaries of the first and last stage are the event's", func(t *testing.T) {
		first := stage("F", 0, 5, false)
		if _, err := first.Apply(eventModel.StageEdit{OpensAt: tm(1)}, at(-1), true, false, start, &finish); !is(err, eventModel.ErrEventStageAnchor.Err()) {
			t.Fatalf("first opens_at: %v", err)
		}
		last := stage("L", 15, 20, false)
		if _, err := last.Apply(eventModel.StageEdit{ClosesAt: tm(19)}, at(1), false, true, start, &finish); !is(err, eventModel.ErrEventStageAnchor.Err()) {
			t.Fatalf("last closes_at: %v", err)
		}
	})
	t.Run("open: opens_at locked, rest editable", func(t *testing.T) {
		s := mid(5, 8)
		now := at(6)
		if _, err := s.Apply(eventModel.StageEdit{OpensAt: tm(4)}, now, false, false, start, &finish); !is(err, eventModel.ErrEventStageOpenedLocked.Err()) {
			t.Fatalf("opens_at: %v", err)
		}
		got, err := s.Apply(eventModel.StageEdit{Name: str("X"), ClosesAt: tm(9), Returnable: yes(true)}, now, false, false, start, &finish)
		if err != nil || got.Name != "X" || !got.ClosesAt.Equal(at(9)) || !got.Returnable {
			t.Fatalf("got %+v err=%v", got, err)
		}
		if _, err = s.Apply(eventModel.StageEdit{ClosesAt: tm(6)}, now, false, false, start, &finish); !is(err, eventModel.ErrEventStageInvalid.Err()) {
			t.Fatalf("closes_at must stay in the future: %v", err)
		}
	})
	t.Run("open: close now", func(t *testing.T) {
		s := mid(5, 8)
		got, err := s.Apply(eventModel.StageEdit{CloseNow: true}, at(6), false, false, start, &finish)
		if err != nil || !got.ClosesAt.Equal(at(6)) || got.State(at(6)) != eventModel.StageClosed {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})
	t.Run("closed: only the name", func(t *testing.T) {
		s := mid(5, 8)
		now := at(9)
		got, err := s.Apply(eventModel.StageEdit{Name: str("Renamed")}, now, false, false, start, &finish)
		if err != nil || got.Name != "Renamed" {
			t.Fatalf("name: %+v err=%v", got, err)
		}
		for name, edit := range map[string]eventModel.StageEdit{
			"opens_at": {OpensAt: tm(4)}, "closes_at": {ClosesAt: tm(12)}, "returnable": {Returnable: yes(true)}, "close now": {CloseNow: true},
		} {
			if _, err = s.Apply(edit, now, false, false, start, &finish); !is(err, eventModel.ErrEventStageClosedLocked.Err()) {
				t.Errorf("%s of a closed stage: %v", name, err)
			}
		}
		// an unchanged value is not a change
		if _, err = s.Apply(eventModel.StageEdit{OpensAt: tm(5), Returnable: yes(false)}, now, false, false, start, &finish); err != nil {
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

func TestStageDeleteAndAnchorAfterDelete(t *testing.T) {
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
	finish := at(10)
	a, b, c := stage("A", 3, 5, false), stage("B", 5, 8, false), stage("C", 8, 10, false)
	// the first stage (opening with the event) was removed: B becomes first
	changed := eventModel.AnchorAfterDelete([]eventModel.Stage{b, c}, at(3), &finish, now)
	if len(changed) != 1 || changed[0].ID != b.ID || !changed[0].OpensAt.Equal(at(3)) {
		t.Fatalf("new first stage not anchored: %+v", changed)
	}
	// the last stage was removed: B becomes last
	changed = eventModel.AnchorAfterDelete([]eventModel.Stage{a, b}, at(3), &finish, now)
	if len(changed) != 1 || changed[0].ID != b.ID || !changed[0].ClosesAt.Equal(finish) {
		t.Fatalf("new last stage not anchored: %+v", changed)
	}
	// a middle stage removed: nothing to anchor
	if changed = eventModel.AnchorAfterDelete([]eventModel.Stage{a, c}, at(3), &finish, now); len(changed) != 0 {
		t.Fatalf("middle delete changed %+v", changed)
	}
}

func TestAnchorToLifecycleMovesBoundaryStages(t *testing.T) {
	finish := at(10)
	now := at(-5)
	a, b := stage("A", 0, 5, false), stage("B", 5, 10, false)
	changed, err := eventModel.AnchorToLifecycle([]eventModel.Stage{a, b}, at(-1), &finish, now)
	if err != nil || len(changed) != 1 || !changed[0].OpensAt.Equal(at(-1)) {
		t.Fatalf("start moved: %+v err=%v", changed, err)
	}
	later := at(12)
	changed, err = eventModel.AnchorToLifecycle([]eventModel.Stage{a, b}, at(0), &later, now)
	if err != nil || len(changed) != 1 || !changed[0].ClosesAt.Equal(later) {
		t.Fatalf("finish moved: %+v err=%v", changed, err)
	}
	// start moved past the first stage's end collides
	farStart := at(6)
	if _, err = eventModel.AnchorToLifecycle([]eventModel.Stage{a, b}, farStart, &finish, now); err == nil {
		t.Fatal("a start past the first stage's end must be refused")
	}
	// the first stage has opened: its opens_at is locked
	if _, err = eventModel.AnchorToLifecycle([]eventModel.Stage{a, b}, at(-1), &finish, at(1)); !is(err, eventModel.ErrEventStageOpenedLocked.Err()) {
		t.Fatalf("an opened first stage: %v", err)
	}
	// finish removed
	if _, err = eventModel.AnchorToLifecycle([]eventModel.Stage{a, b}, at(0), nil, now); !is(err, eventModel.ErrEventStageNeedsFinish.Err()) {
		t.Fatalf("finish removed: %v", err)
	}
	if changed, err = eventModel.AnchorToLifecycle(nil, at(0), nil, now); err != nil || changed != nil {
		t.Fatalf("no stages: %v %v", changed, err)
	}
}
