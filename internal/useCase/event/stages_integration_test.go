package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
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

// The stage lifecycle through the use case: where a new stage lands, what each state allows, and how
// each explicit window remains independent of its neighbors and the event schedule.
func TestEventStages_CreateEditDeleteWithIndependentWindows(t *testing.T) {
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
	start := e.StartAt

	one, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "One", OpensAt: start.Add(time.Minute), ClosesAt: start.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if !one.OpensAt.Equal(start.Add(time.Minute).Truncate(time.Microsecond)) || !one.ClosesAt.Equal(start.Add(2*time.Minute).Truncate(time.Microsecond)) || !one.First || !one.Last || one.State != eventModel.StageUpcoming {
		t.Fatalf("the first stage keeps its explicit window: %+v", one)
	}
	two, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Two", OpensAt: start.Add(time.Hour), ClosesAt: start.Add(90 * time.Minute), Returnable: true})
	if err != nil {
		t.Fatal(err)
	}
	stages, err := f.uc.ListEventStages(ctx, f.eventID)
	if err != nil || len(stages) != 2 || stages[0].ID != one.ID || stages[1].ID != two.ID {
		t.Fatalf("stages = %+v err=%v", stages, err)
	}
	if !stages[0].ClosesAt.Equal(start.Add(2*time.Minute).Truncate(time.Microsecond)) || !stages[1].ClosesAt.Equal(start.Add(90*time.Minute).Truncate(time.Microsecond)) || stages[0].Last || !stages[1].Last {
		t.Fatalf("adding a stage must not move either window: %+v", stages)
	}
	if _, err = f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "one", OpensAt: start.Add(90 * time.Minute), ClosesAt: start.Add(100 * time.Minute)}); err == nil {
		t.Fatal("a duplicate name must be refused")
	}

	if one.DeployLeadMinutes <= 0 || two.DeployLeadMinutes <= 0 {
		t.Fatal("stage replies must retain the computed preparation lead")
	}
	// The first and last stages are editable without moving the event.
	ownOpen, ownClose := start.Add(3*time.Minute), start.Add(4*time.Minute)
	if _, err = f.uc.UpdateEventStage(ctx, f.eventID, one.ID, event.UpdateStageInput{OpensAt: &ownOpen, ClosesAt: &ownClose}); err != nil {
		t.Fatalf("first-stage dates: %v", err)
	}
	newStart := start.Add(-5 * time.Minute)
	if _, err = f.uc.UpdateEventLifecycle(ctx, f.eventID, event.UpdateLifecycleInput{JoinPolicy: e.JoinPolicy, PublishAt: e.PublishAt, StartAt: newStart, FinishAt: e.FinishAt, WithdrawAt: e.WithdrawAt}, f.ownerID); err != nil {
		t.Fatalf("lifecycle edit: %v", err)
	}
	if stages, _ = f.uc.ListEventStages(ctx, f.eventID); !stages[0].OpensAt.Equal(ownOpen.Truncate(time.Microsecond)) || !stages[0].ClosesAt.Equal(ownClose.Truncate(time.Microsecond)) {
		t.Fatalf("event schedule must leave stage dates unchanged: %+v", stages[0])
	}
	// A running event may have no scheduled finish while its stages have explicit closing times.
	if _, err = f.uc.UpdateEventLifecycle(ctx, f.eventID, event.UpdateLifecycleInput{JoinPolicy: e.JoinPolicy, PublishAt: e.PublishAt, StartAt: newStart}, f.ownerID); err != nil {
		t.Fatalf("finish is optional: %v", err)
	}
	third, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Three", OpensAt: start.Add(3 * time.Hour), ClosesAt: start.Add(4 * time.Hour)})
	if err != nil {
		t.Fatalf("creation without event finish: %v", err)
	}
	if err = f.uc.DeleteEventStage(ctx, f.eventID, third.ID); err != nil {
		t.Fatal(err)
	}
	if stages, _ = f.uc.ListEventStages(ctx, f.eventID); !stages[1].ClosesAt.Equal(start.Add(90 * time.Minute).Truncate(time.Microsecond)) {
		t.Fatalf("deleting the last stage must not retime its neighbor: %+v", stages)
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
	afterClose, lifecycleErr := f.uc.GetEventLifecycle(ctx, f.eventID)
	if lifecycleErr != nil || afterClose.FinishAt == nil || !afterClose.FinishAt.Equal(*shifted.FinishAt) {
		t.Fatalf("stage close changed event finish: %+v err=%v", afterClose, lifecycleErr)
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
	one, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "One", OpensAt: start, ClosesAt: start.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	two, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Two", OpensAt: start.Add(time.Hour), ClosesAt: *e.FinishAt})
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

func TestEventStages_SelectedBoundariesFollowScheduleAndCustomDatesStay(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	if _, err := f.db.Pool.Exec(ctx, `UPDATE events SET available_from=publish_at-interval '1 hour' WHERE id=$1`, f.eventID); err != nil {
		t.Fatal(err)
	}
	e, err := f.uc.GetEventLifecycle(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	start, finish := e.StartAt, *e.FinishAt
	first, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Bound start", OpensAt: start, ClosesAt: start.Add(10 * time.Minute), OpensWithEvent: true})
	if err != nil {
		t.Fatal(err)
	}
	mid, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Custom", OpensAt: start.Add(20 * time.Minute), ClosesAt: start.Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	last, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Bound finish", OpensAt: start.Add(40 * time.Minute), ClosesAt: finish, ClosesWithEvent: true})
	if err != nil {
		t.Fatal(err)
	}
	if !first.OpensWithEvent || first.ClosesWithEvent || mid.OpensWithEvent || mid.ClosesWithEvent || !last.ClosesWithEvent {
		t.Fatal("explicit source choices were not preserved")
	}
	nextStart, nextFinish := start.Add(time.Minute), finish.Add(time.Hour)
	nextWithdraw := nextFinish.Add(time.Hour)
	if _, err = f.uc.UpdateEventLifecycle(ctx, f.eventID, event.UpdateLifecycleInput{JoinPolicy: e.JoinPolicy, PublishAt: e.PublishAt, StartAt: nextStart, FinishAt: &nextFinish, WithdrawAt: &nextWithdraw}, f.ownerID); err != nil {
		t.Fatal(err)
	}
	rows, err := f.uc.ListEventStages(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[uuid.UUID]event.EventStageView{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	if !byID[first.ID].OpensAt.Equal(nextStart.Truncate(time.Microsecond)) || !byID[first.ID].ClosesAt.Equal(first.ClosesAt) || !byID[last.ID].ClosesAt.Equal(nextFinish.Truncate(time.Microsecond)) || !byID[mid.ID].OpensAt.Equal(mid.OpensAt) || !byID[mid.ID].ClosesAt.Equal(mid.ClosesAt) {
		t.Fatalf("only selected boundaries should move: %+v", rows)
	}
	if _, err = f.uc.UpdateEventLifecycle(ctx, f.eventID, event.UpdateLifecycleInput{JoinPolicy: e.JoinPolicy, PublishAt: e.PublishAt, StartAt: nextStart}, f.ownerID); err == nil {
		t.Fatal("cannot remove event finish while an upcoming stage closing follows it")
	}
}

func TestEventStageScheduleEditPreservesRecordedManualFinish(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.shiftLifecycle(t, -time.Hour, 2*time.Hour)
	actualFinish := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	if _, err := f.db.Pool.Exec(ctx, `UPDATE events SET available_from=publish_at-interval '1 hour', manual_finished_at=$2 WHERE id=$1`, f.eventID, actualFinish); err != nil {
		t.Fatal(err)
	}
	before, err := f.uc.GetEventLifecycle(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	finish := before.FinishAt.Add(time.Hour)
	withdraw := finish.Add(time.Hour)
	after, err := f.uc.UpdateEventLifecycle(ctx, f.eventID, event.UpdateLifecycleInput{JoinPolicy: before.JoinPolicy, PublishAt: before.PublishAt, StartAt: before.StartAt, FinishAt: &finish, WithdrawAt: &withdraw}, f.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	var recorded *time.Time
	if err := f.db.Pool.QueryRow(ctx, `SELECT manual_finished_at FROM events WHERE id=$1`, f.eventID).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded == nil || !recorded.Equal(actualFinish) || after.Status != eventModel.LifecycleFinished {
		t.Fatalf("editing scheduled dates must not erase actual finish or reopen runtime: %+v", after)
	}
}

func TestCustomStageWindowsStayInsidePlatformAvailabilityWithoutScheduledFinish(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	if _, err := f.db.Pool.Exec(ctx, `UPDATE events SET available_from=publish_at-interval '1 hour' WHERE id=$1`, f.eventID); err != nil {
		t.Fatal(err)
	}
	lifecycle, err := f.uc.GetEventLifecycle(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.uc.UpdateEventLifecycle(ctx, f.eventID, event.UpdateLifecycleInput{JoinPolicy: lifecycle.JoinPolicy, PublishAt: lifecycle.PublishAt, StartAt: lifecycle.StartAt}, f.ownerID); err != nil {
		t.Fatal(err)
	}
	platform, err := f.uc.GetEvent(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	archive := lifecycle.StartAt.Add(90 * time.Minute)
	if _, err = f.uc.UpdateEvent(ctx, f.eventID, event.UpdateEventInput{Tag: platform.Tag, Name: platform.Name, AvailableFrom: platform.AvailableFrom, ArchiveAt: archive}, f.ownerID); err != nil {
		t.Fatal(err)
	}
	late := archive.Add(time.Hour)
	if _, err = f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Unreachable", OpensAt: archive.Add(time.Minute), ClosesAt: late}); !f.is(err, eventModel.ErrEventStageOutsideEvent.Err()) {
		t.Fatalf("a stage after platform archive must be refused: %v", err)
	}
	allowed, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Inside", OpensAt: lifecycle.StartAt.Add(time.Hour), ClosesAt: archive})
	if err != nil {
		t.Fatalf("stage closing exactly at archive is valid: %v", err)
	}
	if _, err = f.uc.UpdateEventStage(ctx, f.eventID, allowed.ID, event.UpdateStageInput{ClosesAt: &late}); !f.is(err, eventModel.ErrEventStageOutsideEvent.Err()) {
		t.Fatalf("stage edit cannot outlast availability: %v", err)
	}
	if _, err = f.uc.UpdateEventLifecycle(ctx, f.eventID, event.UpdateLifecycleInput{JoinPolicy: lifecycle.JoinPolicy, PublishAt: lifecycle.PublishAt, StartAt: lifecycle.StartAt}, f.ownerID); err != nil {
		t.Fatalf("custom closure with nil event finish should remain valid: %v", err)
	}
	if rows, err := f.uc.ListEventStages(ctx, f.eventID); err != nil || len(rows) != 1 || rows[0].ClosesWithEvent || !rows[0].ClosesAt.Equal(archive.Truncate(time.Microsecond)) {
		t.Fatalf("archive must not become a source or retime a stage: %+v err=%v", rows, err)
	}
	smaller := archive.Add(-time.Minute)
	if _, err = f.uc.UpdateEvent(ctx, f.eventID, event.UpdateEventInput{Tag: platform.Tag, Name: platform.Name, AvailableFrom: platform.AvailableFrom, ArchiveAt: smaller}, f.ownerID); !f.is(err, eventModel.ErrEventStageOutsideEvent.Err()) {
		t.Fatalf("platform availability edit must not crop existing stage: %v", err)
	}
	current, err := f.uc.GetEvent(ctx, f.eventID)
	if err != nil || !current.ArchiveAt.Equal(archive.Truncate(time.Microsecond)) {
		t.Fatalf("rejected platform update changed availability: %+v err=%v", current, err)
	}
	// Historical/externally written rows are still loaded, but a lifecycle write must not accept an unreachable window.
	f.stageTimes(t, allowed.ID, allowed.OpensAt, late)
	if _, err = f.uc.UpdateEventLifecycle(ctx, f.eventID, event.UpdateLifecycleInput{JoinPolicy: lifecycle.JoinPolicy, PublishAt: lifecycle.PublishAt, StartAt: lifecycle.StartAt}, f.ownerID); !f.is(err, eventModel.ErrEventStageOutsideEvent.Err()) {
		t.Fatalf("lifecycle edit must enforce availability independently of optional finish: %v", err)
	}
	if rows, err := f.uc.ListEventStages(ctx, f.eventID); err != nil || !rows[0].ClosesAt.Equal(late.Truncate(time.Microsecond)) {
		t.Fatalf("validation must not rewrite historical timestamps: %+v err=%v", rows, err)
	}
}

type availabilityBeforeLockUOW struct {
	inner        postgres.IUnitOfWorker[event.IRepository]
	publishStage func() error
}

func (w availabilityBeforeLockUOW) UnitOfWork(ctx context.Context) (context.Context, event.IRepository, postgres.UoW, error) {
	txCtx, q, unit, err := w.inner.UnitOfWork(ctx)
	if err != nil {
		return txCtx, q, unit, err
	}
	if err = w.publishStage(); err != nil {
		unit.Restore()
		return txCtx, q, unit, err
	}
	return txCtx, q, unit, nil
}

func TestAdminAvailabilityChecksStagesPublishedAfterInitialEventRead(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	if _, err := f.db.Pool.Exec(ctx, `UPDATE events SET available_from=publish_at-interval '1 hour' WHERE id=$1`, f.eventID); err != nil {
		t.Fatal(err)
	}
	lifecycle, err := f.uc.GetEventLifecycle(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.uc.UpdateEventLifecycle(ctx, f.eventID, event.UpdateLifecycleInput{JoinPolicy: lifecycle.JoinPolicy, PublishAt: lifecycle.PublishAt, StartAt: lifecycle.StartAt}, f.ownerID); err != nil {
		t.Fatal(err)
	}
	platform, err := f.uc.GetEvent(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	archive := lifecycle.StartAt.Add(time.Hour)
	staged := false
	uow := availabilityBeforeLockUOW{inner: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)), publishStage: func() error {
		staged = true
		_, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Arrived before lock", OpensAt: lifecycle.StartAt.Add(30 * time.Minute), ClosesAt: lifecycle.StartAt.Add(90 * time.Minute)})
		return err
	}}
	admin := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, UoW: uow})
	if _, err = admin.UpdateEvent(ctx, f.eventID, event.UpdateEventInput{Tag: platform.Tag, Name: platform.Name, AvailableFrom: platform.AvailableFrom, ArchiveAt: archive}, f.ownerID); !f.is(err, eventModel.ErrEventStageOutsideEvent.Err()) || !staged {
		t.Fatalf("availability validation must see stages committed after preliminary read: staged=%v err=%v", staged, err)
	}
	current, err := f.uc.GetEvent(ctx, f.eventID)
	if err != nil || !current.ArchiveAt.IsZero() {
		t.Fatalf("rejected availability edit changed event: %+v err=%v", current, err)
	}
}
