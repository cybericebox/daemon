package challengeAttempt

import (
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

// Decision is a reviewer action applied to an immutable submitted attempt.
// DecisionAutomatic restores the original automated verdict; accepted and
// rejected are explicit moderator overrides.
type Decision int16

const (
	DecisionAutomatic Decision = iota
	DecisionAccepted
	DecisionRejected
)

func (d Decision) Valid() bool {
	return d == DecisionAutomatic || d == DecisionAccepted || d == DecisionRejected
}

func (d Decision) String() string {
	switch d {
	case DecisionAccepted:
		return "accepted"
	case DecisionRejected:
		return "rejected"
	default:
		return "automatic"
	}
}

func ParseDecision(value string) (Decision, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "automatic":
		return DecisionAutomatic, true
	case "accepted":
		return DecisionAccepted, true
	case "rejected":
		return DecisionRejected, true
	default:
		return DecisionAutomatic, false
	}
}

func (d Decision) EffectiveCorrect(automatic bool) bool {
	switch d {
	case DecisionAccepted:
		return true
	case DecisionRejected:
		return false
	default:
		return automatic
	}
}

// ManualDecision is append-only audit data. A later decision supersedes the
// earlier effective verdict but never erases it.
type ManualDecision struct {
	ID        uuid.UUID
	AttemptID uuid.UUID
	Decision  Decision
	Reason    string
	DecidedBy uuid.UUID
	DecidedAt time.Time
}

func NewManualDecision(attemptID uuid.UUID, decision Decision, reason string, decidedBy uuid.UUID, decidedAt time.Time) (ManualDecision, error) {
	reason = strings.TrimSpace(reason)
	if attemptID == uuid.Nil || decidedBy == uuid.Nil || decidedAt.IsZero() || !decision.Valid() {
		return ManualDecision{}, ErrDecisionInvalid.Err()
	}
	if reason == "" {
		return ManualDecision{}, ErrDecisionReasonRequired.Err()
	}
	return ManualDecision{ID: uuid.Must(uuid.NewV7()), AttemptID: attemptID, Decision: decision, Reason: reason, DecidedBy: decidedBy, DecidedAt: decidedAt}, nil
}
