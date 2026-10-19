package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

// TestSetResultsOpened_AnnouncesOnlyTheFirstOpening: opening the results
// publishes participant.event.results_published once, and only while the
// participants can actually see the results; opening again or closing does not.
func TestSetResultsOpened_AnnouncesOnlyTheFirstOpening(t *testing.T) {
	now := time.Now().UTC()
	opened := pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true}
	published := []signalModel.Type{signalModel.TypeParticipantEventResultsPublished}
	started := rollingEventRow
	notStarted := func(eventID uuid.UUID, now time.Time) postgres.Event {
		row := rollingEventRow(eventID, now)
		row.StartAt = now.Add(time.Hour)
		return row
	}
	unpublished := func(eventID uuid.UUID, now time.Time) postgres.Event {
		row := notStarted(eventID, now)
		row.PublishAt = now.Add(30 * time.Minute)
		return row
	}
	finished := func(eventID uuid.UUID, now time.Time) postgres.Event {
		row := rollingEventRow(eventID, now)
		row.FinishAt = pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true}
		return row
	}
	for _, tc := range []struct {
		name       string
		openedAt   pgtype.Timestamptz
		opened     bool
		visibility int16
		event      func(uuid.UUID, time.Time) postgres.Event
		want       []signalModel.Type
	}{
		{"first opening", pgtype.Timestamptz{}, true, 2, started, published},
		{"first opening after the finish", pgtype.Timestamptz{}, true, 1, finished, published},
		{"already open", opened, true, 2, started, nil},
		{"closing", opened, false, 2, started, nil},
		{"results hidden from participants", pgtype.Timestamptz{}, true, 0, started, nil},
		{"event not started", pgtype.Timestamptz{}, true, 2, notStarted, nil},
		{"event not published", pgtype.Timestamptz{}, true, 2, unpublished, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			publisher := &recordingSignalPublisher{}
			uc := event.NewEventUseCase(event.Dependencies{
				Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}, EventDomain: "cybericebox.com", IDHost: "id.cybericebox.com",
				SignalPublishers: func(event.IRepository) event.SignalPublisher { return publisher },
			})
			eventID := uuid.Must(uuid.NewV7())
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
				EventID: eventID, ResultsFreezeMinutes: 60, ResultsChartTeams: 10, ResultsOpenedAt: tc.openedAt,
				ScoreboardVisibility: tc.visibility,
				UpdatedAt:            pgtype.Timestamptz{Time: now, Valid: true},
			}, nil).AnyTimes()
			q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).Return(int64(1), nil).AnyTimes()
			q.EXPECT().AdvanceEventResultRevision(gomock.Any(), gomock.Any()).Return(postgres.AdvanceEventResultRevisionRow{}, nil).AnyTimes()
			q.EXPECT().CreateEventResultChange(gomock.Any(), gomock.Any()).Return(postgres.EventResultChange{}, nil).AnyTimes()
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(tc.event(eventID, now), nil).AnyTimes()

			_, err := uc.SetResultsOpened(context.Background(), eventID, tc.opened, uuid.Must(uuid.NewV7()))
			require.NoError(t, err)
			require.Equal(t, tc.want, publisher.types)
			if len(tc.want) > 0 {
				notice := publisher.payloads[0].(*signalModel.EventNoticePayload)
				require.Equal(t, eventID, notice.ScopeEventID)
				require.Equal(t, "https://event.cybericebox.com/", notice.EventURL)
			}
		})
	}
}

// TestGetManageResults_ParticipantsSee: the moderators' table says whether the
// participants read the board now, so «Панель заходу» shows the participants'
// view banner only then.
func TestGetManageResults_ParticipantsSee(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name       string
		visibility int16
		startIn    time.Duration
		want       bool
	}{
		{"running, public", 2, -time.Hour, true},
		{"running, participants only", 1, -time.Hour, true},
		{"running, hidden", 0, -time.Hour, false},
		{"not started", 2, time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := event.NewEventUseCase(event.Dependencies{
				Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}, EventDomain: "cybericebox.com", IDHost: "id.cybericebox.com",
			})
			eventID := uuid.Must(uuid.NewV7())
			row := rollingEventRow(eventID, now)
			row.StartAt = now.Add(tc.startIn)
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
				EventID: eventID, ResultsFreezeMinutes: 60, ResultsChartTeams: 10, ScoreboardVisibility: tc.visibility,
				UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
			}, nil).AnyTimes()
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(row, nil).AnyTimes()
			q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{}, nil).AnyTimes()
			q.EXPECT().ListManageScoreboard(gomock.Any(), eventID).Return(nil, nil).AnyTimes()
			q.EXPECT().ListManageScoreSolves(gomock.Any(), eventID).Return(nil, nil).AnyTimes()
			q.EXPECT().ListManageHintTotals(gomock.Any(), eventID).Return(nil, nil).AnyTimes()

			view, err := uc.GetManageResults(context.Background(), eventID)
			require.NoError(t, err)
			require.Equal(t, tc.want, view.ParticipantsSee)
		})
	}
}
