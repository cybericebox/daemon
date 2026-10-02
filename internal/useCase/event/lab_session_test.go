package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/cybericebox/daemon/pkg/labaccess"
)

type recordingSessions struct{ sessions []labaccess.Session }

func (r *recordingSessions) Issue(_ context.Context, s labaccess.Session, _ time.Time) (labaccess.Link, error) {
	r.sessions = append(r.sessions, s)
	return labaccess.Link{URL: s.AccessURL + "/_auth?t=jwt", Token: "jwt"}, nil
}

func TestOpenOwnLabLink_IsUnavailableWithoutAnIssuer(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	if _, err := uc.OpenOwnLabLink(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "web", 80); err == nil {
		t.Fatal("no issuer configured must be an explicit error, not a token")
	}
}

func TestOpenOwnLabLink_RefusesAChallengeThatIsNotPublishedForTheTeam(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	sessions := &recordingSessions{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, LabSessions: sessions})
	eventID, userID, teamID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, CreatedAt: now}, nil)
	q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: challengeID}).Return(postgres.GetTeamChallengeRow{EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID, Readiness: 0, CreatedAt: now}, nil)
	if _, err := uc.OpenOwnLabLink(context.Background(), eventID, userID, challengeID, "web", 80); err == nil || len(sessions.sessions) != 0 {
		t.Fatalf("err=%v sessions=%v: a preparing task must not get a web session", err, sessions.sessions)
	}
}

func TestOpenOwnLabLink_RefusesAnUnapprovedParticipant(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	sessions := &recordingSessions{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, LabSessions: sessions})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 1, CreatedAt: time.Now()}, nil)
	if _, err := uc.OpenOwnLabLink(context.Background(), eventID, userID, uuid.Must(uuid.NewV7()), "web", 80); err == nil || len(sessions.sessions) != 0 {
		t.Fatalf("err=%v sessions=%v", err, sessions.sessions)
	}
}

func TestOpenModeratorsLabLink_IsUnavailableWithoutAnIssuer(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	if _, err := uc.OpenModeratorsLabLink(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "web", 80); err == nil {
		t.Fatal("no issuer configured must be an explicit error, not a token")
	}
}

// M7: the lab link, lab status and ACL honour the same locks as the board,
// submit and files: a published team task is not enough.
func openLinkFixture(t *testing.T) (*postgresMocks.MockQuerier, *event.EventUseCase, *recordingSessions, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	sessions := &recordingSessions{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, LabSessions: sessions})
	eventID, userID, teamID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, CreatedAt: now}, nil)
	q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: challengeID}).Return(postgres.GetTeamChallengeRow{EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID, Readiness: 2, CreatedAt: now}, nil)
	return q, uc, sessions, eventID, userID, teamID, challengeID
}

func TestOpenOwnLabLink_RefusesAnUnpublishedBoardChallenge(t *testing.T) {
	q, uc, sessions, eventID, userID, _, challengeID := openLinkFixture(t)
	q.EXPECT().GetEventChallengeForEvent(gomock.Any(), postgres.GetEventChallengeForEventParams{ID: challengeID, EventID: eventID}).Return(postgres.EventChallenge{ID: challengeID, Published: false}, nil)
	if _, err := uc.OpenOwnLabLink(context.Background(), eventID, userID, challengeID, "web", 80); err == nil || len(sessions.sessions) != 0 {
		t.Fatalf("err=%v sessions=%v: an unpublished task must not get a lab link", err, sessions.sessions)
	}
}

func TestOpenOwnLabLink_RefusesATaskOfADetachedSet(t *testing.T) {
	q, uc, sessions, eventID, userID, _, challengeID := openLinkFixture(t)
	setID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventChallengeForEvent(gomock.Any(), gomock.Any()).Return(postgres.EventChallenge{ID: challengeID, EventExerciseID: setID, Published: true}, nil)
	q.EXPECT().GetEventExerciseByID(gomock.Any(), postgres.GetEventExerciseByIDParams{ID: setID, EventID: eventID}).Return(postgres.EventExercise{ID: setID, EventID: eventID, Status: 2}, nil)
	if _, err := uc.OpenOwnLabLink(context.Background(), eventID, userID, challengeID, "web", 80); err == nil || len(sessions.sessions) != 0 {
		t.Fatalf("err=%v sessions=%v: a detached set has no lab link", err, sessions.sessions)
	}
}

func TestOpenOwnLabLink_RefusesALockedPrerequisiteChallenge(t *testing.T) {
	q, uc, sessions, eventID, userID, teamID, challengeID := openLinkFixture(t)
	setID, prerequisiteID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventChallengeForEvent(gomock.Any(), gomock.Any()).Return(postgres.EventChallenge{ID: challengeID, EventExerciseID: setID, Published: true}, nil)
	q.EXPECT().GetEventExerciseByID(gomock.Any(), gomock.Any()).Return(postgres.EventExercise{ID: setID, EventID: eventID, Status: 0}, nil)
	q.EXPECT().ListEventChallengePrerequisites(gomock.Any(), challengeID).Return([]uuid.UUID{prerequisiteID}, nil)
	q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: prerequisiteID}).Return(postgres.GetTeamChallengeRow{EventTeamID: teamID, EventChallengeID: prerequisiteID}, nil)
	if _, err := uc.OpenOwnLabLink(context.Background(), eventID, userID, challengeID, "web", 80); err == nil || len(sessions.sessions) != 0 {
		t.Fatalf("err=%v sessions=%v: a task locked by an unsolved prerequisite has no lab link", err, sessions.sessions)
	}
}
