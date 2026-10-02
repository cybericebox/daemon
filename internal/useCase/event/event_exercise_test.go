package event_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	challengeAttemptModel "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	event "github.com/cybericebox/daemon/internal/useCase/event"
	errs "github.com/cybericebox/daemon/pkg/err"
)

type readyLabInfra struct{ status exerciseModel.LabDeployStatus }

type staticTopologyResolver struct{}

func (staticTopologyResolver) ResolveDeployedTopology(context.Context, uuid.UUID, int32) (exerciseModel.Topology, error) {
	return exerciseModel.Topology{}, nil
}

type dynamicTopologyResolver struct{ staticTopologyResolver }

func (dynamicTopologyResolver) ResolveVersionTopologies(context.Context, uuid.UUID) ([]exerciseModel.Topology, error) {
	return []exerciseModel.Topology{{Devices: []exerciseModel.Device{{Name: "target"}}}}, nil
}

type unavailableInfrastructureCapability struct{}

func (unavailableInfrastructureCapability) RequireLaboratories(context.Context) error {
	return infraModel.ErrInfrastructureUnavailable.Err()
}

func startedEvent(eventID uuid.UUID, now time.Time) postgres.Event {
	finish := now.Add(time.Hour)
	withdraw := finish.Add(time.Hour)
	return postgres.Event{LifecycleConfigured: true,
		ID: eventID, JoinPolicy: 0,
		PublishAt: now.Add(-2 * time.Hour), StartAt: now.Add(-time.Hour),
		FinishAt:      pgtype.Timestamptz{Time: finish, Valid: true},
		WithdrawAt:    pgtype.Timestamptz{Time: withdraw, Valid: true},
		AvailableFrom: now.Add(-time.Hour), ArchiveAt: pgtype.Timestamptz{Time: withdraw, Valid: true}, CreatedAt: now,
	}
}

type recordingVPNStore struct {
	config string
	userID uuid.UUID
	scope  vpnModel.Scope
	ref    uuid.NullUUID
}

func (s *recordingVPNStore) StoreConfig(_ context.Context, userID uuid.UUID, scope vpnModel.Scope, ref uuid.NullUUID, config string) error {
	s.userID, s.scope, s.ref, s.config = userID, scope, ref, config
	return nil
}
func (s *recordingVPNStore) GetConfig(_ context.Context, userID uuid.UUID, scope vpnModel.Scope, ref uuid.NullUUID) (string, error) {
	s.userID, s.scope, s.ref = userID, scope, ref
	return s.config, nil
}

func (f readyLabInfra) DeployLab(context.Context, string, string, infraModel.LabMeta, exerciseModel.Topology) error {
	return nil
}
func (f readyLabInfra) LabStatus(context.Context, string, string) (exerciseModel.LabDeployStatus, error) {
	return f.status, nil
}
func (f readyLabInfra) EnsureLabClient(context.Context, string, string) (string, error) {
	return "", nil
}
func (readyLabInfra) EnsureVPNGroup(context.Context, string) error  { return nil }
func (readyLabInfra) DestroyLabGroup(context.Context, string) error { return nil }

func TestAttachExercise_PinsPublishedVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	exerciseID := uuid.Must(uuid.NewV7())
	versionID := uuid.Must(uuid.NewV7())
	taskID := uuid.Must(uuid.NewV7())
	hintID := uuid.Must(uuid.NewV7())
	actorID := uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{
		ID: versionID, ExerciseID: exerciseID, Status: string(exerciseModel.VersionStatusPublished), PublishedAt: pgtype.Timestamptz{Time: now, Valid: true},
		Variants: []byte(`[{"id":"00000000-0000-0000-0000-000000000001","index":0,"tasks":[{"id":"` + taskID.String() + `","name":"Task","difficulty":"easy","flag":["ICE{secret}"],"hints":[{"id":"` + hintID.String() + `","text":"Look closer","cost":20,"level":"steps"}]}]}]`), CreatedAt: now,
	}, nil)
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID, Name: "Web"}, nil)
	q.EXPECT().IsExerciseAvailableToEvent(gomock.Any(), postgres.IsExerciseAvailableToEventParams{ExerciseID: exerciseID, EventID: eventID}).Return(true, nil)
	q.EXPECT().EventHasActiveExerciseFamily(gomock.Any(), postgres.EventHasActiveExerciseFamilyParams{EventID: eventID, ExerciseID: exerciseID}).Return(false, nil)
	q.EXPECT().CreateEventExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEventExerciseParams) (postgres.EventExercise, error) {
			if arg.EventID != eventID || arg.ExerciseID != exerciseID || arg.ExerciseVersionID != versionID || arg.VariantMode != int16(eventExerciseModel.VariantModePerTeam) || arg.FixedVariantIndex.Valid || !arg.CreatedBy.Valid || arg.CreatedBy.UUID != actorID {
				t.Fatalf("unexpected event exercise attachment: %+v", arg)
			}
			return postgres.EventExercise{ID: arg.ID, EventID: arg.EventID, ExerciseID: arg.ExerciseID, ExerciseVersionID: arg.ExerciseVersionID, VariantMode: arg.VariantMode, FixedVariantIndex: pgtype.Int4{}, CreatedAt: arg.CreatedAt, CreatedBy: arg.CreatedBy}, nil
		})
	q.EXPECT().CreateEventChallenge(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEventChallengeParams) (postgres.EventChallenge, error) {
			if arg.TaskID != taskID || arg.OrderIndex != 0 || len(arg.Snapshot) == 0 || strings.Contains(string(arg.Snapshot), "ICE{secret}") || strings.Contains(string(arg.Snapshot), "Look closer") {
				t.Fatalf("unexpected materialized challenge: %+v", arg)
			}
			if !strings.Contains(string(arg.Hints), hintID.String()) || !strings.Contains(string(arg.Hints), `"level":"steps"`) || strings.Contains(string(arg.Hints), `"cost"`) {
				t.Fatalf("board hints not materialized: %s", arg.Hints)
			}
			return postgres.EventChallenge{ID: arg.ID, EventExerciseID: arg.EventExerciseID, TaskID: arg.TaskID, OrderIndex: arg.OrderIndex, Points: arg.Points, HintsEnabled: arg.HintsEnabled, Published: arg.Published, Snapshot: arg.Snapshot, CreatedAt: arg.CreatedAt}, nil
		})

	v, err := uc.AttachExercise(context.Background(), eventID, event.AttachExerciseInput{ExerciseVersionID: versionID, VariantMode: eventExerciseModel.VariantModePerTeam}, actorID)
	if err != nil {
		t.Fatalf("AttachExercise: %v", err)
	}
	if v.ExerciseID != exerciseID || v.ExerciseVersionID != versionID || v.ExerciseName != "Web" || !unit.saved {
		t.Fatalf("unexpected attachment view: %+v, unit=%+v", v, unit)
	}
}

func TestAttachExercise_RejectsUnavailableExercise(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}})
	eventID, exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{
		ID: versionID, ExerciseID: exerciseID, Status: string(exerciseModel.VersionStatusPublished), PublishedAt: pgtype.Timestamptz{Time: now, Valid: true},
		Variants: []byte(`[{"tasks":[{"name":"Task","difficulty":"easy"}]}]`), CreatedAt: now,
	}, nil)
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID}, nil)
	q.EXPECT().IsExerciseAvailableToEvent(gomock.Any(), gomock.Any()).Return(false, nil)

	_, err := uc.AttachExercise(context.Background(), eventID, event.AttachExerciseInput{ExerciseVersionID: versionID, VariantMode: eventExerciseModel.VariantModePerTeam}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventExerciseModel.ErrEventExerciseNotAvailable.Err()) {
		t.Fatalf("want ErrEventExerciseNotAvailable, got %v", err)
	}
}

func TestAttachExercise_RejectsArchivedExercise(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}})
	exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{
		ID: versionID, ExerciseID: exerciseID, Status: string(exerciseModel.VersionStatusPublished), PublishedAt: pgtype.Timestamptz{Time: now, Valid: true},
		Variants: []byte(`[{"tasks":[{"name":"Task","difficulty":"easy"}]}]`), CreatedAt: now,
	}, nil)
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{
		ID: exerciseID, ArchivedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)

	_, err := uc.AttachExercise(context.Background(), uuid.Must(uuid.NewV7()), event.AttachExerciseInput{ExerciseVersionID: versionID, VariantMode: eventExerciseModel.VariantModePerTeam}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, exerciseModel.ErrExerciseArchived.Err()) {
		t.Fatalf("want ErrExerciseArchived, got %v", err)
	}
}

// W4 (E4): «Оновити» switches the same attachment in place: the board
// challenge keeps its event overrides, its snapshot is refreshed and team
// assignments whose visible content changed are marked «Оновлено».
func TestUpdateEventExercise_SwitchesInPlaceKeepingOverrides(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, exerciseID, attachmentID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	previousVersionID, nextVersionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	taskID, challengeID, teamChallengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	link := postgres.EventExercise{ID: attachmentID, EventID: eventID, ExerciseID: exerciseID, ExerciseVersionID: previousVersionID,
		VariantMode: int16(eventExerciseModel.VariantModePerTeam), Revision: 1, Status: int16(eventExerciseModel.StatusActive), CreatedAt: now}
	variants := func(name string) []byte {
		return []byte(`[{"tasks":[{"id":"` + taskID.String() + `","name":"` + name + `","difficulty":"easy","flag":["ICE{same}"]}]}]`)
	}

	q.EXPECT().GetEventExerciseByID(gomock.Any(), postgres.GetEventExerciseByIDParams{ID: attachmentID, EventID: eventID}).Return(link, nil).Times(2)
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID, Name: "Web", PublishedVersionID: uuid.NullUUID{UUID: nextVersionID, Valid: true}}, nil).Times(2)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), nextVersionID).Return(postgres.ExerciseVersion{ID: nextVersionID, ExerciseID: exerciseID,
		Status: string(exerciseModel.VersionStatusPublished), PublishedAt: pgtype.Timestamptz{Time: now, Valid: true}, Variants: variants("Renamed task"), CreatedAt: now}, nil)
	q.EXPECT().IsExerciseAvailableToEvent(gomock.Any(), gomock.Any()).Return(true, nil)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), previousVersionID).Return(postgres.ExerciseVersion{ID: previousVersionID, ExerciseID: exerciseID,
		Status: string(exerciseModel.VersionStatusUnpublished), PublishedAt: pgtype.Timestamptz{Time: now, Valid: true}, Variants: variants("Old task"), CreatedAt: now}, nil)
	q.EXPECT().ListEventChallenges(gomock.Any(), attachmentID).Return([]postgres.EventChallenge{{ID: challengeID, EventExerciseID: attachmentID, TaskID: taskID,
		OrderIndex: 3, Points: 250, Published: true, Snapshot: []byte(`{"name":"Old task","difficulty":"easy"}`), CreatedAt: now}}, nil)
	q.EXPECT().UpdateEventChallengeContent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventChallengeContentParams) (int64, error) {
		if arg.ID != challengeID || !strings.Contains(string(arg.Snapshot), "Renamed task") {
			t.Fatalf("unexpected challenge refresh: %+v", arg)
		}
		return 1, nil
	})
	q.EXPECT().SetEventChallengeHintCosts(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().MaxEventChallengeOrder(gomock.Any(), attachmentID).Return(int32(3), nil)
	q.EXPECT().ListTeamChallengesForRefresh(gomock.Any(), []uuid.UUID{challengeID}).Return([]postgres.ListTeamChallengesForRefreshRow{{
		ID: teamChallengeID, EventChallengeID: challengeID, VariantIndex: 0, Snapshot: []byte(`{"name":"Old task","difficulty":"easy"}`), Hints: []byte(`[]`), ExpectedFlag: "ICE{same}"}}, nil)
	q.EXPECT().UpdateTeamChallengeContent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateTeamChallengeContentParams) (int64, error) {
		if arg.ID != teamChallengeID || !arg.ContentChanged || arg.ExpectedFlag != "ICE{same}" || !strings.Contains(string(arg.Snapshot), "Renamed task") {
			t.Fatalf("unexpected team refresh: %+v", arg)
		}
		return 1, nil
	})
	q.EXPECT().UpdateEventExerciseSource(gomock.Any(), postgres.UpdateEventExerciseSourceParams{ID: attachmentID, EventID: eventID, ExerciseID: exerciseID, ExerciseVersionID: nextVersionID}).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateEventExerciseSourceParams) (postgres.EventExercise, error) {
			updated := link
			updated.ExerciseVersionID, updated.Revision = arg.ExerciseVersionID, 2
			return updated, nil
		})

	view, err := uc.UpdateEventExercise(context.Background(), eventID, attachmentID, nil, uuid.Must(uuid.NewV7()))
	if err != nil {
		t.Fatalf("UpdateEventExercise: %v", err)
	}
	if view.ID != attachmentID || view.ExerciseVersionID != nextVersionID || view.Revision != 2 || !unit.saved {
		t.Fatalf("unexpected view: %+v unit=%+v", view, unit)
	}
}

func TestUpdateEventExercise_RefusesRemovingAttemptedTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, exerciseID, attachmentID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	previousVersionID, nextVersionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	removedChallengeID := uuid.Must(uuid.NewV7())
	now := time.Now()
	link := postgres.EventExercise{ID: attachmentID, EventID: eventID, ExerciseID: exerciseID, ExerciseVersionID: previousVersionID, Status: int16(eventExerciseModel.StatusActive), CreatedAt: now}
	q.EXPECT().GetEventExerciseByID(gomock.Any(), gomock.Any()).Return(link, nil).Times(2)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), nextVersionID).Return(postgres.ExerciseVersion{ID: nextVersionID, ExerciseID: exerciseID,
		Status: string(exerciseModel.VersionStatusPublished), PublishedAt: pgtype.Timestamptz{Time: now, Valid: true}, Variants: []byte(`[{"tasks":[]}]`), CreatedAt: now}, nil)
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID}, nil)
	q.EXPECT().IsExerciseAvailableToEvent(gomock.Any(), gomock.Any()).Return(true, nil)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), previousVersionID).Return(postgres.ExerciseVersion{ID: previousVersionID, ExerciseID: exerciseID, Variants: []byte(`[{"tasks":[]}]`), CreatedAt: now}, nil)
	q.EXPECT().ListEventChallenges(gomock.Any(), attachmentID).Return([]postgres.EventChallenge{{ID: removedChallengeID, EventExerciseID: attachmentID, TaskID: uuid.Must(uuid.NewV7()), CreatedAt: now}}, nil)
	q.EXPECT().ListEventChallengesWithAttempts(gomock.Any(), []uuid.UUID{removedChallengeID}).Return([]uuid.UUID{removedChallengeID}, nil)

	_, err := uc.UpdateEventExercise(context.Background(), eventID, attachmentID, &nextVersionID, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventExerciseModel.ErrEventExerciseTaskHasAttempts.Err()) || unit.saved {
		t.Fatalf("want ErrEventExerciseTaskHasAttempts without saving, got %v saved=%t", err, unit.saved)
	}
}

func TestDetachEventExercise_AttemptedNeedsConfirmation(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, attachmentID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	link := postgres.EventExercise{ID: attachmentID, EventID: eventID, Status: int16(eventExerciseModel.StatusActive), CreatedAt: time.Now()}
	q.EXPECT().GetEventExerciseByID(gomock.Any(), gomock.Any()).Return(link, nil)
	q.EXPECT().EventExerciseHasAttempts(gomock.Any(), attachmentID).Return(true, nil)

	err := uc.DetachEventExercise(context.Background(), eventID, attachmentID, false, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventExerciseModel.ErrEventExerciseDetachNeedsConfirm.Err()) || unit.saved {
		t.Fatalf("want ErrEventExerciseDetachNeedsConfirm, got %v", err)
	}
}

func TestDetachEventExercise_UnattemptedIsDeleted(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, attachmentID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	link := postgres.EventExercise{ID: attachmentID, EventID: eventID, Status: int16(eventExerciseModel.StatusActive), CreatedAt: time.Now()}
	q.EXPECT().GetEventExerciseByID(gomock.Any(), gomock.Any()).Return(link, nil)
	q.EXPECT().EventExerciseHasAttempts(gomock.Any(), attachmentID).Return(false, nil)
	q.EXPECT().ListEventChallenges(gomock.Any(), attachmentID).Return([]postgres.EventChallenge{{ID: challengeID, EventExerciseID: attachmentID}}, nil)
	q.EXPECT().DeleteLabBindingsForChallenges(gomock.Any(), []uuid.UUID{challengeID}).Return(nil)
	q.EXPECT().DeleteTeamChallengesForChallenges(gomock.Any(), []uuid.UUID{challengeID}).Return(nil)
	q.EXPECT().DeleteEventChallengesByIDs(gomock.Any(), postgres.DeleteEventChallengesByIDsParams{Ids: []uuid.UUID{challengeID}, EventExerciseID: attachmentID}).Return(nil)
	q.EXPECT().DeleteEventExercise(gomock.Any(), postgres.DeleteEventExerciseParams{ID: attachmentID, EventID: eventID}).Return(int64(1), nil)

	if err := uc.DetachEventExercise(context.Background(), eventID, attachmentID, false, uuid.Must(uuid.NewV7())); err != nil || !unit.saved {
		t.Fatalf("detach: err=%v saved=%t", err, unit.saved)
	}
}

func TestUpdateEventChallenge_ChangesOnlyActiveRevision(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	attachmentID := uuid.Must(uuid.NewV7())
	challengeID := uuid.Must(uuid.NewV7())
	taskID := uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().GetEventExerciseByID(gomock.Any(), postgres.GetEventExerciseByIDParams{ID: attachmentID, EventID: eventID}).Return(postgres.EventExercise{ID: attachmentID, EventID: eventID, Status: int16(eventExerciseModel.StatusActive), CreatedAt: now}, nil)
	q.EXPECT().GetEventChallengeByID(gomock.Any(), postgres.GetEventChallengeByIDParams{ID: challengeID, EventExerciseID: attachmentID}).Return(postgres.EventChallenge{ID: challengeID, EventExerciseID: attachmentID, TaskID: taskID, Points: 100, CreatedAt: now}, nil)
	q.EXPECT().UpdateEventChallenge(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateEventChallengeParams) (postgres.EventChallenge, error) {
			// Visibility is per set: a task update keeps it.
			if arg.ID != challengeID || arg.EventExerciseID != attachmentID || arg.Points != 250 || !arg.HintsEnabled || arg.Published {
				t.Fatalf("unexpected board update: %+v", arg)
			}
			return postgres.EventChallenge{ID: arg.ID, EventExerciseID: arg.EventExerciseID, TaskID: taskID, Points: arg.Points, HintsEnabled: arg.HintsEnabled, Published: arg.Published, CreatedAt: now}, nil
		})
	view, err := uc.UpdateEventChallenge(context.Background(), eventID, attachmentID, challengeID, event.UpdateEventChallengeInput{Points: 250, HintsEnabled: true})
	if err != nil {
		t.Fatalf("UpdateEventChallenge: %v", err)
	}
	if view.Points != 250 || !view.HintsEnabled || view.Published {
		t.Fatalf("unexpected challenge view: %+v", view)
	}
}

func TestListEventChallenges_IncludesStoredRelations(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	attachmentID := uuid.Must(uuid.NewV7())
	challengeID := uuid.Must(uuid.NewV7())
	groupID := uuid.Must(uuid.NewV7())
	prerequisiteID := uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().GetEventExerciseByID(gomock.Any(), postgres.GetEventExerciseByIDParams{ID: attachmentID, EventID: eventID}).Return(postgres.EventExercise{ID: attachmentID, EventID: eventID, CreatedAt: now}, nil)
	q.EXPECT().ListEventChallenges(gomock.Any(), attachmentID).Return([]postgres.EventChallenge{{ID: challengeID, EventExerciseID: attachmentID, GroupID: uuid.NullUUID{UUID: groupID, Valid: true}, CreatedAt: now}}, nil)
	q.EXPECT().ListEventChallengeAvailability(gomock.Any(), attachmentID).Return([]postgres.ListEventChallengeAvailabilityRow{{EventChallengeID: challengeID}}, nil)
	// W4 (E7): one query for every prerequisite edge of the board revision.
	q.EXPECT().ListEventExercisePrerequisites(gomock.Any(), attachmentID).Return([]postgres.EventChallengePrerequisite{{ChallengeID: challengeID, PrerequisiteChallengeID: prerequisiteID}}, nil)

	views, err := uc.ListEventChallenges(context.Background(), eventID, attachmentID)
	if err != nil {
		t.Fatalf("ListEventChallenges: %v", err)
	}
	if len(views) != 1 || views[0].GroupID == nil || *views[0].GroupID != groupID || len(views[0].PrerequisiteIDs) != 1 || views[0].PrerequisiteIDs[0] != prerequisiteID {
		t.Fatalf("unexpected challenge relations: %+v", views)
	}
}

func TestListEventChallengesAvailability(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, attachmentID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventExerciseByID(gomock.Any(), postgres.GetEventExerciseByIDParams{ID: attachmentID, EventID: eventID}).Return(postgres.EventExercise{ID: attachmentID, EventID: eventID, CreatedAt: now}, nil)
	q.EXPECT().ListEventChallenges(gomock.Any(), attachmentID).Return([]postgres.EventChallenge{{ID: challengeID, EventExerciseID: attachmentID, Published: true, CreatedAt: now}}, nil)
	q.EXPECT().ListEventChallengeAvailability(gomock.Any(), attachmentID).Return([]postgres.ListEventChallengeAvailabilityRow{{EventChallengeID: challengeID, Preparing: 2, Available: 3, Total: 5}}, nil)
	q.EXPECT().ListEventExercisePrerequisites(gomock.Any(), attachmentID).Return(nil, nil)

	values, err := uc.ListEventChallenges(context.Background(), eventID, attachmentID)
	if err != nil {
		t.Fatalf("ListEventChallenges: %v", err)
	}
	if len(values) != 1 || values[0].Availability.Preparing != 2 || values[0].Availability.Available != 3 || values[0].Availability.Total != 5 {
		t.Fatalf("unexpected availability: %+v", values)
	}
}

func TestUpdateEventChallengeRelations_RejectsCycle(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	attachmentID := uuid.Must(uuid.NewV7())
	firstID := uuid.Must(uuid.NewV7())
	secondID := uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().GetEventExerciseByID(gomock.Any(), postgres.GetEventExerciseByIDParams{ID: attachmentID, EventID: eventID}).Return(postgres.EventExercise{ID: attachmentID, EventID: eventID, Status: int16(eventExerciseModel.StatusActive), CreatedAt: now}, nil)
	q.EXPECT().ListEventChallenges(gomock.Any(), attachmentID).Return([]postgres.EventChallenge{{ID: firstID, EventExerciseID: attachmentID, CreatedAt: now}, {ID: secondID, EventExerciseID: attachmentID, CreatedAt: now}}, nil)
	q.EXPECT().ListEventChallengePrerequisites(gomock.Any(), secondID).Return([]uuid.UUID{firstID}, nil)

	err := uc.UpdateEventChallengeRelations(context.Background(), eventID, attachmentID, firstID, event.UpdateEventChallengeRelationsInput{PrerequisiteIDs: []uuid.UUID{secondID}})
	if err == nil {
		t.Fatal("expected prerequisite cycle to be rejected")
	}
	if unit.saved {
		t.Fatal("cyclic prerequisite transaction was saved")
	}
}

func TestSubmitChallenge_RecordsFirstCorrectSolve(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, userID, teamID, challengeID, teamChallengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	key := uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, CreatedAt: now}, nil)
	q.EXPECT().ReserveRequestIdempotency(gomock.Any(), gomock.Any()).Return([]postgres.RequestIdempotency{{}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: challengeID}).Return(postgres.GetTeamChallengeRow{ID: teamChallengeID, EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID, ExpectedFlag: "ICE{ok}", Readiness: 2, CreatedAt: now}, nil)
	q.EXPECT().IsEventChallengePublished(gomock.Any(), challengeID).Return(true, nil)
	q.EXPECT().ListEventChallengePrerequisites(gomock.Any(), challengeID).Return(nil, nil)
	expectAttemptWindows(q, eventID, teamID, challengeID, teamChallengeID, now, 4, 19)
	q.EXPECT().CreateChallengeAttempt(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateChallengeAttemptParams) (postgres.ChallengeAttempt, error) {
		return postgres.ChallengeAttempt{ID: p.ID, EventID: p.EventID, EventTeamID: p.EventTeamID, TeamChallengeID: p.TeamChallengeID, UserID: p.UserID, Answer: p.Answer, Correct: p.Correct, ReceivedAt: p.ReceivedAt, CreatedAt: p.CreatedAt}, nil
	})
	q.EXPECT().GetEffectiveTeamChallengeSolvedAt(gomock.Any(), teamChallengeID).Return(postgres.GetEffectiveTeamChallengeSolvedAtRow{Solved: false}, nil)
	q.EXPECT().DeleteTeamChallengeSolve(gomock.Any(), teamChallengeID).Return(nil)
	q.EXPECT().GetEffectiveTeamChallengeSolvedAt(gomock.Any(), teamChallengeID).Return(postgres.GetEffectiveTeamChallengeSolvedAtRow{Solved: true, SolvedAt: now}, nil)
	q.EXPECT().GetTeamChallengeScoringContext(gomock.Any(), teamChallengeID).Return(postgres.GetTeamChallengeScoringContextRow{EventID: eventID, EventChallengeID: challengeID, StaticPoints: 100, EventScoringMode: 0, StartAt: now}, nil)
	q.EXPECT().UpsertTeamChallengeSolve(gomock.Any(), postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: teamChallengeID, SolvedAt: now, AwardedPoints: pgtype.Int4{Int32: 100, Valid: true}}).Return(nil)
	q.EXPECT().GetEventTeamVisible(gomock.Any(), postgres.GetEventTeamVisibleParams{ID: teamID, EventID: eventID}).Return(true, nil)
	q.EXPECT().AdvanceEventResultRevision(gomock.Any(), gomock.Any()).Return(postgres.AdvanceEventResultRevisionRow{Revision: 1, UpdatedAt: now}, nil)
	q.EXPECT().CreateEventResultChange(gomock.Any(), gomock.Any()).Return(postgres.EventResultChange{EventID: eventID, Revision: 1, Kind: "team_challenge_solved", CreatedAt: now}, nil)
	q.EXPECT().CompleteRequestIdempotency(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	r, err := uc.SubmitChallenge(context.Background(), eventID, userID, challengeID, event.SubmitChallengeInput{Answer: "ICE{ok}", IdempotencyKey: key, ReceivedAt: now})
	if err != nil || !r.Correct || !r.FirstSolve || !unit.saved {
		t.Fatalf("submit=%+v err=%v saved=%v", r, err, unit.saved)
	}
}

// expectAttemptWindows stubs the rate-limit lock and both window counts.
func expectAttemptWindows(q *postgresMocks.MockQuerier, eventID, teamID, challengeID, teamChallengeID uuid.UUID, now time.Time, challengeAttempts, teamAttempts int64) {
	q.EXPECT().LockEventTeamChallenge(gomock.Any(), postgres.LockEventTeamChallengeParams{EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID}).Return(teamChallengeID, nil)
	q.EXPECT().GetTeamChallengeAttemptWindow(gomock.Any(), postgres.GetTeamChallengeAttemptWindowParams{TeamChallengeID: teamChallengeID, Since: now.Add(-30 * time.Second), RowLimit: 5}).Return(postgres.GetTeamChallengeAttemptWindowRow{Attempts: challengeAttempts, Oldest: now.Add(-20 * time.Second)}, nil)
	if challengeAttempts < 5 {
		q.EXPECT().GetTeamAttemptWindow(gomock.Any(), postgres.GetTeamAttemptWindowParams{EventID: eventID, EventTeamID: teamID, Since: now.Add(-time.Minute), RowLimit: 20}).Return(postgres.GetTeamAttemptWindowRow{Attempts: teamAttempts, Oldest: now.Add(-15 * time.Second)}, nil)
	}
}

func TestSubmitChallenge_RateLimited(t *testing.T) {
	for name, tc := range map[string]struct {
		challengeAttempts, teamAttempts int64
		retryAfter                      int64
	}{
		"per challenge": {challengeAttempts: 5, retryAfter: 10},
		"per team":      {challengeAttempts: 1, teamAttempts: 20, retryAfter: 45},
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			unit := &testUoW{}
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
			eventID, userID, teamID, challengeID, teamChallengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
			q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, CreatedAt: now}, nil).Times(2)
			q.EXPECT().ReserveRequestIdempotency(gomock.Any(), gomock.Any()).Return([]postgres.RequestIdempotency{{}}, nil)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
			q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: challengeID}).Return(postgres.GetTeamChallengeRow{ID: teamChallengeID, EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID, ExpectedFlag: "ICE{ok}", Readiness: 2, CreatedAt: now}, nil)
			q.EXPECT().IsEventChallengePublished(gomock.Any(), challengeID).Return(true, nil)
			q.EXPECT().ListEventChallengePrerequisites(gomock.Any(), challengeID).Return(nil, nil)
			expectAttemptWindows(q, eventID, teamID, challengeID, teamChallengeID, now, tc.challengeAttempts, tc.teamAttempts)
			expectRejectionLogged(q, eventID, userID, teamID, challengeID, now, "rate_limit")

			_, err := uc.SubmitChallenge(context.Background(), eventID, userID, challengeID, event.SubmitChallengeInput{Answer: "ICE{ok}", IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: now})
			if !errors.Is(err, challengeAttemptModel.ErrTooManyAttempts.Err()) || unit.saved {
				t.Fatalf("err=%v saved=%v", err, unit.saved)
			}
			var appErr errs.Error
			if !errors.As(err, &appErr) || appErr.StatusCode().Details()[errs.DetailRetryAfterSeconds] != tc.retryAfter {
				t.Fatalf("retry after: %v", err)
			}
		})
	}
}

func TestSubmitChallenge_ReplaysCompletedIdempotencyResult(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, userID, challengeID, key := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	hash := sha256.Sum256([]byte(eventID.String() + "\x00" + challengeID.String() + "\x00ICE{ok}"))

	q.EXPECT().ReserveRequestIdempotency(gomock.Any(), gomock.Any()).Return([]postgres.RequestIdempotency{}, nil)
	q.EXPECT().GetRequestIdempotency(gomock.Any(), gomock.Any()).Return(postgres.RequestIdempotency{OwnerID: userID, Key: key, RequestHash: hash[:], Completed: true, ResponseStatus: pgtype.Int4{Int32: 200, Valid: true}, ResponseBody: []byte(`{"correct":true,"firstSolve":true}`)}, nil)

	v, err := uc.SubmitChallenge(context.Background(), eventID, userID, challengeID, event.SubmitChallengeInput{Answer: "ICE{ok}", IdempotencyKey: key, ReceivedAt: time.Now()})
	if err != nil {
		t.Fatalf("SubmitChallenge: %v", err)
	}
	if !v.Correct || !v.FirstSolve || unit.saved {
		t.Fatalf("replayed submit = %+v, unit = %+v", v, unit)
	}
}

func TestSubmitChallenge_RejectsUnsolvedPrerequisite(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, userID, teamID, challengeID, teamChallengeID, prerequisiteID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	key := uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, CreatedAt: now}, nil).Times(2)
	q.EXPECT().ReserveRequestIdempotency(gomock.Any(), gomock.Any()).Return([]postgres.RequestIdempotency{{}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: challengeID}).Return(postgres.GetTeamChallengeRow{ID: teamChallengeID, EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID, ExpectedFlag: "ICE{ok}", Readiness: 2, CreatedAt: now}, nil)
	q.EXPECT().IsEventChallengePublished(gomock.Any(), challengeID).Return(true, nil)
	q.EXPECT().ListEventChallengePrerequisites(gomock.Any(), challengeID).Return([]uuid.UUID{prerequisiteID}, nil)
	q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: prerequisiteID}).Return(postgres.GetTeamChallengeRow{EventTeamID: teamID, EventChallengeID: prerequisiteID}, nil)
	expectRejectionLogged(q, eventID, userID, teamID, challengeID, now, "locked")
	if _, err := uc.SubmitChallenge(context.Background(), eventID, userID, challengeID, event.SubmitChallengeInput{Answer: "ICE{ok}", IdempotencyKey: key, ReceivedAt: now}); err == nil || unit.saved {
		t.Fatalf("err=%v saved=%v", err, unit.saved)
	}
}

func TestSubmitChallenge_RejectsUnpublishedBoardChallenge(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, userID, teamID, challengeID, teamChallengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	key := uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, CreatedAt: now}, nil).Times(2)
	q.EXPECT().ReserveRequestIdempotency(gomock.Any(), gomock.Any()).Return([]postgres.RequestIdempotency{{}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: challengeID}).Return(postgres.GetTeamChallengeRow{ID: teamChallengeID, EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID, ExpectedFlag: "ICE{ok}", Readiness: 2, CreatedAt: now}, nil)
	q.EXPECT().IsEventChallengePublished(gomock.Any(), challengeID).Return(false, nil)
	expectRejectionLogged(q, eventID, userID, teamID, challengeID, now, "unavailable")
	if _, err := uc.SubmitChallenge(context.Background(), eventID, userID, challengeID, event.SubmitChallengeInput{Answer: "ICE{ok}", IdempotencyKey: key, ReceivedAt: now}); err == nil || unit.saved {
		t.Fatalf("err=%v saved=%v", err, unit.saved)
	}
}

func TestGetOwnChallengeLabStatus_RejectsPreparingChallenge(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, userID, teamID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(2), TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, CreatedAt: now}, nil)
	q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: challengeID}).Return(postgres.GetTeamChallengeRow{EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID, Readiness: int16(0), CreatedAt: now}, nil)

	if _, err := uc.GetOwnChallengeLabStatus(context.Background(), eventID, userID, challengeID); err == nil {
		t.Fatal("preparing challenge exposed a participant lab status")
	}
}

func TestGetOwnLabVPNConfig_UsesCallerAndEventScope(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	store := &recordingVPNStore{config: "wg-personal"}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, VPN: store})
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, CreatedAt: time.Now()}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, InfrastructureAllowed: true}, nil)
	config, err := uc.GetOwnLabVPNConfig(context.Background(), eventID, userID)
	if err != nil || config != "wg-personal" || store.userID != userID || store.scope != vpnModel.ScopeEvent || !store.ref.Valid || store.ref.UUID != eventID {
		t.Fatalf("config=%q err=%v store=%+v", config, err, store)
	}
}

func TestReorderEventChallenges_VacatesBeforeSwapping(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	attachmentID := uuid.Must(uuid.NewV7())
	firstID := uuid.Must(uuid.NewV7())
	secondID := uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().GetEventExerciseByID(gomock.Any(), postgres.GetEventExerciseByIDParams{ID: attachmentID, EventID: eventID}).Return(postgres.EventExercise{ID: attachmentID, EventID: eventID, Status: int16(eventExerciseModel.StatusActive), CreatedAt: now}, nil)
	q.EXPECT().ListEventChallenges(gomock.Any(), attachmentID).Return([]postgres.EventChallenge{{ID: firstID, EventExerciseID: attachmentID, OrderIndex: 0, CreatedAt: now}, {ID: secondID, EventExerciseID: attachmentID, OrderIndex: 1, CreatedAt: now}}, nil)
	q.EXPECT().VacateEventChallengeOrders(gomock.Any(), attachmentID).Return(nil)
	q.EXPECT().SetEventChallengeOrder(gomock.Any(), postgres.SetEventChallengeOrderParams{ID: secondID, EventExerciseID: attachmentID, OrderIndex: 0}).Return(int64(1), nil)
	q.EXPECT().SetEventChallengeOrder(gomock.Any(), postgres.SetEventChallengeOrderParams{ID: firstID, EventExerciseID: attachmentID, OrderIndex: 1}).Return(int64(1), nil)

	if err := uc.ReorderEventChallenges(context.Background(), eventID, attachmentID, event.ReorderEventChallengesInput{ChallengeIDs: []uuid.UUID{secondID, firstID}}); err != nil {
		t.Fatalf("ReorderEventChallenges: %v", err)
	}
	if !unit.saved {
		t.Fatal("reorder transaction was not saved")
	}
}

// W4: the event catalog is its own query (no tag filter, so the E0 NULL-tags
// bug cannot return); it is scoped to the event and passes the filters.
func TestListPublishedExercisesForEvent_ScopesToEventAndFilters(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	q.EXPECT().ListEventCatalog(gomock.Any(), postgres.ListEventCatalogParams{EventID: eventID, Search: "web", Infrastructure: "yes", Tags: []string{"web", "crypto"}}).
		Return([]postgres.ListEventCatalogRow{{ID: exerciseID, Name: "Web", Scope: 1, PublishedVersionID: versionID, Infrastructure: true}}, nil)
	q.EXPECT().ListVersionVariantDevices(gomock.Any(), []uuid.UUID{versionID}).Return([]postgres.ListVersionVariantDevicesRow{{
		VersionID: versionID, ExerciseID: exerciseID,
		Variants: []byte(`[{"id":"` + uuid.Must(uuid.NewV7()).String() + `","topology":{"devices":[{"id":"` + uuid.Must(uuid.NewV7()).String() + `","name":"web","type":"container","resource_preset":"small"}]}}]`),
	}}, nil)

	items, err := uc.ListPublishedExercisesForEvent(context.Background(), eventID, " web ", "yes", []string{" Web", "crypto", "WEB", " "})
	if err != nil {
		t.Fatalf("ListPublishedExercisesForEvent: %v", err)
	}
	if len(items) != 1 || items[0].PublishedVersionID != versionID || items[0].Scope != "event" || !items[0].Infrastructure {
		t.Fatalf("items = %+v", items)
	}
	// The picker shows the task's total resources: one small device (50m / 128Mi); not resource-heavy.
	if r := items[0].Resources; r.Min != r.Max || r.Max.Devices != 1 || r.Max.CPUMillicores != 50 || r.Max.MemoryBytes != 128<<20 || items[0].ResourceHeavy {
		t.Fatalf("resources = %+v heavy=%v", items[0].Resources, items[0].ResourceHeavy)
	}
}

func TestListEventCatalogTags_ScopesToEventAndClampsLimit(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().ListEventCatalogTags(gomock.Any(), postgres.ListEventCatalogTagsParams{EventID: eventID, Prefix: "we", LimitVal: 50}).
		Return([]postgres.ListEventCatalogTagsRow{{Tag: "web", ExerciseCount: 4}}, nil)
	items, err := uc.ListEventCatalogTags(context.Background(), eventID, " WE ", 0)
	if err != nil || len(items) != 1 || items[0].Tag != "web" || items[0].ExerciseCount != 4 {
		t.Fatalf("items=%+v err=%v", items, err)
	}

	q.EXPECT().ListEventCatalogTags(gomock.Any(), postgres.ListEventCatalogTagsParams{EventID: eventID, LimitVal: 200}).
		Return(nil, nil)
	items, err = uc.ListEventCatalogTags(context.Background(), eventID, "", 5000)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("empty items=%#v err=%v", items, err)
	}
}
