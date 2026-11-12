package exercise

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

func publicOf(t *testing.T, failure error) map[string]any {
	t.Helper()
	var coded appErr.Error
	require.ErrorAs(t, failure, &coded)
	return coded.PublicContext()
}

// The variant of an error is its 1-based position in the version: the bootstrap size gate of a draft and the resources
// gate of a publish name the variant that is wrong, not the first one.
func TestGatesNameTheVariant(t *testing.T) {
	ctx := context.Background()

	big := uuid.Must(uuid.NewV7())
	u := &ExerciseUseCase{files: &sizedFiles{sizes: map[uuid.UUID]int64{big: exerciseModel.MaxBootstrapFileBytes + 1}}}
	withFile := exerciseModel.Variant{Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{
		Name: "web", Type: exerciseModel.DeviceTypeNode, Bootstrap: exerciseModel.Bootstrap{Files: []exerciseModel.BootstrapFile{
			{Path: "/f", Source: &exerciseModel.AttachmentRef{FileID: big}},
		}},
	}}}}
	err := u.requireBootstrapSizes(ctx, []exerciseModel.Variant{{}, withFile})
	require.ErrorIs(t, err, exerciseModel.ErrBootstrapTooLarge.Err())
	require.Equal(t, 2, publicOf(t, err)["variant"])
	require.Equal(t, "web", publicOf(t, err)["device"])

	unknown := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "x", Type: exerciseModel.DeviceTypeContainer, ResourcePreset: "gigantic"}
	policy := &ExerciseUseCase{elevations: &fakeElevations{}}
	err = policy.requireResourcesAllowed(ctx, uuid.Must(uuid.NewV7()), []exerciseModel.Variant{variantOf(), variantOf(), variantOf(unknown)})
	require.ErrorIs(t, err, exerciseModel.ErrDeviceResourcePresetInvalid.Err())
	require.Equal(t, 3, publicOf(t, err)["variant"], "a number, like every other error that names a variant")
}

// An import issue keeps the variant in its context and builds its path from it; a variant checked alone is named by
// its position in the version, not by the 1 the model saw.
func TestImportIssueNamesTheVariant(t *testing.T) {
	variants := []exerciseModel.Variant{
		{},
		{Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "web"}, {Name: "db"}}}},
	}
	named := exerciseModel.ErrNodeProfilePersistence.WithPublicContext("device", "db").
		WithPublicContext("variant", 2).WithPublicContext("field", "persistence").Err()
	issue, ok := issueFrom(named, variants, 0)
	require.True(t, ok)
	require.Equal(t, "variants[2].topology.devices[2]", issue.Path)
	require.Equal(t, 2, issue.Context["variant"])
	require.Equal(t, "persistence", issue.Context["field"])

	alone := exerciseModel.ErrNodeProfilePersistence.WithPublicContext("device", "db").WithPublicContext("variant", 1).Err()
	issue, ok = issueFrom(alone, variants, 2)
	require.True(t, ok)
	require.Equal(t, "variants[2].topology.devices[2]", issue.Path)
	require.Equal(t, 2, issue.Context["variant"], "the position in the version wins over the 1 of a variant checked alone")

	whole, ok := issueFrom(exerciseModel.ErrVersionNoVariants.Err(), variants, 0)
	require.True(t, ok)
	require.Empty(t, whole.Path)
	require.NotContains(t, whole.Context, "variant")
}

// The archive path of a publish-gate error is built from the variant number, which stays in the context.
func TestImportedPathKeepsTheVariant(t *testing.T) {
	variants := []exerciseModel.Variant{
		{},
		{Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "db"}}}},
	}
	failure := withImportedPath(exerciseModel.ErrNodeProfileNotSupported.WithPublicContext("variant", 2).
		WithPublicContext("device", "db").Err(), 0, variants)
	public := publicOf(t, failure)
	require.Equal(t, "versions[1].variants[2].topology.devices[1]", public["field"])
	require.Equal(t, 2, public["variant"])
}
