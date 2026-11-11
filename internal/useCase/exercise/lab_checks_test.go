package exercise_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labCheckModel "github.com/cybericebox/daemon/internal/model/labCheck"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
	apperr "github.com/cybericebox/daemon/pkg/err"
)

// memoryLabChecks keeps results and asks in memory, with the same rate limit rule as the table.
type memoryLabChecks struct {
	results []labCheckModel.Result
	asks    map[string][]time.Time
}

func (m *memoryLabChecks) List(_ context.Context, group, lab string) ([]labCheckModel.Result, error) {
	var out []labCheckModel.Result
	for _, r := range m.results {
		if r.Group == group && r.Lab == lab {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memoryLabChecks) AskRun(_ context.Context, group, task string, window time.Duration, limit int, now time.Time) (bool, time.Duration, error) {
	if m.asks == nil {
		m.asks = map[string][]time.Time{}
	}
	key := group + "/" + task
	var recent []time.Time
	for _, at := range m.asks[key] {
		if at.After(now.Add(-window)) {
			recent = append(recent, at)
		}
	}
	if len(recent) >= limit {
		return false, recent[0].Add(window).Sub(now), nil
	}
	m.asks[key] = append(recent, now)
	return true, 0, nil
}

// checksInfra is the fake infrastructure that can also run checks.
type checksInfra struct {
	*fakeInfra

	asked []string
	err   error
}

func (c *checksInfra) RunLabChecks(_ context.Context, group, lab string, ids []string) error {
	c.asked = append(c.asked, group+"/"+lab+":"+join(ids))
	return c.err
}

func join(ids []string) string { return strings.Join(ids, ",") }

type stateFixture struct {
	q                                       *postgresMocks.MockQuerier
	userID, deployID                        uuid.UUID
	onceID, continuousID, quietID, answerID uuid.UUID
	variant                                 exerciseModel.Variant
	deploy                                  postgres.ExerciseTestDeployment
}

func newStateFixture(t *testing.T) *stateFixture {
	t.Helper()
	f := &stateFixture{q: postgresMocks.NewMockQuerier(gomock.NewController(t))}
	f.userID, f.deployID = uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	f.onceID, f.continuousID, f.quietID, f.answerID = uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	r1, r2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	state := func(id uuid.UUID, key string, mode exerciseModel.StateMode, manual bool, devices ...uuid.UUID) exerciseModel.Task {
		var conditions []exerciseModel.Condition
		for _, d := range devices {
			conditions = append(conditions, exerciseModel.Condition{DeviceID: d, File: &exerciseModel.FileObservation{Path: "/a"}})
		}
		return exerciseModel.Task{ID: id, Key: key, Name: key, Check: exerciseModel.Check{State: &exerciseModel.StateCheck{Mode: mode, Manual: manual, Conditions: conditions}}}
	}
	versionID, variantID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	f.variant = exerciseModel.Variant{
		ID: variantID,
		Tasks: []exerciseModel.Task{
			state(f.onceID, "ospf", exerciseModel.StateModeOnce, true, r1, r2),
			state(f.continuousID, "service", exerciseModel.StateModeContinuous, true, r1),
			state(f.quietID, "quiet", "", false, r2),
			{ID: f.answerID, Key: "theory", Name: "theory", Check: exerciseModel.TextCheck("OSPF")},
		},
		Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{
			{ID: r1, Name: "r1", Type: exerciseModel.DeviceTypeNode}, {ID: r2, Name: "r2", Type: exerciseModel.DeviceTypeNode},
		}},
	}
	variants, _ := json.Marshal([]exerciseModel.Variant{f.variant})
	f.q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, Variants: variants}, nil).AnyTimes()
	f.deploy = postgres.ExerciseTestDeployment{
		ID: f.deployID, GroupName: "tu-group", LabName: "l-1", CreatedBy: f.userID, VersionID: versionID, VariantID: variantID,
		ExpiresAt: time.Now().Add(time.Hour), Flags: []byte(`[]`), Solved: []byte(`[]`),
	}
	f.q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, postgres.GetOwnedExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
			return f.deploy, nil
		}).AnyTimes()
	return f
}

func passed(id string, first, changed time.Time) labCheckModel.Result {
	return labCheckModel.Result{Group: "tu-group", Lab: "l-1", CheckID: id, State: labCheckModel.StatePassed, FirstPassedAt: &first, ChangedAt: changed, Detail: "device said " + id}
}

func failed(id string, changed time.Time) labCheckModel.Result {
	return labCheckModel.Result{Group: "tu-group", Lab: "l-1", CheckID: id, State: labCheckModel.StateNotPassed, ChangedAt: changed, Detail: "not yet " + id}
}

// The author sees where each state task stands, with the details of the checks; a solved state task counts among the solved.
func TestDeployTestStatus_ShowsTheStateOfStateTasksToTheAuthor(t *testing.T) {
	f := newStateFixture(t)
	t0 := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	checks := &memoryLabChecks{results: []labCheckModel.Result{
		passed("ospf:1", t0, t0), passed("ospf:2", t0.Add(30*time.Second), t0.Add(30*time.Second)), // once: both passed
		failed("service:1", t0.Add(time.Minute)), // continuous, now failing
		// quiet:1 has no result yet
	}}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: f.q, Infra: &fakeInfra{status: exerciseModel.LabDeployStatus{Phase: "Ready"}}})
	uc.SetLabChecks(checks)

	st, err := uc.DeployTestStatus(context.Background(), f.userID, f.deployID)
	require.NoError(t, err)
	require.Len(t, st.StateTasks, 3, "only the state tasks")
	byKey := map[string]exerciseModel.TaskCheckState{}
	for _, task := range st.StateTasks {
		byKey[task.Key] = task
	}
	once := byKey["ospf"]
	require.True(t, once.Solved)
	require.Equal(t, t0.Add(30*time.Second), once.SolvedAt.UTC())
	require.True(t, once.Manual)
	require.Equal(t, "device said ospf:1", once.Checks[0].Detail, "the author sees the detail")
	require.False(t, byKey["service"].Solved)
	require.True(t, byKey["service"].Known)
	require.False(t, byKey["quiet"].Known)
	require.Equal(t, []uuid.UUID{f.onceID}, st.SolvedTasks, "a solved state task is counted as solved")

	// The continuous task passes later and then goes back: it follows the current state.
	checks.results = append(checks.results[:2], passed("service:1", t0.Add(2*time.Minute), t0.Add(2*time.Minute)))
	st, err = uc.DeployTestStatus(context.Background(), f.userID, f.deployID)
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{f.onceID, f.continuousID}, st.SolvedTasks)
	checks.results[2] = failed("service:1", t0.Add(3*time.Minute))
	checks.results[2].FirstPassedAt = ptr(t0.Add(2 * time.Minute))
	st, err = uc.DeployTestStatus(context.Background(), f.userID, f.deployID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{f.onceID}, st.SolvedTasks, "continuous: not solved any more")
	for _, task := range st.StateTasks {
		if task.Key == "service" {
			require.NotNil(t, task.FirstSolvedAt, "the first time it was solved is kept")
		}
	}
}

func TestRunTestDeployChecks_AsksForTheChecksOfOneTask(t *testing.T) {
	f := newStateFixture(t)
	infra := &checksInfra{fakeInfra: &fakeInfra{}}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: f.q, Infra: infra})
	uc.SetLabChecks(&memoryLabChecks{})
	ctx := context.Background()

	require.NoError(t, uc.RunTestDeployChecks(ctx, f.userID, f.deployID, f.onceID))
	require.Equal(t, []string{"tu-group/l-1:ospf:1,ospf:2"}, infra.asked, "exactly the checks of the task")

	// A task that does not allow a manual run, an answer task and an unknown task.
	for name, id := range map[string]uuid.UUID{"no manual run": f.quietID, "answer task": f.answerID, "unknown": uuid.Must(uuid.NewV7())} {
		err := uc.RunTestDeployChecks(ctx, f.userID, f.deployID, id)
		require.ErrorIs(t, err, exerciseModel.ErrChecksRunNotAllowed.Err(), name)
	}
	require.Len(t, infra.asked, 1, "nothing more was asked")

	// The laboratory refusing (checks off) passes through.
	infra.err = exerciseModel.ErrLabOptionUnavailable.WithPublicContext("options", "checks").Err()
	require.ErrorIs(t, uc.RunTestDeployChecks(ctx, f.userID, f.deployID, f.continuousID), exerciseModel.ErrLabOptionUnavailable.Err())
}

func TestRunTestDeployChecks_IsRateLimitedPerTask(t *testing.T) {
	f := newStateFixture(t)
	infra := &checksInfra{fakeInfra: &fakeInfra{}}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: f.q, Infra: infra})
	uc.SetLabChecks(&memoryLabChecks{})
	now := time.Now()
	uc.SetClock(func() time.Time { return now })
	ctx := context.Background()

	for range labCheckModel.ChecksRunRateLimit.Attempts {
		require.NoError(t, uc.RunTestDeployChecks(ctx, f.userID, f.deployID, f.onceID))
	}
	err := uc.RunTestDeployChecks(ctx, f.userID, f.deployID, f.onceID)
	require.ErrorIs(t, err, exerciseModel.ErrChecksRunTooOften.Err())
	require.Len(t, infra.asked, labCheckModel.ChecksRunRateLimit.Attempts, "the refused ask never reached the laboratory")
	var appErr apperr.Error
	require.ErrorAs(t, err, &appErr)
	require.EqualValues(t, 60, appErr.StatusCode().Details()[apperr.DetailRetryAfterSeconds])

	// Another task is not held; after the window the same task runs again.
	require.NoError(t, uc.RunTestDeployChecks(ctx, f.userID, f.deployID, f.continuousID))
	now = now.Add(61 * time.Second)
	require.NoError(t, uc.RunTestDeployChecks(ctx, f.userID, f.deployID, f.onceID))
}

func TestRunTestDeployChecks_OnlyOnALiveOwnLab(t *testing.T) {
	f := newStateFixture(t)
	infra := &checksInfra{fakeInfra: &fakeInfra{}}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: f.q, Infra: infra})
	uc.SetLabChecks(&memoryLabChecks{})
	f.deploy.ExpiresAt = time.Now().Add(-time.Minute)
	require.ErrorIs(t, uc.RunTestDeployChecks(context.Background(), f.userID, f.deployID, f.onceID), exerciseModel.ErrTestDeployNotFound.Err())
	require.Empty(t, infra.asked)
}
