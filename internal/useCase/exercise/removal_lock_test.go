package exercise_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/delivery/repository/testDeployRepo"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
)

// lockUnit records whether the transaction was already committed when a follow-up check was queued.
type lockUnit struct{ saved bool }

func (u *lockUnit) Save() error    { u.saved = true; return nil }
func (u *lockUnit) Restore() error { return nil }

type lockWorker struct {
	q    testDeployRepo.Queries
	unit *lockUnit
}

func (w lockWorker) UnitOfWork(ctx context.Context) (context.Context, testDeployRepo.Queries, postgres.UoW, error) {
	return ctx, w.q, w.unit, nil
}

type orderedCheck struct {
	unit     *lockUnit
	attempts []int
	afterTx  []bool
}

func (c *orderedCheck) ScheduleTestDeployRemovalCheck(_ context.Context, _, _ uuid.UUID, attempt int, _ time.Time) error {
	c.attempts = append(c.attempts, attempt)
	c.afterTx = append(c.afterTx, c.unit.saved)
	return nil
}

func lockFixture(t *testing.T, groupExists bool) (*exercise.ExerciseUseCase, *postgresMocks.MockQuerier, *presenceInfra, *orderedCheck, uuid.UUID, uuid.UUID) {
	t.Helper()
	now := fixedNow()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	owner, id := fixedID(3), fixedID(5)
	row := postgres.ExerciseTestDeployment{ID: id, GroupName: "tu-x", LabName: "l-x", CreatedBy: owner, ExpiresAt: now.Add(-time.Minute)}
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil).AnyTimes()
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), owner).Return([]postgres.ExerciseTestDeployment{row}, nil).AnyTimes()
	unit := &lockUnit{}
	infra := &presenceInfra{fakeInfra: &fakeInfra{}, groupExists: groupExists}
	checks := &orderedCheck{unit: unit}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, DeployUoW: lockWorker{q: q, unit: unit}})
	uc.SetClock(func() time.Time { return now })
	uc.SetTestDeployRemovalCheck(checks)
	return uc, q, infra, checks, owner, id
}

// The next check is queued after the commit: queuing it inside took a second pooled connection while the first one
// held the lock, and a few such jobs starved the whole pool (sessions and every other job timed out).
func TestCheckTestDeployRemoved_QueuesTheNextCheckAfterTheCommit(t *testing.T) {
	uc, q, _, checks, owner, id := lockFixture(t, true)
	q.EXPECT().TryLockExerciseTestDeploysOf(gomock.Any(), owner.String()).Return(true, nil)

	if err := uc.CheckTestDeployRemoved(context.Background(), owner, id, 4); err != nil {
		t.Fatal(err)
	}
	if len(checks.attempts) != 1 || checks.attempts[0] != 5 || !checks.afterTx[0] {
		t.Fatalf("check 5 after the commit expected, got %v (after commit %v)", checks.attempts, checks.afterTx)
	}
}

// A busy author does not make the job wait on the lock with a connection held: it looks again later, with the same attempt.
func TestCheckTestDeployRemoved_BusyAuthorIsCheckedAgainLater(t *testing.T) {
	uc, q, infra, checks, owner, id := lockFixture(t, true)
	q.EXPECT().TryLockExerciseTestDeploysOf(gomock.Any(), owner.String()).Return(false, nil)

	if err := uc.CheckTestDeployRemoved(context.Background(), owner, id, 4); err != nil {
		t.Fatal(err)
	}
	if infra.probes != 0 {
		t.Fatalf("no agent call without the lock, made %d", infra.probes)
	}
	if len(checks.attempts) != 1 || checks.attempts[0] != 4 {
		t.Fatalf("the same check again expected, got %v", checks.attempts)
	}
}

// The lease-end job skips a busy author; the periodic sweep ends the lab on its next pass.
func TestEndExpiredTestDeploy_SkipsABusyAuthor(t *testing.T) {
	uc, q, infra, _, owner, id := lockFixture(t, true)
	q.EXPECT().TryLockExerciseTestDeploysOf(gomock.Any(), owner.String()).Return(false, nil)

	if err := uc.EndExpiredTestDeploy(context.Background(), owner, id); err != nil {
		t.Fatal(err)
	}
	if infra.destroyed != "" || infra.probes != 0 {
		t.Fatalf("nothing may run without the lock: destroyed %q, probes %d", infra.destroyed, infra.probes)
	}
}

// The author's own request still waits for the lock: it must not fail because a job is looking at the lab.
func TestDestroyDeployTest_WaitsForTheLock(t *testing.T) {
	uc, q, _, checks, owner, id := lockFixture(t, true)
	q.EXPECT().LockExerciseTestDeploysOf(gomock.Any(), owner.String()).Return(nil)
	q.EXPECT().ExtendOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{}, nil).AnyTimes()

	if err := uc.DestroyDeployTest(context.Background(), owner, id); err != nil {
		t.Fatal(err)
	}
	if len(checks.attempts) != 1 || !checks.afterTx[0] {
		t.Fatalf("one check after the commit expected, got %v (after commit %v)", checks.attempts, checks.afterTx)
	}
}
