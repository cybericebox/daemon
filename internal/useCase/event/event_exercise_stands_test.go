package event_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
	event "github.com/cybericebox/daemon/internal/useCase/event"
	errs "github.com/cybericebox/daemon/pkg/err"
)

// deletingLabInfra records the Labs the update deleted from the agent.
type deletingLabInfra struct {
	readyLabInfra
	mu      sync.Mutex
	deleted [][2]string
}

func (d *deletingLabInfra) DeleteLab(_ context.Context, group, lab string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deleted = append(d.deleted, [2]string{group, lab})
	return nil
}

type standUpdateFixture struct {
	q                                 *postgresMocks.MockQuerier
	uc                                *event.EventUseCase
	infra                             *deletingLabInfra
	unit                              *testUoW
	eventID, attachmentID, nextID     uuid.UUID
	teamID, challengeID, assignmentID uuid.UUID
	recreated                         *postgres.RecreateLabBindingParams
	reset                             *postgres.UpdateTeamChallengeReadinessParams
	stand                             *postgres.UpdateEventTeamStandParams
}

// newStandUpdateFixture prepares one team with a ready stand Lab of the set whose update is asked for. started
// says whether the event is running; stageOpensIn, when set, puts the set into a stage that opens that long from now.
func newStandUpdateFixture(t *testing.T, started bool, stageOpensIn *time.Duration) *standUpdateFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	f := &standUpdateFixture{q: q, unit: &testUoW{}, infra: &deletingLabInfra{}}
	f.uc = event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: f.unit}, Infra: f.infra})
	f.eventID, f.attachmentID, f.nextID = uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	f.teamID, f.challengeID, f.assignmentID = uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	exerciseID, previousID, taskID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	version := func(id uuid.UUID, name string) postgres.ExerciseVersion {
		return postgres.ExerciseVersion{ID: id, ExerciseID: exerciseID, Status: string(exerciseModel.VersionStatusPublished), PublishedAt: pgtype.Timestamptz{Time: now, Valid: true}, CreatedAt: now,
			Variants: []byte(`[{"tasks":[{"id":"` + taskID.String() + `","name":"` + name + `","difficulty":"easy","flag":["ICE{same}"]}],"topology":{"devices":[{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"web"}]}}]`)}
	}
	link := postgres.EventExercise{ID: f.attachmentID, EventID: f.eventID, ExerciseID: exerciseID, ExerciseVersionID: previousID,
		VariantMode: int16(eventExerciseModel.VariantModePerTeam), Revision: 1, Status: int16(eventExerciseModel.StatusActive), CreatedAt: now}
	if stageOpensIn != nil {
		stageID := uuid.Must(uuid.NewV7())
		link.StageID = uuid.NullUUID{UUID: stageID, Valid: true}
		q.EXPECT().GetEventStage(gomock.Any(), postgres.GetEventStageParams{ID: stageID, EventID: f.eventID}).Return(postgres.EventStage{ID: stageID, EventID: f.eventID, Name: "Day 2",
			OpensAt: now.Add(*stageOpensIn), ClosesAt: now.Add(*stageOpensIn + time.Hour), CreatedAt: now, UpdatedAt: now}, nil).AnyTimes()
	}
	ev := startedEvent(f.eventID, now)
	ev.InfrastructureAllowed = true
	if !started {
		ev.StartAt = now.Add(time.Hour)
		ev.FinishAt = pgtype.Timestamptz{Time: now.Add(2 * time.Hour), Valid: true}
		ev.WithdrawAt = pgtype.Timestamptz{Time: now.Add(3 * time.Hour), Valid: true}
		ev.ArchiveAt = ev.WithdrawAt
		ev.AvailableFrom = now.Add(time.Hour)
	}
	challenge := postgres.EventChallenge{ID: f.challengeID, EventExerciseID: f.attachmentID, TaskID: taskID, OrderIndex: 1, Points: 100, Published: true, Snapshot: []byte(`{"name":"Old","difficulty":"easy"}`), CreatedAt: now}
	binding := postgres.LabBinding{ID: uuid.Must(uuid.NewV7()), EventID: f.eventID, EventTeamID: f.teamID, EventChallengeID: f.challengeID,
		LabGroupName: "e-group", LabName: "x-set-v0", Generation: 2, Readiness: int16(labBindingModel.ReadinessReady), CreatedAt: now}

	q.EXPECT().GetEventExerciseByID(gomock.Any(), gomock.Any()).Return(link, nil).AnyTimes()
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID, Name: "Web"}, nil).AnyTimes()
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), f.nextID).Return(version(f.nextID, "New"), nil).AnyTimes()
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), previousID).Return(version(previousID, "Old"), nil).AnyTimes()
	q.EXPECT().IsExerciseAvailableToEvent(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	q.EXPECT().GetEventByID(gomock.Any(), f.eventID).Return(ev, nil).AnyTimes()
	q.EXPECT().ListEventChallenges(gomock.Any(), f.attachmentID).Return([]postgres.EventChallenge{challenge}, nil).AnyTimes()
	q.EXPECT().ListTeamChallengesForRefresh(gomock.Any(), []uuid.UUID{f.challengeID}).Return([]postgres.ListTeamChallengesForRefreshRow{{
		ID: f.assignmentID, EventTeamID: f.teamID, EventChallengeID: f.challengeID, Snapshot: []byte(`{"name":"Old","difficulty":"easy"}`), Hints: []byte(`[]`), ExpectedFlag: "ICE{same}"}}, nil).AnyTimes()
	q.EXPECT().GetLabBinding(gomock.Any(), postgres.GetLabBindingParams{EventTeamID: f.teamID, EventChallengeID: f.challengeID}).Return(binding, nil).AnyTimes()
	q.EXPECT().ListStandTeams(gomock.Any(), gomock.Any()).Return([]postgres.ListStandTeamsRow{{ID: f.teamID, PublicName: "Red team", Admitted: true,
		StandStatus: pgtype.Int2{Int16: 2, Valid: true}, StandGeneration: pgtype.Int4{Int32: 2, Valid: true}, LabGeneration: 2}}, nil).AnyTimes()
	q.EXPECT().UpdateEventChallengeContent(gomock.Any(), gomock.Any()).Return(int64(1), nil).AnyTimes()
	q.EXPECT().SetEventChallengeHintCosts(gomock.Any(), gomock.Any()).Return(int64(1), nil).AnyTimes()
	q.EXPECT().MaxEventChallengeOrder(gomock.Any(), f.attachmentID).Return(int32(1), nil).AnyTimes()
	q.EXPECT().UpdateTeamChallengeContent(gomock.Any(), gomock.Any()).Return(int64(1), nil).AnyTimes()
	q.EXPECT().UpdateEventExerciseSource(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventExerciseSourceParams) (postgres.EventExercise, error) {
		updated := link
		updated.ExerciseVersionID, updated.Revision = arg.ExerciseVersionID, 2
		return updated, nil
	}).AnyTimes()
	q.EXPECT().RecreateLabBinding(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.RecreateLabBindingParams) (int64, error) {
		f.recreated = &arg
		return 1, nil
	}).AnyTimes()
	q.EXPECT().UpdateTeamChallengeReadiness(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateTeamChallengeReadinessParams) (int64, error) {
		f.reset = &arg
		return 1, nil
	}).AnyTimes()
	q.EXPECT().UpdateEventTeamStand(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventTeamStandParams) (int64, error) {
		f.stand = &arg
		return 1, nil
	}).AnyTimes()
	return f
}

func (f *standUpdateFixture) requireRecreated(t *testing.T) {
	t.Helper()
	if f.recreated == nil || f.recreated.Generation != 2 || f.recreated.NextGeneration != 3 || f.recreated.LabName != "x-set-v0-g3" {
		t.Fatalf("lab binding was not moved to the next generation: %+v", f.recreated)
	}
	if f.reset == nil || f.reset.ID != f.assignmentID || f.reset.ExpectedReadiness != int16(teamChallengeModel.ReadinessReady) || f.reset.Readiness != int16(teamChallengeModel.ReadinessPreparing) {
		t.Fatalf("team challenge was not returned to preparing: %+v", f.reset)
	}
	if f.stand == nil || f.stand.Status != 1 || f.stand.Generation != 3 {
		t.Fatalf("stand was not set to creating on the new generation: %+v", f.stand)
	}
	if len(f.infra.deleted) != 1 || f.infra.deleted[0] != [2]string{"e-group", "x-set-v0"} {
		t.Fatalf("replaced lab was not deleted from the agent: %v", f.infra.deleted)
	}
	if !f.unit.saved {
		t.Fatal("switch was not saved")
	}
}

func TestUpdateEventExercise_RecreatesStandsBeforeEventStart(t *testing.T) {
	f := newStandUpdateFixture(t, false, nil)
	if _, err := f.uc.UpdateEventExercise(context.Background(), f.eventID, f.attachmentID, &f.nextID, uuid.Must(uuid.NewV7()), false); err != nil {
		t.Fatalf("UpdateEventExercise: %v", err)
	}
	f.requireRecreated(t)
}

func TestUpdateEventExercise_RunningStandsNeedConfirmation(t *testing.T) {
	f := newStandUpdateFixture(t, true, nil)
	_, err := f.uc.UpdateEventExercise(context.Background(), f.eventID, f.attachmentID, &f.nextID, uuid.Must(uuid.NewV7()), false)
	if !errors.Is(err, eventExerciseModel.ErrEventExerciseStandsRunning.Err()) {
		t.Fatalf("want ErrEventExerciseStandsRunning, got %v", err)
	}
	var coded errs.Error
	if !errors.As(err, &coded) {
		t.Fatalf("error carries no context: %v", err)
	}
	teams, _ := coded.PublicContext()[eventExerciseModel.ContextTeams].([]map[string]string)
	if len(teams) != 1 || teams[0]["ID"] != f.teamID.String() || teams[0]["Name"] != "Red team" {
		t.Fatalf("conflict does not list the affected team: %v", coded.PublicContext())
	}
	if f.unit.saved || f.recreated != nil || len(f.infra.deleted) != 0 {
		t.Fatalf("nothing may change without confirmation: saved=%t recreated=%v deleted=%v", f.unit.saved, f.recreated, f.infra.deleted)
	}
}

func TestUpdateEventExercise_RunningStandsRecreatedWhenConfirmed(t *testing.T) {
	f := newStandUpdateFixture(t, true, nil)
	if _, err := f.uc.UpdateEventExercise(context.Background(), f.eventID, f.attachmentID, &f.nextID, uuid.Must(uuid.NewV7()), true); err != nil {
		t.Fatalf("UpdateEventExercise: %v", err)
	}
	f.requireRecreated(t)
}

func TestUpdateEventExercise_UpcomingStageStandsRecreatedOnStartedEvent(t *testing.T) {
	opensIn := time.Hour
	f := newStandUpdateFixture(t, true, &opensIn)
	if _, err := f.uc.UpdateEventExercise(context.Background(), f.eventID, f.attachmentID, &f.nextID, uuid.Must(uuid.NewV7()), false); err != nil {
		t.Fatalf("UpdateEventExercise: %v", err)
	}
	f.requireRecreated(t)
}

func TestUpdateEventExercise_OpenStageStandsNeedConfirmation(t *testing.T) {
	opened := -time.Hour
	f := newStandUpdateFixture(t, true, &opened)
	_, err := f.uc.UpdateEventExercise(context.Background(), f.eventID, f.attachmentID, &f.nextID, uuid.Must(uuid.NewV7()), false)
	if !errors.Is(err, eventExerciseModel.ErrEventExerciseStandsRunning.Err()) || f.unit.saved {
		t.Fatalf("want ErrEventExerciseStandsRunning without saving, got %v", err)
	}
}
