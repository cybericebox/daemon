package exercise

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

type profileSettings struct {
	ISettings

	profiles exerciseModel.SupportedProfiles
	err      error
}

func (s profileSettings) NodeProfiles(context.Context) (exerciseModel.SupportedProfiles, error) {
	return s.profiles, s.err
}

func microVMVariants() []exerciseModel.Variant {
	return []exerciseModel.Variant{
		{Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{
			{
				Name:    "kernel",
				Type:    exerciseModel.DeviceTypeNode,
				Profile: exerciseModel.NodeProfileMicroVM,
			},
		}}},
	}
}

// Save, publish and import refuse a profile the platform did not tick; the container only is the default, also
// when the setting cannot be read.
func TestRequireProfilesSupported(t *testing.T) {
	ctx := context.Background()
	variants := microVMVariants()

	require.ErrorIs(
		t,
		(&ExerciseUseCase{}).requireProfilesSupported(ctx, variants),
		exerciseModel.ErrNodeProfileNotSupported.Err(),
	)
	require.NoError(
		t,
		(&ExerciseUseCase{}).requireProfilesSupported(
			ctx,
			[]exerciseModel.Variant{
				{
					Topology: exerciseModel.Topology{
						Devices: []exerciseModel.Device{
							{Name: "web", Type: exerciseModel.DeviceTypeContainer},
						},
					},
				},
			},
		),
	)

	ticked := &ExerciseUseCase{
		settings: profileSettings{
			profiles: exerciseModel.SupportedProfiles{Container: true, MicroVM: true},
		},
	}
	require.NoError(t, ticked.requireProfilesSupported(ctx, variants))

	unreadable := &ExerciseUseCase{settings: profileSettings{err: errors.New("db down")}}
	require.ErrorIs(
		t,
		unreadable.requireProfilesSupported(ctx, variants),
		exerciseModel.ErrNodeProfileNotSupported.Err(),
	)
}

// An archive version of any status, a draft included, is refused when its node has an unticked profile.
func TestValidateImportContent_RefusesAnUnsupportedProfile(t *testing.T) {
	parsed := &ParsedExerciseArchive{
		Versions: []exerciseModel.ExerciseVersion{
			{Status: exerciseModel.VersionStatusDraft, Variants: microVMVariants()},
		},
	}
	err := (&ExerciseUseCase{}).validateImportContent(context.Background(), parsed, false)
	require.ErrorIs(t, err, exerciseModel.ErrNodeProfileNotSupported.Err())
}

// An archive version of any status is refused when a node is named like a platform endpoint; the error names the
// version, the variant and the device.
func TestValidateImportContent_RefusesAReservedDeviceName(t *testing.T) {
	parsed := &ParsedExerciseArchive{
		Versions: []exerciseModel.ExerciseVersion{
			{Status: exerciseModel.VersionStatusDraft, Variants: []exerciseModel.Variant{
				{
					Topology: exerciseModel.Topology{
						Devices: []exerciseModel.Device{
							{Name: "gateway", Type: exerciseModel.DeviceTypeNode},
						},
					},
				},
			}},
		},
	}
	err := (&ExerciseUseCase{}).validateImportContent(context.Background(), parsed, false)
	require.ErrorIs(t, err, exerciseModel.ErrDeviceNameReserved.Err())
}
