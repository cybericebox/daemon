package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The resource totals read only the size fields of the devices of the published version (or of the pinned
// version), never the image, env vars or secrets.
func TestPublishedAndPinnedVariantsCarryOnlyWhatTheTotalsNeed(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExercise(t, repo, "IT resources")

	variants := itVariants()
	deviceA, deviceB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variants[0].Topology.Devices = []exerciseModel.Device{
		{ID: deviceA, Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "secret-registry/img", ResourcePreset: "medium",
			EnvVars: []exerciseModel.EnvVar{{Name: "PASS", Value: "ciphertext", Secret: true}}},
		{ID: deviceB, Name: "db", Type: exerciseModel.DeviceTypeContainer, ResourcePreset: "xlarge"},
		{ID: uuid.Must(uuid.NewV7()), Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch},
	}
	_, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: variants}, itNow, uuid.NullUUID{})
	require.NoError(t, err)

	none, err := repo.PublishedVariants(ctx, []uuid.UUID{ex.ID})
	require.NoError(t, err)
	require.Empty(t, none, "a draft is not published")

	published, err := repo.Publish(ctx, ex.ID, itNow.Add(time.Hour))
	require.NoError(t, err)

	got, err := repo.PublishedVariants(ctx, []uuid.UUID{ex.ID})
	require.NoError(t, err)
	require.Len(t, got[ex.ID], len(variants))
	devices := got[ex.ID][0].Topology.Devices
	require.Len(t, devices, 3)
	require.Equal(t, "medium", devices[0].ResourcePreset)
	require.Empty(t, devices[0].Image, "images are not read")
	require.Empty(t, devices[0].EnvVars, "secrets are not read")
	require.Equal(t, "xlarge", devices[1].ResourcePreset)

	policy := resourcesModel.DefaultPolicy()
	total := policy.Total(got[ex.ID][0].Topology)
	require.Equal(t, resourcesModel.Totals{Devices: 2, Blocks: 80, Amount: resourcesModel.Amount{CPUMillicores: 625, MemoryBytes: 512<<20 + 2<<30}}, total)

	byVersion, err := repo.VersionVariants(ctx, []uuid.UUID{published.ID})
	require.NoError(t, err)
	require.Equal(t, got[ex.ID][0].Topology.Devices, byVersion[published.ID][0].Topology.Devices)

	empty, err := repo.VersionVariants(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
}
