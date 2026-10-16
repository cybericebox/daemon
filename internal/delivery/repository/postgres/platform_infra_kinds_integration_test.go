package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/testDeployRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// Test deploys are logged by triggers: a run starts with the deploy and ends
// when its row is removed, and the analytics read the time per lab kind.
func TestInfraKinds_TestLabRunsAreLoggedByTriggersAndCounted(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	exercises := exerciseRepo.New(db.Queries)
	deploys := testDeployRepo.New(db.Queries)
	author := seedVPNUser(t, db, "kinds-author@test.test")
	ex := mustCreateExercise(t, exercises, "IT infra kinds")
	variants := itVariants()
	variants[0].ID = uuid.Must(uuid.NewV7())
	version, err := exercises.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: variants}, itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	create := func(startedAgo time.Duration) exerciseModel.TestDeploy {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		got, createErr := deploys.Create(ctx, exerciseModel.TestDeploy{ID: id, GroupName: "t-" + id.String(), VersionID: version.ID, VariantID: variants[0].ID,
			CreatedBy: author, CreatedAt: now.Add(-startedAgo), ExpiresAt: now.Add(time.Hour)})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return got
	}
	stopped := create(2 * time.Hour)
	create(time.Hour)
	if n, err := deploys.DeleteOwned(ctx, stopped.ID, author); err != nil || n != 1 {
		t.Fatalf("delete: %d, %v", n, err)
	}

	repo := platformAnalyticsRepo.New(db.Queries)
	from, to := now.Add(-3*time.Hour), now.Add(time.Hour)
	rows, err := repo.InfraHoursByKind(ctx, from, to, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Kind != "event" || rows[1].Kind != "moderators" || rows[2].Kind != "test" {
		t.Fatalf("rows = %+v, want event, moderators, test", rows)
	}
	// The stopped lab ran about two hours (its removal is "now"), the running one one hour so far.
	if test := rows[2]; test.Labs != 2 || test.Hours < 2.9 || test.Hours > 3.2 {
		t.Fatalf("test lab hours = %+v, want 2 labs and about 3 hours", test)
	}
	if rows[0].Hours != 0 || rows[1].Labs != 0 {
		t.Fatalf("stand kinds must be empty: %+v", rows)
	}

	peaks, err := repo.InfraPeaksByKind(ctx, from, to, now, "hour", false, true)
	if err != nil {
		t.Fatal(err)
	}
	var peak int64
	for _, p := range peaks {
		peak = max(peak, p.Peak)
	}
	if peak != 2 {
		t.Fatalf("test lab peak = %d (%+v), want 2 labs at once", peak, peaks)
	}
	if stands, err := repo.InfraPeaksByKind(ctx, from, to, now, "hour", true, false); err != nil {
		t.Fatal(err)
	} else {
		for _, p := range stands {
			if p.Peak != 0 {
				t.Fatalf("team stand peak = %+v, want none", stands)
			}
		}
	}

	counts, err := repo.InfraKindCounts(ctx, now)
	if err != nil || counts.TestActive != 1 || counts.TestExpired != 0 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

// A static test is not a laboratory: its run adds no lab hours and no concurrency to the test-lab analytics.
func TestInfraKinds_StaticRunsAreNotLabs(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	exercises := exerciseRepo.New(db.Queries)
	deploys := testDeployRepo.New(db.Queries)
	author := seedVPNUser(t, db, "kinds-static@test.test")
	ex := mustCreateExercise(t, exercises, "IT infra kinds static")
	variants := itVariants()
	variants[0].ID = uuid.Must(uuid.NewV7())
	version, err := exercises.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: variants}, itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	create := func(group string) {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		if _, createErr := deploys.Create(ctx, exerciseModel.TestDeploy{ID: id, GroupName: group, VersionID: version.ID, VariantID: variants[0].ID,
			CreatedBy: author, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}); createErr != nil {
			t.Fatal(createErr)
		}
	}
	create("")
	create("t-lab")

	repo := platformAnalyticsRepo.New(db.Queries)
	from, to := now.Add(-3*time.Hour), now.Add(time.Hour)
	rows, err := repo.InfraHoursByKind(ctx, from, to, now)
	if err != nil {
		t.Fatal(err)
	}
	if test := rows[2]; test.Labs != 1 || test.Hours < 0.9 || test.Hours > 1.2 {
		t.Fatalf("test lab hours = %+v, want only the lab: 1 lab, about 1 hour", test)
	}
	peaks, err := repo.InfraPeaksByKind(ctx, from, to, now, "hour", false, true)
	if err != nil {
		t.Fatal(err)
	}
	var peak int64
	for _, p := range peaks {
		peak = max(peak, p.Peak)
	}
	if peak != 1 {
		t.Fatalf("test lab peak = %d, want 1 (a static test is not a lab)", peak)
	}
}
