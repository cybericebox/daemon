package event

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
)

var boardT0 = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

func boardAt(h float64) time.Time { return boardT0.Add(time.Duration(h * float64(time.Hour))) }

func boardStage(name string, open, close float64, returnable bool) eventModel.Stage {
	return eventModel.Stage{ID: uuid.Must(uuid.NewV7()), Name: name, OpensAt: boardAt(open), ClosesAt: boardAt(close), Returnable: returnable}
}

func TestBuildOwnBoard_StageContext(t *testing.T) {
	one, two, three := boardStage("One", 0, 3, true), boardStage("Two", 4, 7, false), boardStage("Three", 8, 10, false)
	stages := []eventModel.Stage{one, two, three}
	before := eventConfigModel.CountdownSettings{ShowStart: true, ShowFinish: true, FinishMinutes: 30, FinishMode: eventConfigModel.FinishBeforeEnd}
	fromStart := before
	fromStart.FinishMode = eventConfigModel.FinishFromStart

	t.Run("no stages: nothing staged", func(t *testing.T) {
		v := buildOwnBoard(nil, nil, before, boardAt(1))
		if v.CurrentStage != nil || v.NextOpensAt != nil || v.NextChangeAt != nil || len(v.Stages) != 0 || !v.ServerNow.Equal(boardAt(1)) {
			t.Fatalf("%+v", v)
		}
	})
	t.Run("before the first stage: no stage timers, upcoming stages never listed", func(t *testing.T) {
		v := buildOwnBoard(nil, stages, before, boardAt(-1))
		if v.CurrentStage != nil || v.NextOpensAt != nil || len(v.Stages) != 0 {
			t.Fatalf("%+v", v)
		}
		if v.NextChangeAt == nil || !v.NextChangeAt.Equal(boardAt(0)) {
			t.Fatalf("next change = %v", v.NextChangeAt)
		}
	})
	t.Run("an open stage: only opened stages are listed, countdown per mode", func(t *testing.T) {
		v := buildOwnBoard(nil, stages, before, boardAt(1))
		if v.CurrentStage == nil || v.CurrentStage.ID != one.ID || v.CurrentStage.Last || len(v.Stages) != 1 || v.Stages[0].State != eventModel.StageOpen {
			t.Fatalf("%+v", v)
		}
		if v.CurrentStage.EndsAt != nil {
			t.Fatal("before_end: the stage countdown is hidden until the last FinishMinutes")
		}
		if v.NextChangeAt == nil || !v.NextChangeAt.Equal(boardAt(3)) {
			t.Fatalf("next change = %v", v.NextChangeAt)
		}
		v = buildOwnBoard(nil, stages, before, boardAt(2.6))
		if v.CurrentStage.EndsAt == nil || !v.CurrentStage.EndsAt.Equal(boardAt(3)) {
			t.Fatalf("during the last 30 minutes the countdown shows: %+v", v.CurrentStage)
		}
		v = buildOwnBoard(nil, stages, fromStart, boardAt(1))
		if v.CurrentStage.EndsAt == nil {
			t.Fatal("from_start: the countdown is visible from the beginning of the stage")
		}
		off := fromStart
		off.ShowFinish = false
		if v = buildOwnBoard(nil, stages, off, boardAt(2.9)); v.CurrentStage.EndsAt != nil {
			t.Fatal("ShowFinish off switches the countdown off")
		}
	})
	t.Run("a break: the countdown to the next stage, closed stage listed", func(t *testing.T) {
		v := buildOwnBoard(nil, stages, before, boardAt(3.5))
		if v.CurrentStage != nil || v.NextOpensAt == nil || !v.NextOpensAt.Equal(boardAt(4)) {
			t.Fatalf("%+v", v)
		}
		if len(v.Stages) != 1 || v.Stages[0].State != eventModel.StageClosed || !v.Stages[0].Returnable {
			t.Fatalf("stages = %+v", v.Stages)
		}
		if v.NextChangeAt == nil || !v.NextChangeAt.Equal(boardAt(4)) {
			t.Fatalf("next change = %v", v.NextChangeAt)
		}
	})
	t.Run("the last stage ends with the event: no stage countdown", func(t *testing.T) {
		v := buildOwnBoard(nil, stages, fromStart, boardAt(9))
		if v.CurrentStage == nil || !v.CurrentStage.Last || v.CurrentStage.EndsAt != nil || v.NextOpensAt != nil || len(v.Stages) != 3 {
			t.Fatalf("%+v", v)
		}
	})
	t.Run("after every stage: no current stage and no break", func(t *testing.T) {
		v := buildOwnBoard(nil, stages, before, boardAt(11))
		if v.CurrentStage != nil || v.NextOpensAt != nil || v.NextChangeAt != nil {
			t.Fatalf("%+v", v)
		}
	})
}
