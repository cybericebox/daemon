package exerciseModel_test

import (
	"errors"
	"fmt"
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
			out := gw("out", exerciseModel.DeviceTypeInternetGateway)
			out.ReverseConnections = true
			v.Topology.Devices = []exerciseModel.Device{out}
		}, exerciseModel.ErrVPNOptionInvalid, false, false},
		"gateway limit (23522)": {func(v *exerciseModel.Variant) {
			for i := range exerciseModel.MaxLabGateways + 1 {
				v.Topology.Devices = append(v.Topology.Devices, gw(fmt.Sprintf("g%d", i+1), exerciseModel.DeviceTypeVPNGateway))
			}
		}, exerciseModel.ErrGatewayLimit, false, false},
		"gateway device (23524)": {func(v *exerciseModel.Variant) {
			g := gw("gate", exerciseModel.DeviceTypeVPNGateway)
			g.Image = "img"
			v.Topology.Devices = []exerciseModel.Device{g}
		}, exerciseModel.ErrGatewayDeviceInvalid, false, false},
		"two gateways in one network (23523, publish)": {func(v *exerciseModel.Variant) {
			a, b := gw("a", exerciseModel.DeviceTypeVPNGateway), gw("b", exerciseModel.DeviceTypeInternetGateway)
			v.Topology.Devices = []exerciseModel.Device{a, b}
			v.Topology.Connections = []exerciseModel.Connection{conn(devEP(a, "eth0"), devEP(b, "eth0"))}
		}, exerciseModel.ErrGatewayOneNetwork, true, false},
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
			require.Equal(t, "a", public["task"], "the task is its key, not its name")
			require.Equal(t, version.Variants[0].Tasks[0].Name, public["task_name"], "the name is a key of its own")
			require.Equal(t, 1, public["variant"])
		})
	}
}

// One rule for every error of the exercise document: the public task is the task KEY and its name is task_name; a task of an
// old draft that has no key yet is named by task_name alone.
func TestTaskInPublicContextIsTheKey(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*exerciseModel.Task)
		check  func(*exerciseModel.ExerciseVersion) error
		want   appErr.ErrorCreator
	}{
		"task key": {func(task *exerciseModel.Task) { task.Key = "Bad-Key" }, (*exerciseModel.ExerciseVersion).ValidateStructure, exerciseModel.ErrTaskKeyInvalid},
		"requires": {func(task *exerciseModel.Task) { task.Requires = []string{"ghost"} }, (*exerciseModel.ExerciseVersion).ValidateStructure, exerciseModel.ErrTaskRequiresInvalid},
		"answer":   {func(task *exerciseModel.Task) { task.Check.Answer.Type = "bogus" }, (*exerciseModel.ExerciseVersion).ValidateStructure, exerciseModel.ErrAnswerInvalid},
		"substitution": {func(task *exerciseModel.Task) {
			task.Description = []byte(`{"root":{"children":[{"type":"text","text":"{{task.a}}"}]}}`)
		}, (*exerciseModel.ExerciseVersion).ValidateStructure, exerciseModel.ErrSubstitutionNotAllowed},
		"description": {func(task *exerciseModel.Task) { task.Description = nil }, (*exerciseModel.ExerciseVersion).ValidateForPublish, exerciseModel.ErrTaskDescriptionRequired},
	} {
		t.Run(name, func(t *testing.T) {
			version := twoVariants()
			tc.mutate(&version.Variants[0].Tasks[0])
			err := tc.check(&version)
			require.ErrorIs(t, err, tc.want.Err())
			public := publicContext(t, err)
			wantKey := "a"
			if name == "task key" {
				wantKey = "Bad-Key"
			}
			require.Equal(t, wantKey, public["task"])
			require.Equal(t, "Find the flag", public["task_name"])
		})
	}
	unkeyed := twoVariants()
	unkeyed.Variants[0].Tasks[0].Key = ""
	unkeyed.Variants[0].Tasks[0].Description = nil
	err := unkeyed.ValidateForPublish()
	require.ErrorIs(t, err, exerciseModel.ErrTaskDescriptionRequired.Err())
	require.NotContains(t, publicContext(t, err), "task")
	require.Equal(t, "Find the flag", publicContext(t, err)["task_name"])
}
