package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

// L17: the event id is the caller's; only someone who has a participant row on that event leaves a trace on it.
func TestTouchParticipantPresence_OnlyForParticipantsOfTheEvent(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{}, pgx.ErrNoRows)
	// no TouchEventParticipantPresence expected: a stranger writes nothing
	if err := uc.TouchParticipantPresence(context.Background(), eventID, userID); err != nil {
		t.Fatalf("a stranger is ignored, not an error: %v", err)
	}

	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, CreatedAt: time.Now()}, nil)
	q.EXPECT().TouchEventParticipantPresence(gomock.Any(), gomock.Any()).Return(nil)
	if err := uc.TouchParticipantPresence(context.Background(), eventID, userID); err != nil {
		t.Fatalf("participant: %v", err)
	}
}
