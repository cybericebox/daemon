package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformStandRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/testDeployRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The platform test-lab list reads every author's deploys with the exercise
// name, the variant's index and the author; the counts split active and expired leases.
func TestPlatformTestLabs_ListsEveryAuthorWithExerciseVariantAndCounts(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	exercises := exerciseRepo.New(db.Queries)
	deploys := testDeployRepo.New(db.Queries)
	first, second := seedVPNUser(t, db, "ptl-first@test.test"), seedVPNUser(t, db, "ptl-second@test.test")

	ex := mustCreateExercise(t, exercises, "IT platform test lab")
	// The deploy runs the second variant: the list reports its 1-based position.
	variants := append(itVariants(), itVariants()...)
	variants[0].ID, variants[1].ID = uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	version, err := exercises.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: variants}, itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	create := func(user uuid.UUID, expires time.Time) exerciseModel.TestDeploy {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		got, createErr := deploys.Create(ctx, exerciseModel.TestDeploy{ID: id, GroupName: "t-" + id.String(), VersionID: version.ID, VariantID: variants[1].ID,
			CreatedBy: user, CreatedAt: now.Add(-time.Minute), ExpiresAt: expires})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return got
	}
	active := create(first, now.Add(time.Hour))
	create(second, now.Add(-time.Second))

	repo := platformStandRepo.New(db.Queries)
	items, total, err := repo.ListTestLabs(ctx, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("total %d items %d, want 2 and 2", total, len(items))
	}
	var got *platformStandRepo.TestLab
	for i := range items {
		if items[i].ID == active.ID {
			got = &items[i]
		}
	}
	if got == nil || got.ExerciseID != ex.ID || got.ExerciseName != "IT platform test lab" || got.AuthorID != first || got.AuthorEmail != "ptl-first@test.test" || got.VariantNumber != 2 {
		t.Fatalf("active lab = %+v, want variant number 2", got)
	}

	if found, _, err := repo.ListTestLabs(ctx, "ptl-second", 10, 0); err != nil || len(found) != 1 {
		t.Fatalf("search by author email: %v, %v", found, err)
	}
	counts, err := repo.CountTestLabs(ctx, now)
	if err != nil || counts.Active != 1 || counts.Expired != 1 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
	ref, err := repo.GetTestLab(ctx, active.ID)
	if err != nil || ref.OwnerID != first || ref.GroupName != active.GroupName {
		t.Fatalf("ref = %+v, %v", ref, err)
	}
}

// A static test (empty group and lab name, no event) never protects a group from the sweep, is absent from the platform test labs (list, count, get), and still leaves its run in the lab-hour history.
func TestPlatformTestLabs_StaticRowIsNotListed(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	exercises := exerciseRepo.New(db.Queries)
	deploys := testDeployRepo.New(db.Queries)
	owner := seedVPNUser(t, db, "ptl-static@test.test")
	ex := mustCreateExercise(t, exercises, "IT static test lab")
	version, err := exercises.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: itVariants()}, itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	create := func(group, lab string) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		if _, createErr := deploys.Create(ctx, exerciseModel.TestDeploy{ID: id, GroupName: group, LabName: lab, VersionID: version.ID, VariantID: uuid.Must(uuid.NewV7()),
			CreatedBy: owner, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); createErr != nil {
			t.Fatal(createErr)
		}
		return id
	}
	staticID := create("", "")
	labID := create("tu-a", "l-one")

	names, err := db.Queries.ListTestDeployGroupNames(ctx, []string{"", "tu-a"})
	if err != nil || len(names) != 1 || names[0] != "tu-a" {
		t.Fatalf("group names = %v, %v; want only tu-a", names, err)
	}

	items, total, err := platformStandRepo.New(db.Queries).ListTestLabs(ctx, "", 10, 0)
	if err != nil || len(items) != 1 || items[0].ID != labID || total != 1 {
		t.Fatalf("items = %v, total = %d, %v; want only the lab", items, total, err)
	}
	counts, err := db.Queries.CountPlatformTestLabs(ctx, now)
	if err != nil || counts.Active != 1 || counts.Expired != 0 {
		t.Fatalf("counts = %+v, %v; want only the lab", counts, err)
	}
	if _, refErr := platformStandRepo.New(db.Queries).GetTestLab(ctx, staticID); !repositoryTools.IsObjectNotFoundError(refErr) {
		t.Fatalf("static ref: want not found, got %v", refErr)
	}
	if ref, refErr := platformStandRepo.New(db.Queries).GetTestLab(ctx, labID); refErr != nil || ref.ID != labID {
		t.Fatalf("lab ref = %+v, %v", ref, refErr)
	}

	runs := func(id uuid.UUID) (started, ended bool) {
		t.Helper()
		if scanErr := db.Pool.QueryRow(ctx, `SELECT true, ended_at IS NOT NULL FROM exercise_test_deploy_runs WHERE deploy_id = $1`, id).Scan(&started, &ended); scanErr != nil {
			t.Fatalf("run of %v: %v", id, scanErr)
		}
		return started, ended
	}
	if _, ended := runs(staticID); ended {
		t.Fatal("the run of a live static test is open")
	}
	if _, err = db.Pool.Exec(ctx, `DELETE FROM exercise_test_deployments WHERE id = $1`, staticID); err != nil {
		t.Fatal(err)
	}
	if _, ended := runs(staticID); !ended {
		t.Fatal("deleting the static test closes its run")
	}
}
