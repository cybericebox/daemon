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
// publishes participant.event.results_published once; opening again or
// closing does not.
func TestSetResultsOpened_AnnouncesOnlyTheFirstOpening(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name     string
		openedAt pgtype.Timestamptz
		opened   bool
		want     []signalModel.Type
	}{
		{"first opening", pgtype.Timestamptz{}, true, []signalModel.Type{signalModel.TypeParticipantEventResultsPublished}},
		{"already open", pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true}, true, nil},
		{"closing", pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true}, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			publisher := &recordingSignalPublisher{}
			uc := event.NewEventUseCase(event.Dependencies{
				Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}, EventDomain: "cybericebox.com",
				SignalPublishers: func(event.IRepository) event.SignalPublisher { return publisher },
			})
			eventID := uuid.Must(uuid.NewV7())
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
				EventID: eventID, ResultsFreezeMinutes: 60, ResultsChartTeams: 10, ResultsOpenedAt: tc.openedAt,
				UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
			}, nil).AnyTimes()
			q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).Return(int64(1), nil).AnyTimes()
			q.EXPECT().AdvanceEventResultRevision(gomock.Any(), gomock.Any()).Return(postgres.AdvanceEventResultRevisionRow{}, nil).AnyTimes()
			q.EXPECT().CreateEventResultChange(gomock.Any(), gomock.Any()).Return(postgres.EventResultChange{}, nil).AnyTimes()
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil).AnyTimes()

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
