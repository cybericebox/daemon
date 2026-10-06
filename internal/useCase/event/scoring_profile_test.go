package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestUpdateEventScoringProfileRejectsPopularityForRollingJoin(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	id := uuid.Must(uuid.NewV7())
	now := time.Now()
	e := startedEvent(id, now)
	e.JoinPolicy = int16(eventModel.JoinPolicyRolling)
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(e, nil)
	_, err := uc.UpdateEventScoringProfile(context.Background(), id, event.UpdateEventScoringProfileInput{Profile: eventModel.ScoringProfile{Mode: eventModel.ScoringPopularityCurve, MinPoints: 10, MaxPoints: 100, FloorAtPercent: 50}}, uuid.Must(uuid.NewV7()))
	if err == nil {
		t.Fatal("popularity scoring must require a locked-at-start roster")
	}
}

func TestUpdateEventScoringProfileWritesFullProfile(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	id, actor := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	e := startedEvent(id, now)
	e.UpdatedAt = pgtype.Timestamptz{Time: now, Valid: true}
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(e, nil)
	q.EXPECT().UpdateEventScoringProfile(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventScoringProfileParams) (int64, error) {
		if arg.ID != id || arg.ScoringMode != int16(eventModel.ScoringFirstSolvesLadder) || arg.DynamicAlgorithm != 1 || !arg.ForceEventScoring {
			t.Fatalf("unexpected scoring update: %+v", arg)
		}
		return 1, nil
	})
	v, err := uc.UpdateEventScoringProfile(context.Background(), id, event.UpdateEventScoringProfileInput{Profile: eventModel.ScoringProfile{Mode: eventModel.ScoringFirstSolvesLadder, MinPoints: 10, MaxPoints: 100, FloorAtPercent: 50}, ForceEventScoring: true}, actor)
	if err != nil {
		t.Fatalf("UpdateEventScoringProfile: %v", err)
	}
	if v.Profile.Mode != eventModel.ScoringFirstSolvesLadder || !v.ForceEventScoring {
		t.Fatalf("unexpected response: %+v", v)
	}
}
