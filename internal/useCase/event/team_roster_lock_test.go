package event_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

// individualConfig makes a roster change stop right after the config read: the point of the test is what comes first.
func individualConfig(eventID uuid.UUID) postgres.EventConfig {
	return postgres.EventConfig{EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationIndividual), Valid: true}}
}

// Join, assign, leave, kick and form all start with the event's roster lock, before any read they decide on. A bare
// strict mock proves the order: the lock must be called, and first.
func TestRosterChangesTakeTheEventLockBeforeReadingAnything(t *testing.T) {
	eventID, teamID, userID, actorID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	calls := map[string]func(uc *event.EventUseCase) error{
		"join": func(uc *event.EventUseCase) error { return uc.JoinTeam(context.Background(), eventID, userID, "code") },
		"assign": func(uc *event.EventUseCase) error {
			return uc.AssignParticipantToTeam(context.Background(), eventID, teamID, userID)
		},
		"form": func(uc *event.EventUseCase) error {
			return uc.FormManagedTeam(context.Background(), eventID, teamID, actorID)
		},
	}
	for name, call := range calls {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		unit := &testUoW{}
		uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
		gomock.InOrder(
			q.EXPECT().LockEventForTeamChange(gomock.Any(), eventID).Return(eventID, nil),
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(individualConfig(eventID), nil),
		)
		err := call(uc)
		require.ErrorIs(t, err, eventTeamModel.ErrEventTeamParticipationInvalid.Err(), name)
		require.True(t, unit.restored, name+": the transaction ends")
	}
}

func TestAFailedRosterLockEndsTheChangeWithoutAWrite(t *testing.T) {
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	q.EXPECT().LockEventForTeamChange(gomock.Any(), eventID).Return(uuid.Nil, errors.New("lock timeout"))
	require.Error(t, uc.JoinTeam(context.Background(), eventID, userID, "code"))
	require.False(t, unit.saved)
}

// The seats are counted in the invitation's own transaction: members (and pending invitations) together may not pass
// the maximum, however the invitations were interleaved.
func TestAnInvitationNeedsAFreeSeatCountedUnderTheLock(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	notifier := &invitationNotifier{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, EventDomain: "example.test", IDHost: "id.example.test", SetupTokens: invitationTokens{}})
	uc.SetInvitationNotifier(notifier)
	eventID, teamID, managerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true}, MaxTeamSize: 3}, nil).AnyTimes()
	q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Name: "Blue", MemberCount: 3}, nil).AnyTimes()
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "ctf", Name: "CTF", LifecycleConfigured: true}, nil).AnyTimes()
	results, err := uc.InviteTeamMembers(context.Background(), eventID, teamID, managerID, []event.ParticipantInvitationInput{{Email: "member@example.test"}})
	require.NoError(t, err)
	require.NotEmpty(t, results[0].Error, "no seat is left")
	require.False(t, unit.saved, "nothing is written for a full team")
}
