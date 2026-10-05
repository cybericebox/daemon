package exercise

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// ITestDeployExpiry schedules the end of a test lab's lease: at the given time the lab is removed if its
// lease is still over (the satisfied-by is the River enqueuer, see internal/useCase/useCase.go).
type ITestDeployExpiry interface {
	ScheduleTestDeployExpiry(ctx context.Context, ownerID, deployID uuid.UUID, at time.Time) error
}

// SetTestDeployExpiry wires the scheduler after the use cases exist; nil leaves the periodic sweep alone to
// remove expired labs.
func (u *ExerciseUseCase) SetTestDeployExpiry(s ITestDeployExpiry) { u.expiry = s }

// timeNow is the use case's clock; tests replace it.
func (u *ExerciseUseCase) timeNow() time.Time {
	if u.clock != nil {
		return u.clock()
	}
	return time.Now()
}

// scheduleTestDeployExpiry queues the job that ends the lab at its lease's end. It is best effort: the
// periodic sweep removes what a missed job leaves, at most a minute later.
func (u *ExerciseUseCase) scheduleTestDeployExpiry(ctx context.Context, deploy exerciseModel.TestDeploy) {
	if u.expiry == nil || deploy.ExpiresAt.IsZero() {
		return
	}
	if err := u.expiry.ScheduleTestDeployExpiry(context.WithoutCancel(ctx), deploy.CreatedBy, deploy.ID, deploy.ExpiresAt); err != nil {
		log.Warn().Err(err).Str("deploy", deploy.ID.String()).Msg("Failed to schedule the end of a test lab; the periodic sweep removes it")
	}
}
