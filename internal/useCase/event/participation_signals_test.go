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
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

type recordingSignalPublisher struct {
	types    []signalModel.Type
	payloads []signalModel.Payload
}

func (p *recordingSignalPublisher) Publish(_ context.Context, typ signalModel.Type, payload signalModel.Payload) error {
	p.types = append(p.types, typ)
	p.payloads = append(p.payloads, payload)
	return nil
}

func TestJoinEventOpenRegistrationPublishesCompletionAndEnrollmentInUoW(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	allowNonStaff(q)
	unit := &testUoW{}
	publisher := &recordingSignalPublisher{}
	var factoryRepo event.IRepository
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit},
		SignalPublishers: event.SignalPublisherFactory(func(repo event.IRepository) event.SignalPublisher {
			factoryRepo = repo
			return publisher
		}),
	})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{LifecycleConfigured: true,
		ID: eventID, Tag: "olympiad", Name: "Olympiad", AvailableFrom: now.Add(time.Hour), ArchiveAt: pgtype.Timestamptz{Time: now.Add(2 * time.Hour), Valid: true},
		CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}, JoinPolicy: int16(eventModel.JoinPolicyLockedAtStart),
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationIndividual), Valid: true},
		Registration: int16(eventConfigModel.RegistrationOpen), MaxTeamSize: 1,
		CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil).Times(2)
	q.EXPECT().UpsertEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), CreatedAt: now,
	}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), CreatedAt: now,
	}, nil)
	q.EXPECT().CreateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventTeamParams) (postgres.EventTeam, error) {
		return postgres.EventTeam{ID: arg.ID, EventID: arg.EventID, Name: arg.Name, CaptainID: arg.CaptainID, MemberCount: 1, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
	})
	q.EXPECT().AssignEventParticipantTeam(gomock.Any(), gomock.Any()).Return(int64(1), nil)

	result, err := uc.JoinEvent(context.Background(), eventID, userID)
	require.NoError(t, err)
	require.Equal(t, participantModel.StatusApproved, result.Status)
	require.Same(t, q, factoryRepo)
	require.Equal(t, []signalModel.Type{
		signalModel.TypeParticipantOpenRegistrationCompleted,
		signalModel.TypeParticipantEnrolled,
	}, publisher.types)
	require.True(t, unit.saved)
}

func TestApproveParticipantPublishesApprovalAndEnrollmentInUoW(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	publisher := &recordingSignalPublisher{}
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit},
		SignalPublishers: event.SignalPublisherFactory(func(event.IRepository) event.SignalPublisher { return publisher }),
	})
	eventID, userID, managerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusPending), CreatedAt: now,
	}, nil)
	q.EXPECT().UpdateEventParticipant(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{LifecycleConfigured: true,
		ID: eventID, Tag: "olympiad", Name: "Olympiad", AvailableFrom: now.Add(time.Hour), ArchiveAt: pgtype.Timestamptz{Time: now.Add(2 * time.Hour), Valid: true},
		CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationIndividual), Valid: true},
		MaxTeamSize: 1, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil).Times(2)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), CreatedAt: now,
	}, nil)
	q.EXPECT().CreateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventTeamParams) (postgres.EventTeam, error) {
		return postgres.EventTeam{ID: arg.ID, EventID: arg.EventID, Name: arg.Name, CaptainID: arg.CaptainID, MemberCount: 1, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
	})
	q.EXPECT().AssignEventParticipantTeam(gomock.Any(), gomock.Any()).Return(int64(1), nil)

	require.NoError(t, uc.ApproveParticipant(context.Background(), eventID, userID, managerID))
	require.Equal(t, []signalModel.Type{
		signalModel.TypeParticipantApprovalRegistrationApproved,
		signalModel.TypeParticipantEnrolled,
	}, publisher.types)
	require.True(t, unit.saved)
}

func TestRejectParticipantPublishesOnlyRejectionInUoW(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	publisher := &recordingSignalPublisher{}
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit},
		SignalPublishers: event.SignalPublisherFactory(func(event.IRepository) event.SignalPublisher { return publisher }),
	})
	eventID, userID, managerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusPending), CreatedAt: now,
	}, nil)
	q.EXPECT().UpdateEventParticipant(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{LifecycleConfigured: true,
		ID: eventID, Tag: "olympiad", Name: "Olympiad", AvailableFrom: now.Add(time.Hour), ArchiveAt: pgtype.Timestamptz{Time: now.Add(2 * time.Hour), Valid: true},
		CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)

	require.NoError(t, uc.RejectParticipant(context.Background(), eventID, userID, managerID))
	require.Equal(t, []signalModel.Type{signalModel.TypeParticipantApprovalRegistrationRejected}, publisher.types)
	require.True(t, unit.saved)
}
