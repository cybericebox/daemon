package exercise

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	calendarUseCase "github.com/cybericebox/daemon/internal/useCase/resourceCalendar"
)

// TestLabGate is the resource calendar's admission of a catalog author's test laboratory: inside the author's
// booked window, the guaranteed pool, or room no event has reserved. It fails with "no free resources now" and
// the nearest free window the author can book. The hold is released when the laboratory ends.
type TestLabGate interface {
	AdmitTestLab(ctx context.Context, req calendarUseCase.TestLabRequest) (calendarUseCase.TestLabRoom, error)
	ReleaseTestLab(ctx context.Context, id uuid.UUID) error
	ExtendTestLab(ctx context.Context, id, owner uuid.UUID, size calendarUseCase.Amount, until time.Time) error
}

// SetTestLabGate wires the calendar after the use cases exist; nil admits every test laboratory.
func (u *ExerciseUseCase) SetTestLabGate(g TestLabGate) { u.testLabGate = g }

// admitTestLab asks the calendar for room for the laboratory of this topology; the hold is keyed by the deploy id.
func (u *ExerciseUseCase) admitTestLab(ctx context.Context, id, owner uuid.UUID, topo exerciseModel.Topology) error {
	if u.testLabGate == nil {
		return nil
	}
	policy := u.Policy()
	var largest calendarUseCase.Amount
	for _, d := range policy.Devices(topo) {
		largest = largest.Max(d.Amount)
	}
	_, err := u.testLabGate.AdmitTestLab(ctx, calendarUseCase.TestLabRequest{
		ID: id, Owner: owner, Size: policy.Total(topo).Amount, LargestDevice: largest, Lease: u.testDeployTTL(),
	})
	return err
}

// releaseTestLab frees the calendar room of a test laboratory that ended; a failure only leaves the hold to
// expire with its lease.
func (u *ExerciseUseCase) releaseTestLab(ctx context.Context, id uuid.UUID) {
	if u.testLabGate != nil {
		_ = u.testLabGate.ReleaseTestLab(ctx, id)
	}
}
