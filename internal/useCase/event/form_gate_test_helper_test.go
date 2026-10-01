package event_test

import (
	"context"
	"github.com/jackc/pgx/v5"

	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
)

// newFormGateMock preserves unrelated use-case fixtures: those tests do not
// describe a pending form, so their default is the non-blocking case. Gate
// behavior itself is asserted by the focused internal-package tests.
func newFormGateMock(ctrl *gomock.Controller) *postgresMocks.MockQuerier {
	q := postgresMocks.NewMockQuerier(ctrl)
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), gomock.Any()).
		Return(postgres.EventFormVersion{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().GetEventParticipantFieldsMissing(gomock.Any(), gomock.Any()).Return(int32(0), nil).AnyTimes()
	q.EXPECT().GetEventTeamFieldsMissing(gomock.Any(), gomock.Any()).Return(int32(0), nil).AnyTimes()
	q.EXPECT().HasIncompleteRequiredEventFormDelivery(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, postgres.HasIncompleteRequiredEventFormDeliveryParams) (bool, error) {
			return false, nil
		}).
		AnyTimes()
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
	q.EXPECT().MarkUserInvitationSent(gomock.Any(), gomock.Any()).Return(int64(1), nil).AnyTimes()
	q.EXPECT().CountPendingTeamInvitations(gomock.Any(), gomock.Any()).Return(int64(0), nil).AnyTimes()
	q.EXPECT().GetEventParticipantProfile(gomock.Any(), gomock.Any()).Return(postgres.GetEventParticipantProfileRow{FirstName: "Олена", LastName: "Коваль", DisplayName: "Олена Коваль"}, nil).AnyTimes()
	q.EXPECT().ListEventTeamMembers(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().ListPendingTeamInvitations(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
}
