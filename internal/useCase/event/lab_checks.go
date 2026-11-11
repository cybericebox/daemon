package event

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labCheckModel "github.com/cybericebox/daemon/internal/model/labCheck"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
	"github.com/cybericebox/daemon/pkg/err"
)

// LabCheckStore keeps the manual runs of checks that were asked for (the rate limit). Nil: a manual run is refused.
type LabCheckStore interface {
	AskRun(ctx context.Context, group, taskKey string, window time.Duration, limit int, now time.Time) (allowed bool, retryAfter time.Duration, err error)
}

// ChecksRunner is the optional capability of the infrastructure that asks the laboratory to run checks of a lab now.
type ChecksRunner interface {
	RunLabChecks(ctx context.Context, group, lab string, ids []string) error
}

// RunOwnChallengeChecks asks the laboratory to check the caller team's own task now: the task must be checked by the state
// of the lab and allow a manual run. It follows every lock of the board (published, the stage, the prerequisites: a task
// that waits for others answers ErrTaskLocked with the names of what it waits for, never anything else of it), and it is
// limited per team and task. The answer only says the request was recorded; the participant sees the result when the task
// is solved. Never exposes a definition, an expected value or a detail of a check.
func (u *EventUseCase) RunOwnChallengeChecks(ctx context.Context, eventID, userID, challengeID uuid.UUID) error {
	p, availableErr := u.requireOwnAvailableChallenge(ctx, eventID, userID, challengeID)
	if availableErr != nil {
		if errors.Is(availableErr, teamChallengeModel.ErrTeamChallengePrerequisites.Err()) {
			return u.lockedTaskError(ctx, eventID, userID, challengeID, availableErr)
		}
		return availableErr
	}
	teamID := *p.TeamID
	tc, getErr := u.teamChallenges.Get(ctx, teamID, challengeID)
	if getErr != nil {
		return model.ErrPlatform.WithError(getErr).WithMessage("Failed to get team challenge").Err()
	}
	challenge, getErr := u.eventChallenges.GetForEvent(ctx, eventID, challengeID)
	if getErr != nil {
		return model.ErrPlatform.WithError(getErr).WithMessage("Failed to get event challenge").Err()
	}
	set, getErr := u.eventExercises.GetByID(ctx, eventID, challenge.EventExerciseID)
	if getErr != nil {
		return model.ErrPlatform.WithError(getErr).WithMessage("Failed to get event exercise").Err()
	}
	resolver, ok := u.topologies.(DeployedVariantResolver)
	if !ok {
		return exerciseModel.ErrChecksRunNotAllowed.Err()
	}
	variant, resolveErr := resolver.ResolveDeployedVariant(ctx, set.ExerciseVersionID, tc.VariantIndex)
	if resolveErr != nil {
		return model.ErrPlatform.WithError(resolveErr).WithMessage("Failed to read the task of the team variant").Err()
	}
	var task *exerciseModel.Task
	for i := range variant.Tasks {
		if variant.Tasks[i].ID == challenge.TaskID {
			task = &variant.Tasks[i]
		}
	}
	if task == nil || task.Check.State == nil || !task.Check.State.Manual {
		return exerciseModel.ErrChecksRunNotAllowed.Err()
	}
	binding, bindingErr := u.labBindings.Get(ctx, teamID, challengeID)
	if bindingErr != nil {
		if repositoryTools.IsObjectNotFoundError(bindingErr) {
			return exerciseModel.ErrChecksRunNotAllowed.Err()
		}
		return model.ErrPlatform.WithError(bindingErr).WithMessage("Failed to read the lab of the task").Err()
	}
	runner, ok := u.infra.(ChecksRunner)
	if !ok || u.labChecks == nil {
		return infraUnavailable()
	}
	limit := labCheckModel.ChecksRunRateLimit
	allowed, retryAfter, askErr := u.labChecks.AskRun(ctx, binding.LabGroupName, task.Key, limit.Window, limit.Attempts, time.Now())
	if askErr != nil {
		return model.ErrPlatform.WithError(askErr).WithMessage("Failed to record the manual run of checks").Err()
	}
	if !allowed {
		return exerciseModel.ErrChecksRunTooOften.WithDetail(err.DetailRetryAfterSeconds, int64((retryAfter+time.Second-1)/time.Second)).Err()
	}
	return runner.RunLabChecks(ctx, binding.LabGroupName, binding.LabName, labCheckModel.ExpectedCheckIDs(*task))
}

// lockedTaskError is the answer for a task that waits for others: ErrTaskLocked with the names (and only the names) of the
// prerequisites the team has not solved. When they cannot be read the plain prerequisite error stands.
func (u *EventUseCase) lockedTaskError(ctx context.Context, eventID, userID, challengeID uuid.UUID, fallback error) error {
	p, getErr := u.participants.Get(ctx, eventID, userID)
	if getErr != nil || p.TeamID == nil {
		return fallback
	}
	waits, listErr := u.teamChallenges.Prerequisites(ctx, *p.TeamID)
	if listErr != nil {
		return fallback
	}
	var names []string
	for _, prerequisite := range waits[challengeID] {
		if !prerequisite.Solved {
			names = append(names, prerequisite.Name)
		}
	}
	if len(names) == 0 {
		return fallback
	}
	return exerciseModel.ErrTaskLocked.WithPublicContext("waits_for", strings.Join(names, "; ")).Err()
}
