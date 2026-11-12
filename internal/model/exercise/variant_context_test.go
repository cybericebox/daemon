package exerciseModel_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

// twoVariants is a version of two variants whose tasks line up; the tests break one of them and read the public context.
func twoVariants() exerciseModel.ExerciseVersion {
	task := validTask()
	task.Key = "a"
	other := task
	other.Check = exerciseModel.FlagCheck("ICE{x}") // its own check: the variants do not share the answer
	return exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{
		{Tasks: []exerciseModel.Task{task}},
		{Tasks: []exerciseModel.Task{other}},
	}}
}

// The variant is the public context key of an error about something inside a variant: the 1-based position in the
// version, set by the structure check and by the publish check alike, so a task key or a device name that repeats in
// every variant is told apart.
func TestValidationErrorsNameTheVariant(t *testing.T) {
	microvm := func(v *exerciseModel.Variant) {
		device := dev("db", exerciseModel.DeviceTypeContainer)
		device.Profile = exerciseModel.NodeProfileMicroVM
		device.Persistence = &exerciseModel.DevicePersistence{Enabled: true}
		v.Topology.Devices = []exerciseModel.Device{device}
	}
	for name, tc := range map[string]struct {
		mutate  func(*exerciseModel.Variant)
		want    appErr.ErrorCreator
		publish bool
		// differs: the error is found by comparing the variants with the first one, so it names the one that differs
		// from it, the second, whichever of the two was broken.
		differs bool
	}{
		"task key (23501)": {func(v *exerciseModel.Variant) { v.Tasks[0].Key = "Bad-Key" }, exerciseModel.ErrTaskKeyInvalid, false, false},
		"requires (23502)": {func(v *exerciseModel.Variant) { v.Tasks[0].Requires = []string{"ghost"} }, exerciseModel.ErrTaskRequiresInvalid, false, false},
		"vpn option (23520)": {func(v *exerciseModel.Variant) {
			v.Topology.VPN.ReverseConnections = true
		}, exerciseModel.ErrVPNOptionInvalid, false, false},
		"device type": {func(v *exerciseModel.Variant) {
			v.Topology.Devices = []exerciseModel.Device{dev("x", "bogus")}
		}, exerciseModel.ErrDeviceTypeInvalid, false, false},
		"persistence debounce": {func(v *exerciseModel.Variant) {
			device := dev("db", exerciseModel.DeviceTypeContainer)
			device.Persistence = &exerciseModel.DevicePersistence{Enabled: true, Debounce: "fast"}
			v.Topology.Devices = []exerciseModel.Device{device}
		}, exerciseModel.ErrDevicePersistenceInvalid, false, false},
		"persistence on microvm (23105)": {microvm, exerciseModel.ErrNodeProfilePersistence, false, false},
		"profile": {func(v *exerciseModel.Variant) {
			device := dev("db", exerciseModel.DeviceTypeContainer)
			device.Profile = "bogus"
			v.Topology.Devices = []exerciseModel.Device{device}
		}, exerciseModel.ErrNodeProfileInvalid, false, false},
		"answer type differs from the other variant": {func(v *exerciseModel.Variant) {
			v.Tasks[0].Check.Answer.Type = exerciseModel.AnswerTypeText
			v.Tasks[0].Check.Answer.Values = []string{"plain"}
		}, exerciseModel.ErrTaskCheckInvalid, false, true},
		"description (publish)": {func(v *exerciseModel.Variant) { v.Tasks[0].Description = nil }, exerciseModel.ErrTaskDescriptionRequired, true, false},
		"no check (publish)":    {func(v *exerciseModel.Variant) { v.Tasks[0].Check = exerciseModel.Check{} }, exerciseModel.ErrTaskCheckInvalid, true, true},
	} {
		for position := range 2 {
			t.Run(name, func(t *testing.T) {
				version := twoVariants()
				tc.mutate(&version.Variants[position])
				var err error
				if tc.publish {
					err = version.ValidateForPublish()
				} else {
					err = version.ValidateStructure()
				}
				require.ErrorIs(t, err, tc.want.Err())
				expected := position + 1
				if tc.differs {
					expected = 2
				}
				require.Equal(t, expected, publicContext(t, err)["variant"], "the variant is its 1-based position")
			})
		}
	}
}

// The whole exercise has no variant: an error about the set keeps none.
func TestWholeExerciseErrorsHaveNoVariant(t *testing.T) {
	err := (&exerciseModel.ExerciseVersion{}).ValidateStructure()
	require.ErrorIs(t, err, exerciseModel.ErrVersionNoVariants.Err())
	var carrier interface{ PublicContext() map[string]any }
	if errors.As(err, &carrier) {
		require.NotContains(t, carrier.PublicContext(), "variant")
	}
}

// A deploy meets errors of the variant's content and errors of the platform: only the first kind gets the variant, and
// the place that already named a variant keeps its value.
func TestInVariantIfScoped(t *testing.T) {
	for name, scoped := range map[string]error{
		"lab option (73512)":       exerciseModel.ErrLabOptionUnavailable.WithPublicContext("options", "checks").Err(),
		"runtime not supported":    exerciseModel.ErrRuntimeNotSupported.Err(),
		"service command unknown":  exerciseModel.ErrServiceCommandUnknown.Err(),
		"device unresolved":        exerciseModel.ErrRuntimeDeviceUnresolved.Err(),
		"bootstrap too large":      exerciseModel.ErrBootstrapTooLarge.Err(),
		"substitution unresolved":  exerciseModel.ErrSubstitutionUnresolved.Err(),
		"variable of the document": exerciseModel.ErrVariableInvalid.Err(),
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, 3, publicContext(t, exerciseModel.InVariantIfScoped(scoped, 3))["variant"])
		})
	}
	platform := exerciseModel.ErrTestDeployGroupBusy.Err()
	require.Equal(t, platform, exerciseModel.InVariantIfScoped(platform, 3), "a platform error is not about a variant")
	named := exerciseModel.ErrDeviceNameReserved.WithPublicContext("variant", 2).Err()
	require.Equal(t, 2, publicContext(t, exerciseModel.InVariant(named, 5))["variant"], "an error that names a variant keeps it")
	require.NoError(t, exerciseModel.InVariant(nil, 1))
	whole := exerciseModel.ErrVersionNoVariants.Err()
	require.Equal(t, whole, exerciseModel.InVariant(whole, 0), "number 0 is the whole exercise")
	require.NotContains(t, publicContext(t, exerciseModel.InVariant(whole, 0)), "variant")
}

// The publish errors about one task name the task and the field the author fixes, in public: the reader is the author.
func TestPublishErrorsNameTheTaskAndField(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate  func(*exerciseModel.Task)
		want    appErr.ErrorCreator
		field   string
		publish bool
	}{
		"name":        {func(task *exerciseModel.Task) { task.Name = "" }, exerciseModel.ErrTaskNameInvalid, "name", false},
		"description": {func(task *exerciseModel.Task) { task.Description = nil }, exerciseModel.ErrTaskDescriptionRequired, "description", true},
		"hint text": {func(task *exerciseModel.Task) {
			task.Hints = []exerciseModel.Hint{{Level: exerciseModel.HintLevelNudge}}
		}, exerciseModel.ErrHintTextRequired, "hints[1].text", true},
		"flag source": {func(task *exerciseModel.Task) {
			task.Check = exerciseModel.Check{Answer: &exerciseModel.AnswerCheck{Type: exerciseModel.AnswerTypeFlag}}
		}, exerciseModel.ErrFlagSourceInvalid, "check.answer.values", true},
		"several values without a device": {func(task *exerciseModel.Task) {
			task.Check = exerciseModel.Check{Answer: &exerciseModel.AnswerCheck{Type: exerciseModel.AnswerTypeFlag, Values: []string{"ICE{a}", "ICE{b}"}}}
		}, exerciseModel.ErrFlagMultiNeedsDevice, "check.answer.values", true},
	} {
		t.Run(name, func(t *testing.T) {
			version := twoVariants()
			for i := range version.Variants {
				tc.mutate(&version.Variants[i].Tasks[0])
			}
			var err error
			if tc.publish {
				err = version.ValidateForPublish()
			} else {
				err = version.ValidateStructure()
			}
			require.ErrorIs(t, err, tc.want.Err())
			public := publicContext(t, err)
			require.Equal(t, tc.field, public["field"])
			require.Equal(t, version.Variants[0].Tasks[0].Name, public["task"], "the task is public")
			require.Equal(t, 1, public["variant"])
		})
	}
}
