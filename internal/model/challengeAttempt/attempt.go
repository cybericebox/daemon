// Package challengeAttempt owns an immutable submitted answer.
package challengeAttempt

import (
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

type Attempt struct {
	ID, EventID, EventTeamID, TeamChallengeID, UserID uuid.UUID
	Answer                                            string
	Correct                                           bool
	ReceivedAt, CreatedAt                             time.Time
}

// New captures the request-arrival timestamp supplied by the transport layer;
// it must not be replaced by a later database or worker timestamp.
func New(eventID, teamID, teamChallengeID, userID uuid.UUID, answer string, correct bool, receivedAt time.Time) (Attempt, error) {
	answer = strings.TrimSpace(answer)
	if eventID == uuid.Nil || teamID == uuid.Nil || teamChallengeID == uuid.Nil || userID == uuid.Nil || answer == "" || receivedAt.IsZero() {
		return Attempt{}, ErrAttemptInvalid.Err()
	}
	return Attempt{ID: uuid.Must(uuid.NewV7()), EventID: eventID, EventTeamID: teamID, TeamChallengeID: teamChallengeID, UserID: userID, Answer: answer, Correct: correct, ReceivedAt: receivedAt, CreatedAt: time.Now()}, nil
}
