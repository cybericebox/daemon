package event

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// A task that waits for others answers with the names of what it waits for and nothing else of it.
func TestLockedTaskError_NamesOnlyTheUnsolvedPrerequisites(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	challenge, theory, practice, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventParticipant{EventID: eventID, UserID: userID, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil).AnyTimes()
	q.EXPECT().ListTeamChallengePrerequisites(gomock.Any(), teamID).Return([]postgres.ListTeamChallengePrerequisitesRow{
		{ChallengeID: challenge, PrerequisiteChallengeID: theory, Name: "Protocol", Solved: true},
		{ChallengeID: challenge, PrerequisiteChallengeID: practice, Name: "Configure OSPF", Solved: false},
		{ChallengeID: other, PrerequisiteChallengeID: theory, Name: "Protocol", Solved: false},
	}, nil).AnyTimes()
	u := &EventUseCase{participants: participantRepo.New(q), teamChallenges: teamChallengeRepo.New(q)}
	fallback := teamChallengeModel.ErrTeamChallengePrerequisites.Err()

	err := u.lockedTaskError(context.Background(), eventID, userID, challenge, fallback)
	require.ErrorIs(t, err, exerciseModel.ErrTaskLocked.Err())
	require.Contains(t, err.Error(), "Configure OSPF")
	require.NotContains(t, err.Error(), "Protocol", "a solved prerequisite is not waited for")

	// Nothing unsolved to name (it unlocked meanwhile): the plain error stands.
	err = u.lockedTaskError(context.Background(), eventID, userID, uuid.Must(uuid.NewV7()), fallback)
	require.ErrorIs(t, err, teamChallengeModel.ErrTeamChallengePrerequisites.Err())
}
