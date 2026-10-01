package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestReorderGroupChallenges_WritesPositionsAcrossSets(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, groupID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	q.EXPECT().ListEventGroupChallengeIDs(gomock.Any(), postgres.ListEventGroupChallengeIDsParams{EventID: eventID, GroupID: uuid.NullUUID{UUID: groupID, Valid: true}}).
		Return([]uuid.UUID{first, second}, nil)
	gomock.InOrder(
		q.EXPECT().SetEventChallengeBoardOrder(gomock.Any(), postgres.SetEventChallengeBoardOrderParams{BoardOrder: pgtype.Int4{Int32: 0, Valid: true}, ID: second, EventID: eventID}).Return(int64(1), nil),
		q.EXPECT().SetEventChallengeBoardOrder(gomock.Any(), postgres.SetEventChallengeBoardOrderParams{BoardOrder: pgtype.Int4{Int32: 1, Valid: true}, ID: first, EventID: eventID}).Return(int64(1), nil),
	)

	if err := uc.ReorderGroupChallenges(context.Background(), eventID, event.ReorderGroupChallengesInput{GroupID: &groupID, ChallengeIDs: []uuid.UUID{second, first}}); err != nil {
		t.Fatalf("ReorderGroupChallenges: %v", err)
	}
	if !unit.saved {
		t.Fatal("reorder transaction was not saved")
	}
}

func TestReorderGroupChallenges_RejectsIncompleteOrder(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	first := uuid.Must(uuid.NewV7())

	// No group: the «Без групи» bucket.
	q.EXPECT().ListEventGroupChallengeIDs(gomock.Any(), postgres.ListEventGroupChallengeIDsParams{EventID: eventID}).
		Return([]uuid.UUID{first, uuid.Must(uuid.NewV7())}, nil)

	err := uc.ReorderGroupChallenges(context.Background(), eventID, event.ReorderGroupChallengesInput{ChallengeIDs: []uuid.UUID{first, first}})
	if !errors.Is(err, eventChallengeModel.ErrEventChallengeBoardOrderInvalid.Err()) || unit.saved {
		t.Fatalf("want ErrEventChallengeBoardOrderInvalid without saving, got %v saved=%t", err, unit.saved)
	}
}

func TestSetEventExerciseVisibility_ShowsTheWholeSet(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, attachmentID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventExerciseByID(gomock.Any(), postgres.GetEventExerciseByIDParams{ID: attachmentID, EventID: eventID}).
		Return(postgres.EventExercise{ID: attachmentID, EventID: eventID, Status: int16(eventExerciseModel.StatusActive), CreatedAt: now}, nil)
	q.EXPECT().ListEventChallenges(gomock.Any(), attachmentID).Return([]postgres.EventChallenge{
		{ID: uuid.Must(uuid.NewV7()), EventExerciseID: attachmentID, Published: true, CreatedAt: now},
		{ID: uuid.Must(uuid.NewV7()), EventExerciseID: attachmentID, Published: false, CreatedAt: now},
	}, nil)
	q.EXPECT().SetEventExerciseChallengesPublished(gomock.Any(), postgres.SetEventExerciseChallengesPublishedParams{Published: true, EventExerciseID: attachmentID}).Return(nil)
	// Newly shown tasks open at once where their assignment is ready (the
	// stand rule: before the strict barrier only static ones).
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, InfrastructureAllowed: true, CreatedAt: now, AvailableFrom: now}, nil)
	q.EXPECT().GetEventStandRollout(gomock.Any(), eventID).Return(postgres.EventStandRollout{}, pgx.ErrNoRows)
	q.EXPECT().PublishAvailableTeamChallenges(gomock.Any(), postgres.PublishAvailableTeamChallengesParams{EventID: eventID, LabsOpen: false}).Return(nil, nil)

	if err := uc.SetEventExerciseVisibility(context.Background(), eventID, attachmentID, true); err != nil {
		t.Fatalf("SetEventExerciseVisibility: %v", err)
	}
}

func TestSetEventExerciseVisibility_NoChangeWritesNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, attachmentID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventExerciseByID(gomock.Any(), gomock.Any()).
		Return(postgres.EventExercise{ID: attachmentID, EventID: eventID, Status: int16(eventExerciseModel.StatusActive), CreatedAt: now}, nil)
	q.EXPECT().ListEventChallenges(gomock.Any(), attachmentID).Return([]postgres.EventChallenge{{ID: uuid.Must(uuid.NewV7()), EventExerciseID: attachmentID, CreatedAt: now}}, nil)

	if err := uc.SetEventExerciseVisibility(context.Background(), eventID, attachmentID, false); err != nil {
		t.Fatalf("SetEventExerciseVisibility: %v", err)
	}
}

// A task the new version adds follows its set's visibility.
func TestUpdateEventExercise_NewTaskFollowsSetVisibility(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, exerciseID, attachmentID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	previousVersionID, nextVersionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	keptTaskID, newTaskID, keptChallengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	link := postgres.EventExercise{ID: attachmentID, EventID: eventID, ExerciseID: exerciseID, ExerciseVersionID: previousVersionID,
		Status: int16(eventExerciseModel.StatusActive), CreatedAt: now}
	variants := []byte(`[{"tasks":[{"id":"` + keptTaskID.String() + `","name":"Kept","difficulty":"easy"},{"id":"` + newTaskID.String() + `","name":"New","difficulty":"easy"}]}]`)

	q.EXPECT().GetEventExerciseByID(gomock.Any(), gomock.Any()).Return(link, nil).Times(2)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), nextVersionID).Return(postgres.ExerciseVersion{ID: nextVersionID, ExerciseID: exerciseID,
		Status: string(exerciseModel.VersionStatusPublished), PublishedAt: pgtype.Timestamptz{Time: now, Valid: true}, Variants: variants, CreatedAt: now}, nil)
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID}, nil).AnyTimes()
	q.EXPECT().IsExerciseAvailableToEvent(gomock.Any(), gomock.Any()).Return(true, nil)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), previousVersionID).Return(postgres.ExerciseVersion{ID: previousVersionID, ExerciseID: exerciseID, Variants: variants, CreatedAt: now}, nil)
	q.EXPECT().ListEventChallenges(gomock.Any(), attachmentID).Return([]postgres.EventChallenge{{ID: keptChallengeID, EventExerciseID: attachmentID, TaskID: keptTaskID,
		Points: 100, Published: true, Snapshot: []byte(`{"name":"Kept","difficulty":"easy"}`), CreatedAt: now}}, nil)
	q.EXPECT().UpdateEventChallengeContent(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().SetEventChallengeHintCosts(gomock.Any(), gomock.Any()).Return(int64(1), nil).AnyTimes()
	q.EXPECT().MaxEventChallengeOrder(gomock.Any(), attachmentID).Return(int32(0), nil)
	q.EXPECT().CreateEventChallenge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventChallengeParams) (postgres.EventChallenge, error) {
		if arg.TaskID != newTaskID || !arg.Published {
			t.Fatalf("new task must follow the shown set: %+v", arg)
		}
		return postgres.EventChallenge{ID: arg.ID, EventExerciseID: arg.EventExerciseID, TaskID: arg.TaskID, Published: arg.Published, CreatedAt: now}, nil
	})
	q.EXPECT().ListTeamChallengesForRefresh(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().UpdateEventExerciseSource(gomock.Any(), gomock.Any()).Return(link, nil)

	if _, err := uc.UpdateEventExercise(context.Background(), eventID, attachmentID, &nextVersionID, uuid.Must(uuid.NewV7())); err != nil {
		t.Fatalf("UpdateEventExercise: %v", err)
	}
}
