package event_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestEventTaskPointsLimit(t *testing.T) {
	for _, points := range []int32{999, 1000} {
		t.Run(fmt.Sprint(points), func(t *testing.T) {
			q := newFormGateMock(gomock.NewController(t))
			uc := newUC(q)
			eventID, attachmentID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
			q.EXPECT().GetEventExerciseByID(gomock.Any(), gomock.Any()).Return(postgres.EventExercise{ID: attachmentID, EventID: eventID, Status: int16(eventExerciseModel.StatusActive)}, nil)
			q.EXPECT().GetEventChallengeByID(gomock.Any(), gomock.Any()).Return(postgres.EventChallenge{ID: challengeID, EventExerciseID: attachmentID, Points: 100}, nil)
			if points == 999 {
				q.EXPECT().UpdateEventChallenge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventChallengeParams) (postgres.EventChallenge, error) {
					if arg.Points != 999 {
						t.Fatalf("persisted points=%d, want 999", arg.Points)
					}
					return postgres.EventChallenge{ID: challengeID, Points: arg.Points}, nil
				})
				q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
			}
			view, err := uc.UpdateEventChallenge(context.Background(), eventID, attachmentID, challengeID, event.UpdateEventChallengeInput{Points: points})
			if points == 1000 {
				if !errors.Is(err, eventChallengeModel.ErrEventChallengePointsInvalid.Err()) {
					t.Fatalf("oversize points: %v", err)
				}
			} else if err != nil || view.Points != 999 {
				t.Fatalf("boundary points: view=%+v err=%v", view, err)
			}
		})
	}
}

func TestEventScoringProfilePointsLimit(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		for _, points := range []int32{999, 1000} {
			t.Run(fmt.Sprintf("dynamic=%t/points=%d", dynamic, points), func(t *testing.T) {
				q := newFormGateMock(gomock.NewController(t))
				uc := newUC(q)
				id := uuid.Must(uuid.NewV7())
				now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
				q.EXPECT().GetEventByID(gomock.Any(), id).Return(startedEvent(id, now), nil)
				in := event.UpdateEventScoringProfileInput{Profile: eventModel.ScoringProfile{Mode: eventModel.ScoringStatic}, StaticPoints: &points}
				wantErr := eventModel.ErrEventStaticPointsInvalid.Err()
				if dynamic {
					in.Profile = eventModel.ScoringProfile{Mode: eventModel.ScoringPopularityCurve, MinPoints: 0, MaxPoints: points, FloorAtPercent: 50}
					in.StaticPoints = nil
					wantErr = eventModel.ErrEventScoringProfileInvalid.Err()
				}
				if points == 999 {
					q.EXPECT().UpdateEventScoringProfile(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventScoringProfileParams) (int64, error) {
						if (dynamic && arg.DynamicMaxPoints != 999) || (!dynamic && (!arg.StaticPoints.Valid || arg.StaticPoints.Int32 != 999)) {
							t.Fatalf("unexpected persisted profile: %+v", arg)
						}
						return 1, nil
					})
				}
				_, err := uc.UpdateEventScoringProfile(context.Background(), id, in, uuid.Nil)
				if points == 1000 {
					if !errors.Is(err, wantErr) {
						t.Fatalf("oversize profile: %v", err)
					}
				} else if err != nil {
					t.Fatalf("boundary profile: %v", err)
				}
			})
		}
	}
}

func TestTaskScoringOverrideRejectsOversizePointsBeforeWrites(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	id, attachmentID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventExerciseByID(gomock.Any(), gomock.Any()).Return(postgres.EventExercise{ID: attachmentID, EventID: id, Status: int16(eventExerciseModel.StatusActive)}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(startedEvent(id, time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)), nil)
	err := uc.BulkUpdateChallengeScoring(context.Background(), id, attachmentID, event.BulkUpdateChallengeScoringInput{
		ChallengeIDs: []uuid.UUID{uuid.Must(uuid.NewV7())},
		Override:     &eventModel.ScoringProfile{Mode: eventModel.ScoringPopularityCurve, MinPoints: 0, MaxPoints: 1000, FloorAtPercent: 50},
	})
	if !errors.Is(err, eventModel.ErrEventScoringProfileInvalid.Err()) || unit.saved {
		t.Fatalf("oversize override must not commit: err=%v saved=%t", err, unit.saved)
	}
}

func TestGetScoringProfilePreservesLegacyPoints(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	uc := newUC(q)
	id := uuid.Must(uuid.NewV7())
	row := startedEvent(id, time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	row.ScoringMode = int16(eventModel.ScoringPopularityCurve)
	row.DynamicMinPoints, row.DynamicMaxPoints = 1000, 1500
	row.StaticPoints = pgtype.Int4{Int32: 2000, Valid: true}
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(row, nil)
	view, err := uc.GetEventScoringProfile(context.Background(), id)
	if err != nil || view.Profile.MaxPoints != 1500 || view.Profile.MinPoints != 1000 || view.StaticPoints == nil || *view.StaticPoints != 2000 {
		t.Fatalf("legacy values must load unchanged: view=%+v err=%v", view, err)
	}
}
