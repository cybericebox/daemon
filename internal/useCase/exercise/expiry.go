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

// ITestDeployRemovalCheck schedules the short follow-up check of a lab that is being removed (the satisfied-by is
// the River enqueuer). At most one check waits per deploy.
type ITestDeployRemovalCheck interface {
	ScheduleTestDeployRemovalCheck(ctx context.Context, ownerID, deployID uuid.UUID, attempt int, at time.Time) error
}

const (
	// removalCheckEvery is the pause between the follow-up checks of a lab being removed.
	removalCheckEvery = 3 * time.Second
	// removalCheckAttempts bounds the checks (about two minutes); the periodic sweep stays the safety net after that.
	removalCheckAttempts = 40
)

// SetTestDeployRemovalCheck wires the follow-up checks after the use cases exist; nil leaves the periodic sweep alone
// to drop the rows of removed labs.
func (u *ExerciseUseCase) SetTestDeployRemovalCheck(s ITestDeployRemovalCheck) { u.removalCheck = s }

// scheduleRemovalCheck queues check number attempt of a lab that is being removed. Best effort: the sweep drops the row.
func (u *ExerciseUseCase) scheduleRemovalCheck(ctx context.Context, deploy exerciseModel.TestDeploy, attempt int) {
	if u.removalCheck == nil || attempt > removalCheckAttempts {
		return
	}
	if err := u.removalCheck.ScheduleTestDeployRemovalCheck(context.WithoutCancel(ctx), deploy.CreatedBy, deploy.ID, attempt, u.timeNow().Add(removalCheckEvery)); err != nil {
		log.Warn().Err(err).Str("deploy", deploy.ID.String()).Msg("Failed to schedule the removal check of a test lab; the periodic sweep drops it")
	}
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
