package exercise

import (
	"context"
	"slices"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labCheckModel "github.com/cybericebox/daemon/internal/model/labCheck"
	"github.com/cybericebox/daemon/pkg/err"
)

// ILabChecks keeps the recorded results of laboratory checks and the manual runs asked for (satisfied by
// *labCheckRepo.Repository). Nil (narrow constructions): a test deploy shows no task states and a manual run is refused.
type ILabChecks interface {
	List(ctx context.Context, group, lab string) ([]labCheckModel.Result, error)
	AskRun(ctx context.Context, group, taskKey string, window time.Duration, limit int, now time.Time) (allowed bool, retryAfter time.Duration, err error)
}

// SetLabChecks wires the store of check results.
func (u *ExerciseUseCase) SetLabChecks(c ILabChecks) { u.labChecks = c }

// ChecksRunner is the optional capability of the infrastructure that asks the laboratory to run checks of a lab now.
type ChecksRunner interface {
	RunLabChecks(ctx context.Context, group, lab string, ids []string) error
}

// TaskCheckStates maps the recorded results of a lab to the state tasks of its variant, with every check's detail (for the
// author of a test; the participant-facing view never carries the details).
func TaskCheckStates(variant exerciseModel.Variant, results []labCheckModel.Result) []exerciseModel.TaskCheckState {
	byID := make(map[string]labCheckModel.Result, len(results))
	for _, r := range results {
		byID[r.CheckID] = r
	}
	states := labCheckModel.TaskStates(variant, results)
	out := make([]exerciseModel.TaskCheckState, 0, len(states))
	for i, task := range variant.StateTasks() {
		state := states[i]
		view := exerciseModel.TaskCheckState{
			TaskID: task.ID, Key: task.Key, Mode: state.Mode, Manual: task.Check.State.Manual,
			Solved: state.Solved, SolvedAt: state.SolvedAt, FirstSolvedAt: state.FirstSolvedAt, Known: state.Known,
		}
		for _, id := range labCheckModel.ExpectedCheckIDs(task) {
			detail := exerciseModel.CheckDetail{ID: id}
			if r, ok := byID[id]; ok {
				changed := r.ChangedAt
				detail.Known, detail.Passed = true, r.State == labCheckModel.StatePassed
				detail.FirstPassedAt, detail.ChangedAt = r.FirstPassedAt, &changed
				detail.Value, detail.Detail = r.Value, r.Detail
			}
			view.Checks = append(view.Checks, detail)
		}
		out = append(out, view)
	}
	return out
}

// withTaskStates adds the state of the variant's state tasks to a test deploy's status, and counts the solved ones among the
// solved tasks. A failure to read is not the poll's failure: the status then carries none.
func (u *ExerciseUseCase) withTaskStates(ctx context.Context, status *exerciseModel.LabDeployStatus, deploy exerciseModel.TestDeploy) {
	if deploy.VersionID == uuid.Nil {
		return // a row without a version has no variant to read
	}
	_, variant, loadErr := u.loadVersionVariant(ctx, deploy.VersionID, deploy.VariantID)
	if loadErr != nil {
		return
	}
	status.ReverseConnections = variant.Topology.VPN.Enabled && variant.Topology.VPN.ReverseConnections
	if u.labChecks == nil || len(variant.StateTasks()) == 0 {
		return
	}
	results, listErr := u.labChecks.List(ctx, deploy.GroupName, deploy.LabName)
	if listErr != nil {
		return
	}
	status.StateTasks = TaskCheckStates(variant, results)
	for _, task := range status.StateTasks {
		if task.Solved && !slices.Contains(status.SolvedTasks, task.TaskID) {
			status.SolvedTasks = append(slices.Clone(status.SolvedTasks), task.TaskID)
		}
	}
}

// RunTestDeployChecks asks the laboratory to run the checks of one state task of the author's own test lab now. It needs the
// task to allow a manual run; it is limited per lab group and task (labCheckModel.ChecksRunRateLimit).
func (u *ExerciseUseCase) RunTestDeployChecks(ctx context.Context, userID, deployID, taskID uuid.UUID) error {
	deploy, getErr := u.testDeploys.GetOwned(ctx, deployID, userID)
	if getErr != nil {
		if repositoryTools.IsObjectNotFoundError(getErr) {
			return exerciseModel.ErrTestDeployNotFound.Err()
		}
		return model.ErrPlatform.WithError(getErr).WithMessage("Failed to load test deploy").Err()
	}
	if deploy.IsStatic() || testDeployExpired(deploy, u.timeNow()) {
		return exerciseModel.ErrTestDeployNotFound.Err()
	}
	if scopeErr := u.requireScopedPreviewActive(ctx, deploy); scopeErr != nil {
		return scopeErr
	}
	_, variant, loadErr := u.loadVersionVariant(ctx, deploy.VersionID, deploy.VariantID)
	if loadErr != nil {
		return loadErr
	}
	return u.runTaskChecks(ctx, variant, taskID, deploy.GroupName, deploy.LabName)
}

// runTaskChecks is the manual run of one task's checks: the task must be a state task that allows it, the rate limit holds,
// and the laboratory is asked for exactly the checks of that task.
func (u *ExerciseUseCase) runTaskChecks(ctx context.Context, variant exerciseModel.Variant, taskID uuid.UUID, group, lab string) error {
	var task *exerciseModel.Task
	for i := range variant.Tasks {
		if variant.Tasks[i].ID == taskID {
			task = &variant.Tasks[i]
		}
	}
	if task == nil || task.Check.State == nil || !task.Check.State.Manual {
		return exerciseModel.ErrChecksRunNotAllowed.Err()
	}
	runner, ok := u.infra.(ChecksRunner)
	if !ok || u.labChecks == nil {
		return infraModel.ErrInfrastructureUnavailable.Err()
	}
	limit := labCheckModel.ChecksRunRateLimit
	allowed, retryAfter, askErr := u.labChecks.AskRun(ctx, group, task.Key, limit.Window, limit.Attempts, u.timeNow())
	if askErr != nil {
		return model.ErrPlatform.WithError(askErr).WithMessage("Failed to record the manual run of checks").Err()
	}
	if !allowed {
		return exerciseModel.ErrChecksRunTooOften.WithDetail(err.DetailRetryAfterSeconds, int64((retryAfter+time.Second-1)/time.Second)).Err()
	}
	return runner.RunLabChecks(ctx, group, lab, labCheckModel.ExpectedCheckIDs(*task))
}
