package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

func (f *standFixture) setOf(t *testing.T, challenge uuid.UUID) uuid.UUID {
	t.Helper()
	var set uuid.UUID
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT event_exercise_id FROM event_challenges WHERE id = $1`, challenge).Scan(&set); err != nil {
		t.Fatal(err)
	}
	return set
}

func (f *standFixture) stageTimes(t *testing.T, id uuid.UUID, opens, closes time.Time) {
	t.Helper()
	if _, err := f.db.Pool.Exec(context.Background(), `UPDATE event_stages SET opens_at = $2, closes_at = $3 WHERE id = $1`, id, opens, closes); err != nil {
		t.Fatal(err)
	}
}

func (f *standFixture) is(err error, want error) bool { return errors.Is(err, want) }

// The stage lifecycle through the use case: where a new stage lands, what each state allows, and that the
// first and last boundaries follow the event schedule.
func TestEventStages_CreateEditDeleteAndAnchoring(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	// the fixture publishes before the event becomes available; a lifecycle edit needs a consistent row
	if _, err := f.db.Pool.Exec(ctx, `UPDATE events SET available_from = publish_at - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	e, err := f.uc.GetEventLifecycle(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	start, finish := e.StartAt, *e.FinishAt

	one, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "One", OpensAt: start.Add(time.Minute), ClosesAt: start.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if !one.OpensAt.Equal(start.Truncate(time.Microsecond)) || !one.ClosesAt.Equal(finish.Truncate(time.Microsecond)) || !one.First || !one.Last || one.State != eventModel.StageUpcoming {
		t.Fatalf("the first stage is the whole event window: %+v", one)
	}
	two, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Two", OpensAt: start.Add(time.Hour), ClosesAt: start.Add(2 * time.Hour), Returnable: true})
	if err != nil {
		t.Fatal(err)
	}
	stages, err := f.uc.ListEventStages(ctx, f.eventID)
	if err != nil || len(stages) != 2 || stages[0].ID != one.ID || stages[1].ID != two.ID {
		t.Fatalf("stages = %+v err=%v", stages, err)
	}
	if !stages[0].ClosesAt.Equal(start.Add(time.Hour).Truncate(time.Microsecond)) || !stages[1].ClosesAt.Equal(finish.Truncate(time.Microsecond)) || stages[0].Last || !stages[1].Last {
		t.Fatalf("the previous last stage ends where the new one opens: %+v", stages)
	}
	if _, err = f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "one", OpensAt: start.Add(90 * time.Minute), ClosesAt: start.Add(100 * time.Minute)}); err == nil {
		t.Fatal("a duplicate name must be refused")
	}

	// the anchors are the event's: editing them is refused
	newStart := start.Add(-5 * time.Minute)
	if _, err = f.uc.UpdateEventStage(ctx, f.eventID, one.ID, event.UpdateStageInput{OpensAt: &newStart}); !f.is(err, eventModel.ErrEventStageAnchor.Err()) {
		t.Fatalf("the first stage's opens_at is the event start: %v", err)
	}
	// ... but moving the event start moves the first stage with it
	if _, err = f.uc.UpdateEventLifecycle(ctx, f.eventID, event.UpdateLifecycleInput{JoinPolicy: e.JoinPolicy, PublishAt: e.PublishAt, StartAt: newStart, FinishAt: e.FinishAt, WithdrawAt: e.WithdrawAt}, f.ownerID); err != nil {
		t.Fatalf("lifecycle edit: %v", err)
	}
	if stages, _ = f.uc.ListEventStages(ctx, f.eventID); !stages[0].OpensAt.Equal(newStart.Truncate(time.Microsecond)) {
		t.Fatalf("the first stage must follow the event start: %+v", stages[0])
	}
	// removing the finish while stages exist is refused
	if _, err = f.uc.UpdateEventLifecycle(ctx, f.eventID, event.UpdateLifecycleInput{JoinPolicy: e.JoinPolicy, PublishAt: e.PublishAt, StartAt: newStart}, f.ownerID); err == nil {
		t.Fatal("stages need a scheduled finish")
	}

	// sets: free before the stage opens
	set := f.setOf(t, f.infraOne)
	view, err := f.uc.SetEventExerciseStage(ctx, f.eventID, set, &one.ID)
	if err != nil || view.StageID == nil || *view.StageID != one.ID {
		t.Fatalf("set stage: %+v err=%v", view, err)
	}
	if _, err = f.uc.SetEventExerciseStage(ctx, f.eventID, set, &two.ID); err != nil {
		t.Fatalf("between upcoming stages: %v", err)
	}
	if err = f.uc.DeleteEventStage(ctx, f.eventID, two.ID); !f.is(err, eventModel.ErrEventStageNotDeletable.Err()) {
		t.Fatalf("a stage with sets is not deletable: %v", err)
	}
	if _, err = f.uc.SetEventExerciseStage(ctx, f.eventID, set, nil); err != nil {
		t.Fatal(err)
	}

	// time passes: stage one opens, then closes
	f.shiftLifecycle(t, -time.Hour, 3*time.Hour)
	now := time.Now()
	shifted, err := f.uc.GetEventLifecycle(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	f.stageTimes(t, one.ID, shifted.StartAt, now.Add(10*time.Minute))
	f.stageTimes(t, two.ID, now.Add(10*time.Minute), *shifted.FinishAt)
	if _, err = f.uc.SetEventExerciseStage(ctx, f.eventID, set, &one.ID); err != nil {
		t.Fatalf("adding a set to an open stage must work: %v", err)
	}
	if _, err = f.uc.SetEventExerciseStage(ctx, f.eventID, set, nil); !f.is(err, eventModel.ErrEventStageOpenedLocked.Err()) {
		t.Fatalf("nothing leaves an opened stage: %v", err)
	}
	if _, err = f.uc.SetEventExerciseStage(ctx, f.eventID, set, &two.ID); !f.is(err, eventModel.ErrEventStageOpenedLocked.Err()) {
		t.Fatalf("open -> anywhere is refused: %v", err)
	}
	if err = f.uc.DeleteEventStage(ctx, f.eventID, one.ID); !f.is(err, eventModel.ErrEventStageNotDeletable.Err()) {
		t.Fatalf("an opened stage is never deletable: %v", err)
	}
	opens := now.Add(-2 * time.Hour)
	if _, err = f.uc.UpdateEventStage(ctx, f.eventID, one.ID, event.UpdateStageInput{OpensAt: &opens}); !f.is(err, eventModel.ErrEventStageOpenedLocked.Err()) {
		t.Fatalf("an opened stage's opens_at is locked: %v", err)
	}
	renamed := "Warm-up"
	returnable := true
	if got, updateErr := f.uc.UpdateEventStage(ctx, f.eventID, one.ID, event.UpdateStageInput{Name: &renamed, Returnable: &returnable}); updateErr != nil || got.Name != renamed || !got.Returnable {
		t.Fatalf("an open stage's name and returnable are editable: %+v err=%v", got, updateErr)
	}

	// «Закрити зараз»: the stage ends now, the next one does not start earlier; a closed stage is locked
	closed, err := f.uc.UpdateEventStage(ctx, f.eventID, one.ID, event.UpdateStageInput{CloseNow: true})
	if err != nil || closed.State != eventModel.StageClosed {
		t.Fatalf("close now: %+v err=%v", closed, err)
	}
	stages, _ = f.uc.ListEventStages(ctx, f.eventID)
	if !stages[1].OpensAt.Equal(now.Add(10 * time.Minute).Truncate(time.Microsecond)) {
		t.Fatalf("the next stage must not start earlier: %+v", stages[1])
	}
	notReturnable := false
	if _, err = f.uc.UpdateEventStage(ctx, f.eventID, one.ID, event.UpdateStageInput{Returnable: &notReturnable}); !f.is(err, eventModel.ErrEventStageClosedLocked.Err()) {
		t.Fatalf("returnable is locked once the stage closed: %v", err)
	}
	if _, err = f.uc.UpdateEventStage(ctx, f.eventID, one.ID, event.UpdateStageInput{Name: &renamed}); err != nil {
		t.Fatalf("a closed stage keeps its name editable: %v", err)
	}
	if _, err = f.uc.SetEventExerciseStage(ctx, f.eventID, f.setOf(t, f.staticChallenge), &one.ID); !f.is(err, eventModel.ErrEventStageClosedLocked.Err()) {
		t.Fatalf("a closed stage accepts nothing new: %v", err)
	}
	// the set's tasks of an opened stage cannot be detached
	if err = f.uc.DetachEventExercise(ctx, f.eventID, set, true, f.ownerID); !f.is(err, eventModel.ErrEventStageOpenedLocked.Err()) {
		t.Fatalf("detaching removes the set from an opened stage: %v", err)
	}
}

// Labs of a later stage deploy at opens_at minus the computed lead, the event-start barrier does not wait for
// them, and a set added to an open stage deploys at once.
func TestStandEngine_LaterStageLabsDeployAtTheirLead(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	e, err := f.uc.GetEventLifecycle(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	start := e.StartAt
	one, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "One"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Two", OpensAt: start.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	set := f.setOf(t, f.infraOne)
	if _, err = f.uc.SetEventExerciseStage(ctx, f.eventID, set, &two.ID); err != nil {
		t.Fatal(err)
	}

	// before the event: the labs of the later stage are not due, so nothing deploys and the barrier does not wait
	f.pass(t)
	if len(f.agent.deployed) != 0 {
		t.Fatalf("the labs of a later stage must wait for their lead: %v", f.agent.deployed)
	}
	f.shiftLifecycle(t, -time.Minute, 3*time.Hour)
	now := time.Now()
	f.stageTimes(t, one.ID, now.Add(-time.Minute), now.Add(time.Hour))
	f.stageTimes(t, two.ID, now.Add(time.Hour), now.Add(3*time.Hour))
	f.pass(t)
	if len(f.agent.deployed) != 0 {
		t.Fatalf("an hour before stage two its labs are still not due: %v", f.agent.deployed)
	}
	view, err := f.uc.GetEventStands(ctx, f.eventID)
	if err != nil || !view.ChallengesOpened {
		t.Fatalf("the barrier must not wait for the labs of later stages: %+v err=%v", view, err)
	}
	f.assertStatuses(t, map[string]string{"": "ready", "Blue": "ready", "Red": "ready"})

	// inside the lead the labs deploy
	f.stageTimes(t, one.ID, now.Add(-time.Hour), now.Add(5*time.Minute))
	f.stageTimes(t, two.ID, now.Add(5*time.Minute), now.Add(3*time.Hour))
	f.pass(t)
	if len(f.agent.deployed) != 3 {
		t.Fatalf("within the lead the labs of stage two deploy (blue, red, moderators): %v", f.agent.deployed)
	}

	// stage two opens; a set added to it deploys at once (no lead)
	f.stageTimes(t, one.ID, now.Add(-time.Hour), now.Add(-time.Minute))
	f.stageTimes(t, two.ID, now.Add(-time.Minute), now.Add(3*time.Hour))
	added := f.attach(t, true)
	if _, err = f.uc.SetEventExerciseStage(ctx, f.eventID, f.setOf(t, added), &two.ID); err != nil {
		t.Fatalf("adding a set to an open stage: %v", err)
	}
	before := len(f.agent.deployed)
	f.pass(t)
	if len(f.agent.deployed) != before+3 {
		t.Fatalf("a set added to an open stage deploys at once: %d -> %d", before, len(f.agent.deployed))
	}
}
