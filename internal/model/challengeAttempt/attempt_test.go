package challengeAttempt_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
)

func TestNew_PreservesReceivedTime(t *testing.T) {
	received := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	a, err := challengeAttempt.New(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), " ICE{x} ", true, received)
	if err != nil || a.ReceivedAt != received || a.Answer != "ICE{x}" {
		t.Fatalf("attempt = %+v, %v", a, err)
	}
}

func TestNewManualDecision_RequiresConcreteReason(t *testing.T) {
	attemptID := uuid.Must(uuid.NewV7())
	actorID := uuid.Must(uuid.NewV7())

	_, err := challengeAttempt.NewManualDecision(attemptID, challengeAttempt.DecisionAccepted, "   ", actorID, time.Now())
	if !errors.Is(err, challengeAttempt.ErrDecisionReasonRequired.Err()) {
		t.Fatalf("NewManualDecision blank reason error = %v, want ErrDecisionReasonRequired", err)
	}
}

func TestNewManualDecision_PreservesAcceptanceAndReason(t *testing.T) {
	attemptID := uuid.Must(uuid.NewV7())
	actorID := uuid.Must(uuid.NewV7())
	decidedAt := time.Date(2026, 9, 22, 12, 30, 0, 0, time.UTC)

	d, err := challengeAttempt.NewManualDecision(attemptID, challengeAttempt.DecisionAccepted, "Validated by the organizing team", actorID, decidedAt)
	if err != nil {
		t.Fatalf("NewManualDecision: %v", err)
	}
	if d.AttemptID != attemptID || d.Decision != challengeAttempt.DecisionAccepted || d.Reason != "Validated by the organizing team" || d.DecidedBy != actorID || d.DecidedAt != decidedAt {
		t.Fatalf("decision = %+v", d)
	}
}
