package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

// activityRow matches a written activity row by a predicate.
type activityRowMatcher struct {
	match func(postgres.CreateEventActivityParams) bool
}

func activityRow(match func(postgres.CreateEventActivityParams) bool) gomock.Matcher {
	return activityRowMatcher{match: match}
}

func (m activityRowMatcher) Matches(x any) bool {
	p, ok := x.(postgres.CreateEventActivityParams)
	return ok && m.match(p)
}

func (m activityRowMatcher) String() string { return "an event activity row" }

// expectRejectionLogged expects the refused submission row: the team the
// participant is in, the task, the request time and the reason.
func expectRejectionLogged(q *postgresMocks.MockQuerier, eventID, userID, teamID, challengeID uuid.UUID, at time.Time, reason string) {
	q.EXPECT().CreateEventActivity(gomock.Any(), activityRow(func(p postgres.CreateEventActivityParams) bool {
		var data map[string]string
		if json.Unmarshal(p.Data, &data) != nil {
			return false
		}
		return p.Kind == "attempt_rejected" && p.EventID == eventID && p.UserID.UUID == userID && p.TeamID.UUID == teamID &&
			p.SubjectID.UUID == challengeID && p.At.Equal(at) && data["reason"] == reason
	})).Return(nil)
}

func expectOwnBoard(q *postgresMocks.MockQuerier, eventID, userID, teamID, challengeID uuid.UUID, now time.Time) {
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
	}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	q.EXPECT().ListTeamBoardChallenges(gomock.Any(), postgres.ListTeamBoardChallengesParams{EventTeamID: teamID, PublishedOnly: true}).Return([]postgres.ListTeamBoardChallengesRow{{
		ID: uuid.Must(uuid.NewV7()), EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID, Readiness: 2, Snapshot: []byte(`{}`),
	}}, nil)
	q.EXPECT().ListTeamChallengePrerequisites(gomock.Any(), teamID).Return(nil, nil)
}

// A task open is written once (with the database dedupe window); a repeat
// inside the window costs no database work at all.
func TestOpenOwnChallenge_RecordsOncePerWindow(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	eventID, userID, teamID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expectOwnBoard(q, eventID, userID, teamID, challengeID, time.Now())
	q.EXPECT().CreateEventActivityOnce(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateEventActivityOnceParams) (int64, error) {
		if p.Kind != "task_opened" || p.EventID != eventID || p.UserID != userID || p.TeamID.UUID != teamID || p.SubjectID != challengeID {
			return 0, fmt.Errorf("unexpected row %+v", p)
		}
		if got := p.At.Sub(p.Since); got != time.Minute {
			return 0, fmt.Errorf("dedupe window = %s, want 1m", got)
		}
		return 1, nil
	})
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})

	for i := 0; i < 3; i++ {
		if err := uc.OpenOwnChallenge(context.Background(), eventID, userID, challengeID); err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
	}
}

func TestOpenOwnChallenge_UnknownTaskIsNotFound(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expectOwnBoard(q, eventID, userID, teamID, uuid.Must(uuid.NewV7()), time.Now())
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})

	err := uc.OpenOwnChallenge(context.Background(), eventID, userID, uuid.Must(uuid.NewV7()))
	if !eventChallengeModelIsNotFound(err) {
		t.Fatalf("err = %v, want challenge not found", err)
	}
}

// A write failure never fails the beacon.
func TestOpenOwnChallenge_WriteFailureIsSwallowed(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	eventID, userID, teamID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expectOwnBoard(q, eventID, userID, teamID, challengeID, time.Now())
	q.EXPECT().CreateEventActivityOnce(gomock.Any(), gomock.Any()).Return(int64(0), fmt.Errorf("db down"))
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})

	if err := uc.OpenOwnChallenge(context.Background(), eventID, userID, challengeID); err != nil {
		t.Fatalf("a failed log write must not fail the request: %v", err)
	}
}

// A submission after the finish is logged as after_finish: the lifecycle is
// read again only to tell it from one before the start.
func TestSubmitChallenge_ClosedRuntimeLogsAfterFinish(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, userID, teamID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	finished := startedEvent(eventID, now)
	finished.FinishAt = pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true}
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, CreatedAt: now}, nil).Times(2)
	q.EXPECT().ReserveRequestIdempotency(gomock.Any(), gomock.Any()).Return([]postgres.RequestIdempotency{{}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(finished, nil).Times(2)
	expectRejectionLogged(q, eventID, userID, teamID, challengeID, now, "after_finish")

	if _, err := uc.SubmitChallenge(context.Background(), eventID, userID, challengeID, event.SubmitChallengeInput{Answer: "ICE{late}", IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: now}); err == nil || unit.saved {
		t.Fatalf("err=%v saved=%v", err, unit.saved)
	}
}

// A failure that is not a refusal (here: the participant read) is not logged.
func TestSubmitChallenge_PlatformFailureIsNotLogged(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ReserveRequestIdempotency(gomock.Any(), gomock.Any()).Return([]postgres.RequestIdempotency{{}}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows)

	if _, err := uc.SubmitChallenge(context.Background(), eventID, userID, uuid.Must(uuid.NewV7()), event.SubmitChallengeInput{Answer: "x", IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: time.Now()}); err == nil {
		t.Fatal("a missing participant must fail the submission")
	}
}

func eventChallengeModelIsNotFound(err error) bool {
	return err != nil && errors.Is(err, eventChallengeModel.ErrEventChallengeNotFound.Err())
}
