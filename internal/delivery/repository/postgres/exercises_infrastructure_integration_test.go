package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// TestExerciseVariantsHaveInfrastructure pins the SQL twin of
// exerciseModel.HasInfrastructure: a Go nil device list is stored as
// "devices": null, which the lax jsonpath once counted as a device.
func TestExerciseVariantsHaveInfrastructure(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		variants string
		want     bool
	}{
		{"no variants", `[]`, false},
		{"no topology", `[{"index":0}]`, false},
		{"null devices", `[{"topology":{"vpn":{"enabled":true},"devices":null,"connections":null}}]`, false},
		{"empty devices", `[{"topology":{"devices":[]}}]`, false},
		{"device", `[{"topology":{"devices":null}},{"topology":{"devices":[{"name":"host-1"}]}}]`, true},
	} {
		var got bool
		if err := db.Pool.QueryRow(ctx, `SELECT exercise_variants_have_infrastructure($1::jsonb)`, tc.variants).Scan(&got); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: infrastructure = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestExerciseHasInfrastructure_FromSavedTopology round-trips versions saved
// by the repository: the published version decides, and an exercise whose
// variants carry no devices is never infrastructure.
func TestExerciseHasInfrastructure_FromSavedTopology(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)

	save := func(ex exerciseModel.Exercise, variants []exerciseModel.Variant, publish bool) {
		t.Helper()
		if _, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: variants}, itNow, uuid.NullUUID{}); err != nil {
			t.Fatalf("UpsertDraft: %v", err)
		}
		if publish {
			if _, err := repo.Publish(ctx, ex.ID, itNow.Add(time.Hour)); err != nil {
				t.Fatalf("Publish: %v", err)
			}
		}
	}
	check := func(ex exerciseModel.Exercise, want bool) {
		t.Helper()
		got, err := repo.HasInfrastructure(ctx, ex.ID)
		if err != nil || got != want {
			t.Fatalf("%s: HasInfrastructure = %v (err %v), want %v", ex.Name, got, err, want)
		}
	}
	withDevice := func() []exerciseModel.Variant {
		variants := itVariants()
		variants[0].Topology.Devices = []exerciseModel.Device{{ID: uuid.Must(uuid.NewV7()), Name: "host-1", Type: "container"}}
		return variants
	}

	plain := mustCreateExercise(t, repo, "IT no topology")
	check(plain, false)
	save(plain, itVariants(), true)
	check(plain, false)
	// A device in the unpublished draft does not change the published answer.
	save(plain, withDevice(), false)
	check(plain, false)

	lab := mustCreateExercise(t, repo, "IT with device")
	save(lab, withDevice(), true)
	check(lab, true)
}
