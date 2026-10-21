package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/internal/model/rbac"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestWithdrawnEventResultsAreStaffOnly(t *testing.T) {
	for _, audience := range []string{"anonymous", "participant", "manager"} {
		for _, mode := range []string{"snapshot", "poll", "open-stream"} {
			t.Run(audience+"/"+mode, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				q := newFormGateMock(ctrl)
				uc := event.NewEventUseCase(event.Dependencies{Repo: q})
				clock := newFakeClock(uc)
				eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
				now := time.Now().UTC()
				withdrawn := mode != "open-stream"
				reads := 1
				if !withdrawn {
					reads = 2
				}
				q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(publicConfig(eventID, now), nil).Times(reads)
				q.EXPECT().GetEventByID(gomock.Any(), eventID).DoAndReturn(func(context.Context, uuid.UUID) (postgres.Event, error) {
					if withdrawn {
						return startedEvent(eventID, now.Add(-3*time.Hour)), nil
					}
					return startedEvent(eventID, now), nil
				}).Times(reads)
				q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 7, UpdatedAt: now}, nil).Times(reads)
				q.EXPECT().ListEventScoreboard(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				q.EXPECT().ListEventScoreTimeline(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				access := event.ResultsAccess{}
				if audience != "anonymous" {
					access = event.ResultsAccess{UserID: &userID, Role: rbac.RoleUser}
					if audience == "manager" {
						q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, nil)
					} else {
						q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, pgx.ErrNoRows)
						q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
							EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), CreatedAt: now,
						}, nil)
					}
				}
				stream := uc.OpenLiveResults(eventID, access, false)
				if mode == "open-stream" {
					if _, err := stream.Replay(context.Background(), 7); err != nil {
						t.Fatalf("published event's initial replay: %v", err)
					}
					withdrawn = true
					clock.advance(2 * time.Second)
				}
				var err error
				switch mode {
				case "snapshot":
					_, err = uc.GetResultsSnapshot(context.Background(), eventID, access, false)
				case "poll":
					_, err = uc.ReplayLiveResults(context.Background(), eventID, access, 7, false)
				case "open-stream":
					_, err = stream.Replay(context.Background(), 7)
				}
				if audience == "manager" {
					if err != nil {
						t.Fatalf("staff must retain access to withdrawn results: %v", err)
					}
				} else if !errors.Is(err, eventModel.ErrEventNotFound.Err()) {
					t.Fatalf("withdrawn results must be not-found to %s, got %v", audience, err)
				}
			})
		}
	}
}
