package exercise_test

import (
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
)

// expectNoUsage answers the usage questions of an import report: no event attaches the exercise, no test lab runs.
func expectNoUsage(q *postgresMocks.MockQuerier) {
	q.EXPECT().
		ListExerciseActiveAttachments(gomock.Any(), gomock.Any()).
		Return([]postgres.ListExerciseActiveAttachmentsRow{}, nil).
		AnyTimes()
	q.EXPECT().
		ListExerciseChallengeAnswerCounts(gomock.Any(), gomock.Any()).
		Return([]postgres.ListExerciseChallengeAnswerCountsRow{}, nil).
		AnyTimes()
	q.EXPECT().
		CountLeasedExerciseTestDeploys(gomock.Any(), gomock.Any()).
		Return(int32(0), nil).
		AnyTimes()
}
