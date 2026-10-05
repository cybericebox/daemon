package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

type hintFixture struct {
	eventID, userID, teamID, challengeID, teamChallengeID, hintID uuid.UUID
	now                                                           time.Time
}

func newHintFixture() hintFixture {
	return hintFixture{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()),
		uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), time.Now()}
}

// W4: a paid unlock in balance mode charges the event cost override at once
// and tells live results to reload; the unlocked text is returned.
func TestUnlockHint_BalanceModeChargesOverrideAndRecalculates(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	f := newHintFixture()
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: f.eventID, UserID: f.userID}).
		Return(postgres.EventParticipant{EventID: f.eventID, UserID: f.userID, Status: 2, TeamID: uuid.NullUUID{UUID: f.teamID, Valid: true}, CreatedAt: f.now}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), f.eventID).Return(startedEvent(f.eventID, f.now), nil)
	q.EXPECT().GetEventConfig(gomock.Any(), f.eventID).Return(postgres.EventConfig{EventID: f.eventID, HintChargeMode: 1}, nil)
	q.EXPECT().GetTeamChallengeHints(gomock.Any(), gomock.Any()).
		Return(postgres.GetTeamChallengeHintsRow{ID: f.teamChallengeID, EventID: f.eventID, EventTeamID: f.teamID, EventChallengeID: f.challengeID, Readiness: 2, StagePhase: 1,
			TeamHints: []byte(`[{"id":"` + f.hintID.String() + `","text":"Variant text"}]`), BoardHints: []byte(`[{"id":"` + f.hintID.String() + `","cost":20,"text":"Canonical"}]`),
			HintCosts: []byte(`{"` + f.hintID.String() + `":35}`), HintsEnabled: true, Published: true}, nil)
	q.EXPECT().ListTeamChallengePrerequisites(gomock.Any(), f.teamID).Return(nil, nil)
	q.EXPECT().CreateHintUnlock(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateHintUnlockParams) (postgres.CreateHintUnlockRow, error) {
		if arg.Cost != 35 || arg.HintID != f.hintID || arg.TeamChallengeID != f.teamChallengeID || !arg.UnlockedBy.Valid || arg.UnlockedBy.UUID != f.userID {
			t.Fatalf("unexpected unlock: %+v", arg)
		}
		return postgres.CreateHintUnlockRow{TeamChallengeID: arg.TeamChallengeID, HintID: arg.HintID, UnlockedBy: arg.UnlockedBy, UnlockedAt: arg.UnlockedAt, Cost: arg.Cost, Created: true}, nil
	})
	q.EXPECT().AdvanceEventResultRevision(gomock.Any(), gomock.Any()).Return(postgres.AdvanceEventResultRevisionRow{Revision: 2}, nil)
	q.EXPECT().CreateEventResultChange(gomock.Any(), gomock.Any()).Return(postgres.EventResultChange{EventID: f.eventID, Revision: 2}, nil)

	hint, err := uc.UnlockHint(context.Background(), f.eventID, f.userID, f.challengeID, f.hintID)
	if err != nil {
		t.Fatalf("UnlockHint: %v", err)
	}
	if !hint.Unlocked || hint.Cost != 35 || hint.Content == nil || *hint.Content != "Variant text" || !unit.saved {
		t.Fatalf("hint=%+v saved=%t", hint, unit.saved)
	}
}

func TestUnlockHint_RefusesDisabledHints(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	f := newHintFixture()
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).
		Return(postgres.EventParticipant{EventID: f.eventID, UserID: f.userID, Status: 2, TeamID: uuid.NullUUID{UUID: f.teamID, Valid: true}, CreatedAt: f.now}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), f.eventID).Return(startedEvent(f.eventID, f.now), nil)
	q.EXPECT().GetEventConfig(gomock.Any(), f.eventID).Return(postgres.EventConfig{EventID: f.eventID}, nil)
	q.EXPECT().GetTeamChallengeHints(gomock.Any(), gomock.Any()).
		Return(postgres.GetTeamChallengeHintsRow{ID: f.teamChallengeID, EventID: f.eventID, EventTeamID: f.teamID, EventChallengeID: f.challengeID, Readiness: 2, StagePhase: 1, Published: true}, nil)

	_, err := uc.UnlockHint(context.Background(), f.eventID, f.userID, f.challengeID, f.hintID)
	if !errors.Is(err, eventChallengeModel.ErrEventChallengeHintsDisabled.Err()) || unit.saved {
		t.Fatalf("want ErrEventChallengeHintsDisabled, got %v", err)
	}
}

// The event can only hide hints: a task that enables them stays closed while
// «Вимкнути підказки для всіх завдань» is on.
func TestUnlockHint_RefusesWhenEventDisablesHints(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	f := newHintFixture()
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).
		Return(postgres.EventParticipant{EventID: f.eventID, UserID: f.userID, Status: 2, TeamID: uuid.NullUUID{UUID: f.teamID, Valid: true}, CreatedAt: f.now}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), f.eventID).Return(startedEvent(f.eventID, f.now), nil)
	q.EXPECT().GetEventConfig(gomock.Any(), f.eventID).Return(postgres.EventConfig{EventID: f.eventID, HintsDisabled: true}, nil)
	q.EXPECT().GetTeamChallengeHints(gomock.Any(), gomock.Any()).
		Return(postgres.GetTeamChallengeHintsRow{ID: f.teamChallengeID, EventID: f.eventID, EventTeamID: f.teamID, EventChallengeID: f.challengeID, Readiness: 2, StagePhase: 1, Published: true, HintsEnabled: true}, nil)

	_, err := uc.UnlockHint(context.Background(), f.eventID, f.userID, f.challengeID, f.hintID)
	if !errors.Is(err, eventChallengeModel.ErrEventChallengeHintsDisabled.Err()) || unit.saved {
		t.Fatalf("want ErrEventChallengeHintsDisabled, got %v", err)
	}
}

// L20: a hint costs points; like a submission it needs the blocking required forms filled in.
func TestUnlockHint_RefusedWhileRequiredFormsAreBlocking(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	stubParticipationModelReads(q)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	f := newHintFixture()
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).
		Return(postgres.EventParticipant{EventID: f.eventID, UserID: f.userID, Status: 2, TeamID: uuid.NullUUID{UUID: f.teamID, Valid: true}, CreatedAt: f.now}, nil)
	q.EXPECT().GetEventParticipantFieldsMissing(gomock.Any(), gomock.Any()).Return(int32(1), nil)
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), f.eventID).Return(postgres.EventFormVersion{
		EventID: f.eventID, Version: 2, Enabled: true, Required: true, RequireExisting: true, BlockSubmissions: true, Document: []byte(`{"blocks":[]}`),
	}, nil)
	// no event, hint or unlock is read: the gate is first

	if _, err := uc.UnlockHint(context.Background(), f.eventID, f.userID, f.challengeID, f.hintID); !errors.Is(err, participantModel.ErrEventFormRequired.Err()) {
		t.Fatalf("want ErrEventFormRequired, got %v", err)
	}
	if unit.saved {
		t.Fatal("nothing may commit")
	}
}
