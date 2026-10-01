package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
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
