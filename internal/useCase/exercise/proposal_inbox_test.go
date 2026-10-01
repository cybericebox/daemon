package exercise_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
)

type fakeProposalInbox struct {
	decided  []inboxUseCase.Proposal
	approved []bool
	by       []uuid.UUID
	err      error
}

func (f *fakeProposalInbox) ProposalSubmitted(context.Context, inboxUseCase.Proposal) error {
	return f.err
}
func (f *fakeProposalInbox) ProposalDecided(_ context.Context, p inboxUseCase.Proposal, approved bool, by uuid.UUID) error {
	f.decided, f.approved, f.by = append(f.decided, p), append(f.approved, approved), append(f.by, by)
	return f.err
}

// TestRejectProposal_ReportsDecisionToInbox: the rejection reaches the inbox
// (resolving the admins' request, telling the proposer); an inbox failure
// never fails the already stored decision.
func TestRejectProposal_ReportsDecisionToInbox(t *testing.T) {
	for _, inboxErr := range []error{nil, errors.New("dispatch down")} {
		ctrl := gomock.NewController(t)
		q := postgresMocks.NewMockQuerier(ctrl)
		proposalID, exerciseID, proposer, admin := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		q.EXPECT().GetExerciseProposal(gomock.Any(), proposalID).Return(postgres.ExerciseProposal{
			ID: proposalID, ExerciseID: exerciseID, ProposedBy: uuid.NullUUID{UUID: proposer, Valid: true},
		}, nil)
		q.EXPECT().DecideExerciseProposal(gomock.Any(), gomock.Any()).Return(int64(1), nil)
		q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID, Name: "Web"}, nil)
		inbox := &fakeProposalInbox{err: inboxErr}
		uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
		uc.SetProposalInbox(inbox)

		if _, err := uc.RejectProposal(context.Background(), exercise.Actor{UserID: admin}, proposalID, "later"); err != nil {
			t.Fatalf("RejectProposal: %v", err)
		}
		if len(inbox.decided) != 1 || inbox.approved[0] || inbox.by[0] != admin {
			t.Fatalf("inbox decision = %+v approved=%v by=%v", inbox.decided, inbox.approved, inbox.by)
		}
		got := inbox.decided[0]
		if got.ID != proposalID || got.ProposedBy != proposer || got.ExerciseName != "Web" || got.DecisionNote != "later" {
			t.Fatalf("proposal notice = %+v", got)
		}
	}
}
