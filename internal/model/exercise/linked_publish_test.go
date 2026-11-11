package exerciseModel_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func publicContext(t *testing.T, err error) map[string]any {
	t.Helper()
	var carrier interface{ PublicContext() map[string]any }
	require.ErrorAs(t, err, &carrier, "the error carries no public context")
	return carrier.PublicContext()
}

// linkedPublishVersion is a base with one subtask and n linked variants, each with a flag of its own.
func linkedPublishVersion(n int) exerciseModel.ExerciseVersion {
	base := exerciseModel.Variant{
		ID:    uuid.Must(uuid.NewV7()),
		Tasks: []exerciseModel.Task{validTask()},
	}
	base.Tasks[0].Check = exerciseModel.FlagCheck("ICE{base}")
	variants := []exerciseModel.Variant{base}
	for i := range n {
		linked := exerciseModel.Variant{
			ID:       uuid.Must(uuid.NewV7()),
			LinkedTo: uuid.NullUUID{UUID: base.ID, Valid: true},
		}
		task := base.Tasks[0]
		task.Check = exerciseModel.FlagCheck("ICE{linked" + string(rune('a'+i)) + "}")
		linked.Tasks = []exerciseModel.Task{task}
		variants = append(variants, linked)
	}
	return exerciseModel.ExerciseVersion{Variants: variants}
}

func attachment(name string) []exerciseModel.AttachmentRef {
	return []exerciseModel.AttachmentRef{{FileID: uuid.Must(uuid.NewV7()), Name: name}}
}

func TestValidateForPublish_LinkedVariantsKeepFilesOfTheBase(t *testing.T) {
	version := linkedPublishVersion(2)
	version.Variants[0].Tasks[0].Attachments = attachment("task.bin")
	version.Variants[1].Tasks[0].Attachments = attachment("task.bin")

	err := version.ValidateForPublish()
	require.ErrorIs(t, err, exerciseModel.ErrLinkedAttachmentMissing.Err())
	assert.Equal(t, map[string]any{"variant": 3, "task": "Find the flag"}, publicContext(t, err))

	version.Variants[2].Tasks[0].Attachments = attachment("task.bin")
	require.NoError(t, version.ValidateForPublish())
}

func TestValidateForPublish_LinkedVariantsWithoutFilesOnTheBaseAreFine(t *testing.T) {
	version := linkedPublishVersion(2)
	version.Variants[1].Tasks[0].Attachments = attachment("own.txt")
	require.NoError(t, version.ValidateForPublish())
}

// templateLinkedVersion is a base with n linked variants whose flag is the same template injected into a device, the
// only way variants may share a flag value.
func templateLinkedVersion(n int) exerciseModel.ExerciseVersion {
	deviceID := uuid.Must(uuid.NewV7())
	version := linkedPublishVersion(n)
	for i := range version.Variants {
		task := &version.Variants[i].Tasks[0]
		task.Key = "flag"
		task.Check = exerciseModel.FlagCheck(`template:ICE{\d}`)
		version.Variants[i].Topology.Devices = []exerciseModel.Device{
			{
				ID:    deviceID,
				Name:  "srv",
				Type:  exerciseModel.DeviceTypeContainer,
				Image: "platform:srv@sha256:" + strings.Repeat("ab", 32),
				// The template reaches the device through its bootstrap env: that is the delivery.
				Bootstrap: exerciseModel.Bootstrap{
					Env: []exerciseModel.EnvVar{{Name: "FLAG", Value: "{{task.flag}}"}},
				},
			},
		}
	}
	return version
}

func TestValidateForPublish_LinkedVariantMustDifferFromItsBase(t *testing.T) {
	t.Run("same flag template, no files, no image", func(t *testing.T) {
		version := templateLinkedVersion(1)
		err := version.ValidateForPublish()
		require.ErrorIs(t, err, exerciseModel.ErrLinkedVariantIdentical.Err())
		assert.Equal(t, map[string]any{"variant": 2}, publicContext(t, err))
	})
	t.Run("own files make it differ", func(t *testing.T) {
		version := templateLinkedVersion(1)
		version.Variants[1].Tasks[0].Attachments = attachment("own.txt")
		require.NoError(t, version.ValidateForPublish())
	})
	t.Run("an image override makes it differ", func(t *testing.T) {
		version := templateLinkedVersion(1)
		device := version.Variants[1].Topology.Devices[0]
		version.Variants[1].ImageOverrides = []exerciseModel.ImageOverride{
			{DeviceID: device.ID, Image: "platform:other@sha256:" + strings.Repeat("cd", 32)},
		}
		require.NoError(t, version.ValidateForPublish())
	})
	t.Run("a flag of its own makes it differ", func(t *testing.T) {
		version := templateLinkedVersion(1)
		version.Variants[1].Tasks[0].Check = exerciseModel.FlagCheck(`template:ICE{[a-f]}`)
		require.NoError(t, version.ValidateForPublish())
	})
	t.Run("only the third is identical", func(t *testing.T) {
		version := templateLinkedVersion(3)
		version.Variants[1].Tasks[0].Check = exerciseModel.FlagCheck(`template:ICE{[a-f]}`)
		version.Variants[3].Tasks[0].Check = exerciseModel.FlagCheck(`template:ICE{[g-z]}`)
		err := version.ValidateForPublish()
		require.ErrorIs(t, err, exerciseModel.ErrLinkedVariantIdentical.Err())
		assert.Equal(t, map[string]any{"variant": 3}, publicContext(t, err))
	})
}

// The khctf PWN shape: 15 linked variants of one base, a flag injected into a device, the binary attached only to
// variant 1.
func TestValidateForPublish_KhctfShape(t *testing.T) {
	deviceID := uuid.Must(uuid.NewV7())
	version := linkedPublishVersion(14)
	for i := range version.Variants {
		task := &version.Variants[i].Tasks[0]
		task.Key = "flag"
		task.Check = exerciseModel.FlagCheck("ICE{pwn" + string(rune('a'+i)) + "}")
		version.Variants[i].Topology.Devices = []exerciseModel.Device{
			{
				ID:    deviceID,
				Name:  "pwn",
				Type:  exerciseModel.DeviceTypeContainer,
				Image: "platform:pwn@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Bootstrap: exerciseModel.Bootstrap{
					Env: []exerciseModel.EnvVar{{Name: "FLAG", Value: "{{task.flag}}"}},
				},
			},
		}
	}
	version.Variants[0].Tasks[0].Attachments = attachment("pwn")

	err := version.ValidateForPublish()
	if errors.Is(err, exerciseModel.ErrLinkedAttachmentMissing.Err()) {
		assert.Equal(
			t,
			map[string]any{"variant": 2, "task": "Find the flag"},
			publicContext(t, err),
		)
	} else {
		require.Fail(
			t,
			"the missing binary on the linked variants must block publishing",
			"got %v",
			err,
		)
	}

	for i := 1; i < len(version.Variants); i++ {
		version.Variants[i].Tasks[0].Attachments = attachment("pwn")
	}
	require.NoError(t, version.ValidateForPublish())
}
