package event_test

import (
	"context"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"sync"

	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
)

// userClockMarks records, per mock, the accounts whose 30-day unconfirmed
// clock was (re)started, so tests can assert it.
var userClockMarks sync.Map

// userClockNoop lists users the clock write matches no row for (active accounts).
var userClockNoop sync.Map

func userClockMarksOf(q *postgresMocks.MockQuerier) []uuid.UUID {
	v, _ := userClockMarks.Load(q)
	ids, _ := v.([]uuid.UUID)
	return ids
}

// newFormGateMock preserves unrelated use-case fixtures: those tests do not
// describe a pending form, so their default is the non-blocking case. Gate
// behavior itself is asserted by the focused internal-package tests.
func newFormGateMock(ctrl *gomock.Controller) *postgresMocks.MockQuerier {
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().ListEventPreviewResourceHolds(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	// Existing unrelated source-change fixtures have no canonical Lab rows;
	// actual lock/terminal behavior is tested against PostgreSQL.
	q.EXPECT().LockEventForLabSourceChange(gomock.Any(), gomock.Any()).Return(uuid.Nil, nil).AnyTimes()
	q.EXPECT().LockEventTeamsForLabSourceChange(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().LockEventTeamLabsForSourceChange(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().LockEventTeamForLabAdmission(gomock.Any(), gomock.Any()).Return(uuid.Nil, nil).AnyTimes()
	q.EXPECT().GetEventTeamLabForChallenge(gomock.Any(), gomock.Any()).Return(postgres.EventTeamLab{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), gomock.Any()).
		Return(postgres.EventFormVersion{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().GetEventParticipantFieldsMissing(gomock.Any(), gomock.Any()).Return(int32(0), nil).AnyTimes()
	q.EXPECT().GetEventTeamFieldsMissing(gomock.Any(), gomock.Any()).Return(int32(0), nil).AnyTimes()
	q.EXPECT().HasIncompleteRequiredEventFormDelivery(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, postgres.HasIncompleteRequiredEventFormDeliveryParams) (bool, error) {
			return false, nil
		}).
		AnyTimes()
	// Every roster change starts with the event's team lock.
	q.EXPECT().LockEventForTeamChange(gomock.Any(), gomock.Any()).Return(uuid.Nil, nil).AnyTimes()
	stubParticipationModelReads(q)
	return q
}

// stubParticipationModelReads answers the W2 narrow reads (admission, names,
// invitation delivery) with neutral values for tests about other behavior.
func stubParticipationModelReads(q *postgresMocks.MockQuerier) {
	q.EXPECT().GetEventTeamAdmitted(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	q.EXPECT().GetEventTeamFormed(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	q.EXPECT().GetEventMinTeamSize(gomock.Any(), gomock.Any()).Return(int32(1), nil).AnyTimes()
	q.EXPECT().MarkEventParticipantInvitationSent(gomock.Any(), gomock.Any()).Return(int64(1), nil).AnyTimes()
	q.EXPECT().MarkUserInvitationSent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.MarkUserInvitationSentParams) (int64, error) {
		if _, noop := userClockNoop.Load(p.ID); noop {
			return 0, nil
		}
		userClockMarks.Store(q, append(userClockMarksOf(q), p.ID))
		return 1, nil
	}).AnyTimes()
	q.EXPECT().CountPendingTeamInvitations(gomock.Any(), gomock.Any()).Return(int64(0), nil).AnyTimes()
	q.EXPECT().GetEventParticipantProfile(gomock.Any(), gomock.Any()).Return(postgres.GetEventParticipantProfileRow{FirstName: "Олена", LastName: "Коваль", DisplayName: "Олена Коваль"}, nil).AnyTimes()
	q.EXPECT().ListEventTeamMembers(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().ListPendingTeamInvitations(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
}
