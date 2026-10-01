package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/testDeployRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The exercise-scoped list joins through the version, is owner-only, and keeps
// each deploy's flags for its author.
func TestListOwnedTestDeploysForExercise_ScopesByExerciseAndOwner(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	exercises := exerciseRepo.New(db.Queries)
	deploys := testDeployRepo.New(db.Queries)
	owner, stranger := seedVPNUser(t, db, "td-owner@test.test"), seedVPNUser(t, db, "td-stranger@test.test")

	versionOf := func(name string) uuid.UUID {
		ex := mustCreateExercise(t, exercises, name)
		v, err := exercises.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: itVariants()}, itNow, uuid.NullUUID{})
		if err != nil {
			t.Fatal(err)
		}
		return v.ID
	}
	mine, other := versionOf("IT deploy mine"), versionOf("IT deploy other")
	exerciseOfMine, err := db.Queries.GetExerciseVersionByID(ctx, mine)
	if err != nil {
		t.Fatal(err)
	}

	create := func(version, user uuid.UUID, flags []exerciseModel.DeployFlag) exerciseModel.TestDeploy {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		got, createErr := deploys.Create(ctx, exerciseModel.TestDeploy{ID: id, GroupName: "t-" + id.String(), VersionID: version, VariantID: uuid.Must(uuid.NewV7()),
			CreatedBy: user, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), Flags: flags})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return got
	}
	want := create(mine, owner, []exerciseModel.DeployFlag{{TaskID: uuid.Must(uuid.NewV7()), Name: "Login", Flag: "ICE{a}"}})
	create(other, owner, nil)
	create(mine, stranger, nil)

	list, err := deploys.ListOwnedForExercise(ctx, owner, exerciseOfMine.ExerciseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != want.ID || len(list[0].Flags) != 1 || list[0].Flags[0].Flag != "ICE{a}" {
		t.Fatalf("list = %+v", list)
	}
	all, err := db.Queries.ListOwnedExerciseTestDeploys(ctx, owner)
	if err != nil || len(all) != 2 {
		t.Fatalf("all = %d, %v", len(all), err)
	}
}

// One active test lab per user: two concurrent creates of one owner are serialized by the
// per-owner lock, so exactly limit-many are stored; an expired lease does not count; another owner is free.
func TestCreateIfUnderLimit_ExactlyOneOfConcurrentCreatesWins(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	exercises := exerciseRepo.New(db.Queries)
	owner, other := seedVPNUser(t, db, "td-race-owner@test.test"), seedVPNUser(t, db, "td-race-other@test.test")
	ex := mustCreateExercise(t, exercises, "IT deploy race")
	version, err := exercises.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: itVariants()}, itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWorker[testDeployRepo.Queries](postgres.NewUoWFactory(db.Pool))

	create := func(user uuid.UUID, expires time.Time, limit int) (bool, error) {
		id := uuid.Must(uuid.NewV7())
		txCtx, repo, tx, err := uow.UnitOfWork(ctx)
		if err != nil {
			return false, err
		}
		defer func() { _ = tx.Restore() }()
		_, created, err := testDeployRepo.New(repo).CreateIfUnderLimit(txCtx, exerciseModel.TestDeploy{ID: id, GroupName: "t-" + id.String(), VersionID: version.ID,
			VariantID: uuid.Must(uuid.NewV7()), CreatedBy: user, CreatedAt: time.Now(), ExpiresAt: expires}, time.Now(), limit)
		if err != nil {
			return false, err
		}
		return created, tx.Save()
	}

	const racers = 8
	results := make(chan bool, racers)
	errs := make(chan error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		go func() {
			<-start
			created, err := create(owner, time.Now().Add(time.Hour), 1)
			results <- created
			errs <- err
		}()
	}
	close(start)
	won := 0
	for i := 0; i < racers; i++ {
		if <-results {
			won++
		}
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if won != 1 {
		t.Fatalf("exactly one concurrent create must win, %d did", won)
	}
	if created, err := create(other, time.Now().Add(time.Hour), 1); err != nil || !created {
		t.Fatalf("another owner is not blocked: %v %v", created, err)
	}
	// An expired lease is not active: the owner of an expired-only row may start again.
	if _, err := db.Pool.Exec(ctx, `UPDATE exercise_test_deployments SET expires_at = now() - interval '1 minute' WHERE created_by = $1`, owner); err != nil {
		t.Fatal(err)
	}
	if created, err := create(owner, time.Now().Add(time.Hour), 1); err != nil || !created {
		t.Fatalf("an expired lease does not block: %v %v", created, err)
	}
	// A higher limit admits more: with one active already, a limit of 3 allows exactly two more of many.
	won = 0
	for i := 0; i < 4; i++ {
		if created, err := create(owner, time.Now().Add(time.Hour), 3); err != nil {
			t.Fatal(err)
		} else if created {
			won++
		}
	}
	if won != 2 {
		t.Fatalf("a limit of 3 with one active admits two more, admitted %d", won)
	}
}

// A correct answer is added once, only for the deploy's own author.
func TestMarkTestDeploySolved_IsIdempotentAndOwnerScoped(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	exercises := exerciseRepo.New(db.Queries)
	deploys := testDeployRepo.New(db.Queries)
	owner, stranger := seedVPNUser(t, db, "td-solved-owner@test.test"), seedVPNUser(t, db, "td-solved-stranger@test.test")
	ex := mustCreateExercise(t, exercises, "IT deploy solved")
	v, err := exercises.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: itVariants()}, itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.Must(uuid.NewV7())
	if _, err = deploys.Create(ctx, exerciseModel.TestDeploy{ID: id, GroupName: "t-" + id.String(), VersionID: v.ID, VariantID: uuid.Must(uuid.NewV7()),
		CreatedBy: owner, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	task, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, taskID := range []uuid.UUID{task, task, other} {
		if _, err = deploys.MarkSolved(ctx, id, owner, taskID); err != nil {
			t.Fatal(err)
		}
	}
	got, err := deploys.GetOwned(ctx, id, owner)
	if err != nil || len(got.Solved) != 2 || got.Solved[0] != task || got.Solved[1] != other {
		t.Fatalf("solved = %v err %v", got.Solved, err)
	}
	if _, err = deploys.MarkSolved(ctx, id, stranger, uuid.Must(uuid.NewV7())); err == nil {
		t.Fatal("another user must not mark the deploy")
	}
}

// The author's test labs share one group: the group name is not unique, the lab name tells them apart.
func TestTestDeploys_ShareTheAuthorsGroup(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	exercises := exerciseRepo.New(db.Queries)
	deploys := testDeployRepo.New(db.Queries)
	owner := seedVPNUser(t, db, "td-shared@test.test")
	ex := mustCreateExercise(t, exercises, "IT deploy shared")
	v, err := exercises.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: itVariants()}, itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	for _, lab := range []string{"l-one", "l-two"} {
		if _, err = deploys.Create(ctx, exerciseModel.TestDeploy{ID: uuid.Must(uuid.NewV7()), GroupName: "tu-shared", LabName: lab, VersionID: v.ID,
			VariantID: uuid.Must(uuid.NewV7()), CreatedBy: owner, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatalf("%s: %v", lab, err)
		}
	}
	list, err := deploys.ListOwned(ctx, owner)
	if err != nil || len(list) != 2 || list[0].GroupName != list[1].GroupName || list[0].LabName == list[1].LabName {
		t.Fatalf("list = %+v err %v", list, err)
	}
}
