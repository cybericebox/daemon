package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestRunEventMailNotices_PublishesOnceAndRecordsState(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	publisher := &recordingSignalPublisher{}
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, EventDomain: "cybericebox.com",
		SignalPublishers: func(event.IRepository) event.SignalPublisher { return publisher },
	})
	now := time.Now().UTC()
	reminderEvent, finishedEvent, rollingID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	startAt := now.Add(23 * time.Hour)
	finishedAt := now.Add(-time.Hour)
	teamInvitee := uuid.Must(uuid.NewV7())

	q.EXPECT().ListEventsDueStartReminder(gomock.Any(), gomock.Any()).Return([]postgres.ListEventsDueStartReminderRow{
		{ID: reminderEvent, Tag: "olymp", Name: "Олімпіада", StartAt: startAt, DaysBeforeStart: 7},
	}, nil)
	q.EXPECT().MarkEventStartReminderSent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.MarkEventStartReminderSentParams) error {
		require.Equal(t, reminderEvent, p.EventID)
		require.True(t, p.StartReminderSentFor.Time.Equal(startAt), "state is keyed by the announced start")
		return nil
	})
	q.EXPECT().ListEventsDueFinishedNotice(gomock.Any(), gomock.Any()).Return([]postgres.ListEventsDueFinishedNoticeRow{
		{ID: finishedEvent, Tag: "ctf", Name: "CTF", FinishedAt: finishedAt},
	}, nil)
	q.EXPECT().MarkEventFinishedNotified(gomock.Any(), gomock.Any()).Return(nil)
	// Event whose joining closed at the start and has started: the roster is
	// frozen, so a team invitation is expired. (A rolling event keeps both
	// registration and roster open, so nothing expires there.)
	q.EXPECT().ListInvitationExpiryCandidates(gomock.Any(), gomock.Any()).Return([]postgres.ListInvitationExpiryCandidatesRow{
		{EventID: rollingID, UserID: teamInvitee, InvitedToTeam: true},
	}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), rollingID).Return(lockedStartedEventRow(rollingID, now), nil)
	q.EXPECT().MarkInvitationExpiredNotified(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.MarkInvitationExpiredNotifiedParams) (int64, error) {
		require.Equal(t, teamInvitee, p.UserID)
		return 1, nil
	})

	require.NoError(t, uc.RunEventMailNotices(context.Background()))
	require.Equal(t, []signalModel.Type{
		signalModel.TypeParticipantEventStartReminder,
		signalModel.TypeParticipantEventFinished,
		signalModel.TypeParticipantInvitationExpired,
	}, publisher.types)
	reminder := publisher.payloads[0].(*signalModel.EventNoticePayload)
	require.Equal(t, "https://olymp.cybericebox.com/", reminder.EventURL)
	require.Equal(t, 23, reminder.HoursLeft)
	require.NotEmpty(t, reminder.StartAt)
	require.Equal(t, teamInvitee, publisher.payloads[2].(*signalModel.ParticipantPayload).SubjectUserID)
	require.True(t, unit.saved)
}

func TestRunEventMailNotices_SkipsInvitationClaimedByAnotherPass(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	publisher := &recordingSignalPublisher{}
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}},
		SignalPublishers: func(event.IRepository) event.SignalPublisher { return publisher },
	})
	eventID := uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventsDueStartReminder(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().ListEventsDueFinishedNotice(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().ListInvitationExpiryCandidates(gomock.Any(), gomock.Any()).Return([]postgres.ListInvitationExpiryCandidatesRow{
		{EventID: eventID, UserID: uuid.Must(uuid.NewV7()), InvitedToTeam: true},
	}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(lockedStartedEventRow(eventID, time.Now().UTC()), nil)
	q.EXPECT().MarkInvitationExpiredNotified(gomock.Any(), gomock.Any()).Return(int64(0), nil)

	require.NoError(t, uc.RunEventMailNotices(context.Background()))
	require.Empty(t, publisher.types)
}
