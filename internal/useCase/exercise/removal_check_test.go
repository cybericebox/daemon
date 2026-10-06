package exercise_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
)

type removalCheck struct {
	attempts []int
	at       []time.Time
}

func (r *removalCheck) ScheduleTestDeployRemovalCheck(_ context.Context, _, _ uuid.UUID, attempt int, at time.Time) error {
	r.attempts = append(r.attempts, attempt)
	r.at = append(r.at, at)
	return nil
}

func removalFixture(t *testing.T, groupExists bool) (*exercise.ExerciseUseCase, *postgresMocks.MockQuerier, *presenceInfra, *removalCheck, uuid.UUID, uuid.UUID, time.Time) {
	t.Helper()
	now := fixedNow()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	owner := fixedID(3)
	id := fixedID(5)
	row := postgres.ExerciseTestDeployment{ID: id, GroupName: "tu-x", LabName: "l-x", CreatedBy: owner, ExpiresAt: now.Add(-time.Minute)}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil).AnyTimes()
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return([]postgres.ExerciseTestDeployment{row}, nil).AnyTimes()
	infra := &presenceInfra{fakeInfra: &fakeInfra{}, groupExists: groupExists}
	checks := &removalCheck{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	uc.SetClock(func() time.Time { return now })
	uc.SetTestDeployRemovalCheck(checks)
	return uc, q, infra, checks, owner, id, now
}

// An explicit end that leaves the lab still on the agent queues the first short check.
func TestDestroyDeployTest_QueuesAShortFollowUpWhileTheLabStillExists(t *testing.T) {
	uc, _, _, checks, owner, id, now := removalFixture(t, true)

	if err := uc.DestroyDeployTest(context.Background(), owner, id); err != nil {
		t.Fatal(err)
	}
	if len(checks.attempts) != 1 || checks.attempts[0] != 1 || !checks.at[0].Equal(now.Add(3*time.Second)) {
		t.Fatalf("one check after 3 s expected, got %v at %v", checks.attempts, checks.at)
	}
}

// The request never probes the agent and never drops the row, even for a lab that may already be gone: it asks
// for the deletion and leaves the looking (and the drop) to the check job.
func TestDestroyDeployTest_LeavesTheLookingToTheCheckJob(t *testing.T) {
	uc, _, infra, checks, owner, id, _ := removalFixture(t, false)
	// No DeleteOwnedExerciseTestDeploy expectation: a call would fail the test.

	if err := uc.DestroyDeployTest(context.Background(), owner, id); err != nil {
		t.Fatal(err)
	}
	if infra.probes != 0 {
		t.Fatalf("a request must not probe the agent, made %d", infra.probes)
	}
	if len(checks.attempts) != 1 {
		t.Fatalf("one follow-up check expected, got %v", checks.attempts)
	}
}

// The check never drops a row while the agent still has the lab, and queues the next check.
func TestCheckTestDeployRemoved_NeverDropsWhileTheLabExists(t *testing.T) {
	uc, _, infra, checks, owner, id, _ := removalFixture(t, true)
	// No DeleteOwnedExerciseTestDeploy expectation: a call would fail the test.

	if err := uc.CheckTestDeployRemoved(context.Background(), owner, id, 4); err != nil {
		t.Fatal(err)
	}
	if infra.destroyed != "" {
		t.Fatal("the check only looks, it does not ask the agent to delete again")
	}
	if len(checks.attempts) != 1 || checks.attempts[0] != 5 {
		t.Fatalf("the next check (5) must be queued, got %v", checks.attempts)
	}
}

// The row goes at the first check after the agent reports the lab gone.
func TestCheckTestDeployRemoved_DropsTheRowOnceGone(t *testing.T) {
	uc, q, _, checks, owner, id, _ := removalFixture(t, false)
	q.EXPECT().DeleteOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(int64(1), nil).Times(1)

	if err := uc.CheckTestDeployRemoved(context.Background(), owner, id, 2); err != nil {
		t.Fatal(err)
	}
	if len(checks.attempts) != 0 {
		t.Fatalf("done: nothing more to queue, got %v", checks.attempts)
	}
}

// After the bound the chain stops; the 30 s sweep stays the safety net.
func TestCheckTestDeployRemoved_StopsAfterTheBound(t *testing.T) {
	uc, _, _, checks, owner, id, _ := removalFixture(t, true)

	if err := uc.CheckTestDeployRemoved(context.Background(), owner, id, 40); err != nil {
		t.Fatal(err)
	}
	if len(checks.attempts) != 0 {
		t.Fatalf("no check past the bound, got %v", checks.attempts)
	}
}

// A lab whose lease was extended meanwhile is left alone.
func TestCheckTestDeployRemoved_ExtendedLabIsLeftAlone(t *testing.T) {
	now := fixedNow()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	owner, id := fixedID(3), fixedID(5)
	row := postgres.ExerciseTestDeployment{ID: id, GroupName: "tu-x", LabName: "l-x", CreatedBy: owner, ExpiresAt: now.Add(time.Hour)}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	checks := &removalCheck{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &presenceInfra{fakeInfra: &fakeInfra{}}})
	uc.SetClock(func() time.Time { return now })
	uc.SetTestDeployRemovalCheck(checks)

	if err := uc.CheckTestDeployRemoved(context.Background(), owner, id, 1); err != nil {
		t.Fatal(err)
	}
	if len(checks.attempts) != 0 {
		t.Fatalf("got %v", checks.attempts)
	}
}
