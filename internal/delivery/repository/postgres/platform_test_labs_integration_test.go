package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformStandRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/testDeployRepo"
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
