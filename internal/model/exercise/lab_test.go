package exerciseModel_test

import (
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestCheckIDMapsBackToItsTask(t *testing.T) {
	id := exerciseModel.CheckID("ospf", 2)
	require.Equal(t, "ospf:2", id)
	key, ok := exerciseModel.TaskKeyOfCheck(id)
	require.True(t, ok)
	require.Equal(t, "ospf", key)

	// The longest key and the longest device position stay inside the laboratory's 64 characters.
	longest := exerciseModel.CheckID(strings.Repeat("a", 32), 20)
	require.LessOrEqual(t, len(longest), 64)

	for _, foreign := range []string{"", "ospf", "ospf:", "ospf:0", "ospf:x", ":1", "Ospf:1", "a:b:1"} {
		_, ok = exerciseModel.TaskKeyOfCheck(foreign)
		require.Falsef(t, ok, "%q is not an id of ours", foreign)
	}
}

func TestLabServiceFromImageAndAuthor(t *testing.T) {
	image := &exerciseModel.ImageService{
		Entrypoint: []string{"/entry.sh"},
		Cmd:        []string{"serve", "--port=80"},
		User:       "www",
		WorkingDir: "/srv",
	}
	node := func(mutate func(*exerciseModel.Device)) exerciseModel.Device {
		d := exerciseModel.Device{Name: "web", Type: exerciseModel.DeviceTypeNode, ImageService: image}
		mutate(&d)
		return d
	}

	// Nothing to tell: no bootstrap list, no command: the image decides, whatever the image says.
	service, err := node(func(*exerciseModel.Device) {}).LabService()
	require.NoError(t, err)
	require.Nil(t, service)
	service, err = node(func(d *exerciseModel.Device) { d.Bootstrap.Env = []exerciseModel.EnvVar{{Name: "A", Value: "1"}} }).
		LabService()
	require.NoError(t, err)
	require.Nil(t, service, "env alone needs no launcher")

	// A bootstrap list: the image's values.
	service, err = node(func(d *exerciseModel.Device) { d.Bootstrap.Script = "true" }).LabService()
	require.NoError(t, err)
	require.Equal(t, &exerciseModel.LabService{
		Command: []string{"/entry.sh"}, Args: []string{"serve", "--port=80"}, User: "www", WorkingDir: "/srv",
	}, service)

	// The author's command replaces entrypoint and cmd; the user stays the image's.
	service, err = node(func(d *exerciseModel.Device) { d.Command = []string{"/bin/app"}; d.Args = []string{"-v"} }).LabService()
	require.NoError(t, err)
	require.Equal(t, &exerciseModel.LabService{
		Command: []string{"/bin/app"}, Args: []string{"-v"}, User: "www", WorkingDir: "/srv",
	}, service)
	service, err = node(func(d *exerciseModel.Device) { d.Command = []string{"/bin/app"} }).LabService()
	require.NoError(t, err)
	require.Empty(t, service.Args, "an author command drops the image's cmd")

	// Args alone replace the image's cmd.
	service, err = node(func(d *exerciseModel.Device) { d.Args = []string{"--debug"} }).LabService()
	require.NoError(t, err)
	require.Equal(t, []string{"/entry.sh"}, service.Command)
	require.Equal(t, []string{"--debug"}, service.Args)

	// An image without an entrypoint runs its cmd as the command.
	cmdOnly := &exerciseModel.ImageService{Cmd: []string{"nginx", "-g", "daemon off;"}}
	service, err = node(func(d *exerciseModel.Device) { d.ImageService = cmdOnly; d.Bootstrap.Script = "true" }).LabService()
	require.NoError(t, err)
	require.Equal(t, []string{"nginx", "-g", "daemon off;"}, service.Command)
	require.Empty(t, service.Args)
}

func TestLabServiceUnknownNamesTheDevice(t *testing.T) {
	for name, device := range map[string]exerciseModel.Device{
		"image unknown": {Name: "web", Type: exerciseModel.DeviceTypeNode, Bootstrap: exerciseModel.Bootstrap{Script: "true"}},
		"image says nothing": {
			Name: "web", Type: exerciseModel.DeviceTypeNode, ImageService: &exerciseModel.ImageService{User: "app"},
			Bootstrap: exerciseModel.Bootstrap{Files: []exerciseModel.BootstrapFile{{Path: "/a", Content: "x"}}},
		},
		"args without an image": {Name: "web", Type: exerciseModel.DeviceTypeNode, Args: nil, Command: nil,
			Bootstrap: exerciseModel.Bootstrap{Script: "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := device.LabService()
			require.ErrorIs(t, err, exerciseModel.ErrServiceCommandUnknown.Err())
			require.Contains(t, err.Error(), "web")
		})
	}
	// The author's command is enough without an image.
	device := exerciseModel.Device{
		Name: "web", Type: exerciseModel.DeviceTypeNode, Command: []string{"/app"},
		Bootstrap: exerciseModel.Bootstrap{Script: "true"},
	}
	service, err := device.LabService()
	require.NoError(t, err)
	require.Equal(t, []string{"/app"}, service.Command)
}

func checksFixture() (exerciseModel.Variant, exerciseModel.Topology, []exerciseModel.ResolvedConditions) {
	r1, r2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	topology := exerciseModel.Topology{Devices: []exerciseModel.Device{
		{ID: r1, Name: "r1", Type: exerciseModel.DeviceTypeNode},
		{ID: r2, Name: "r2", Type: exerciseModel.DeviceTypeNode},
	}}
	ospf := validTask()
	ospf.Key = "ospf"
	ospf.Check = exerciseModel.Check{State: &exerciseModel.StateCheck{
		Interval: "30s", Manual: true, Mode: exerciseModel.StateModeContinuous,
		Conditions: []exerciseModel.Condition{
			{DeviceID: r1, File: &exerciseModel.FileObservation{Path: "/etc/frr/frr.conf", Contains: "router ospf"}},
			{DeviceID: r2, Timeout: "45s", Custom: &exerciseModel.CustomObservation{Run: "true"}},
			{DeviceID: r1, Timeout: "20s", Custom: &exerciseModel.CustomObservation{Run: "vtysh -c x"}},
		},
	}}
	quick := validTask()
	quick.Key = "quick"
	quick.Check = exerciseModel.Check{State: &exerciseModel.StateCheck{
		Interval:   "5s",
		Conditions: []exerciseModel.Condition{{DeviceID: r2, File: &exerciseModel.FileObservation{Path: "/a"}}},
	}}
	manual := validTask()
	manual.Key = "manual"
	manual.Check = exerciseModel.Check{State: &exerciseModel.StateCheck{
		Conditions: []exerciseModel.Condition{{DeviceID: r1, Timeout: "50s", File: &exerciseModel.FileObservation{Path: "/b"}}},
	}}
	answer := validTask()
	answer.Key = "theory"
	variant := exerciseModel.Variant{Tasks: []exerciseModel.Task{answer, ospf, quick, manual}, Topology: topology}
	resolved := []exerciseModel.ResolvedConditions{
		{TaskKey: "ospf", Conditions: ospf.Check.State.Conditions},
		{TaskKey: "quick", Conditions: quick.Check.State.Conditions},
		{TaskKey: "manual", Conditions: manual.Check.State.Conditions},
	}
	return variant, topology, resolved
}

func TestBuildLabChecksGroupsPerTaskAndDevice(t *testing.T) {
	variant, topology, resolved := checksFixture()
	checks, err := variant.BuildLabChecks(topology, resolved)
	require.NoError(t, err)
	require.Len(t, checks, 4)

	// ospf: r1 (two conditions, the longest timeout 20s), then r2 (45s). One check per (task, device).
	first := checks[0]
	require.Equal(t, "ospf:1", first.ID)
	require.Equal(t, "ospf", first.TaskKey)
	require.Equal(t, "r1", first.Device)
	require.Equal(t, exerciseModel.StateModeContinuous, first.Mode)
	require.True(t, first.Manual)
	require.Len(t, first.Parts, 2)
	require.NotNil(t, first.Parts[0].File)
	require.NotNil(t, first.Parts[1].Custom)
	require.EqualValues(t, 20, first.TimeLimitSeconds)
	require.EqualValues(t, 30, first.ScheduleSeconds)

	second := checks[1]
	require.Equal(t, "ospf:2", second.ID)
	require.Equal(t, "r2", second.Device)
	require.EqualValues(t, 45, second.TimeLimitSeconds)
	require.EqualValues(t, 45, second.ScheduleSeconds, "a schedule is never shorter than the time limit")

	// Without a timeout the limit is the default, capped by a shorter interval.
	require.EqualValues(t, 5, checks[2].TimeLimitSeconds)
	require.EqualValues(t, 5, checks[2].ScheduleSeconds)
	require.Equal(t, exerciseModel.StateModeOnce, checks[2].Mode, "once is the default")

	// No interval: no schedule, a manual run only if the task says so.
	require.Equal(t, "manual:1", checks[3].ID)
	require.EqualValues(t, 50, checks[3].TimeLimitSeconds)
	require.EqualValues(t, 0, checks[3].ScheduleSeconds)
	require.False(t, checks[3].Manual)

	// Every id maps back to its task.
	for _, c := range checks {
		key, ok := exerciseModel.TaskKeyOfCheck(c.ID)
		require.True(t, ok)
		require.Equal(t, c.TaskKey, key)
	}

	// Build twice: the ids are stable.
	again, err := variant.BuildLabChecks(topology, resolved)
	require.NoError(t, err)
	require.Equal(t, checks, again)
}

func TestBuildLabChecksNamesSwitchesLikeTheLab(t *testing.T) {
	variant, topology, resolved := checksFixture()
	_, err := variant.BuildLabChecks(exerciseModel.Topology{Devices: topology.Devices[:1]}, resolved)
	require.Error(t, err, "a condition on a device the topology lacks is refused")
	_, err = variant.BuildLabChecks(topology, []exerciseModel.ResolvedConditions{{TaskKey: "theory"}})
	require.Error(t, err, "a task without a state has no checks")
}

func TestNeedsOfMeasuresTheLab(t *testing.T) {
	variant, topology, resolved := checksFixture()
	checks, err := variant.BuildLabChecks(topology, resolved)
	require.NoError(t, err)
	topology.Devices[0].Access = []exerciseModel.Access{{Kind: exerciseModel.AccessTerminal}}
	topology.Devices[1].Bootstrap = exerciseModel.Bootstrap{
		Timeout: "90s",
		Script:  "echo hi",
		Files:   []exerciseModel.BootstrapFile{{Path: "/a", Content: "12345"}, {Path: "/b", Content: "123"}},
	}
	needs := exerciseModel.NeedsOf(topology, checks)
	require.True(t, needs.Checks)
	require.True(t, needs.Terminal)
	require.Equal(t, 4, needs.ChecksPerLab)
	require.Equal(t, 2, needs.MaxParts)
	require.Equal(t, 50, needs.MaxCheckTimeLimit)
	require.Equal(t, 2, needs.MaxFiles)
	require.Equal(t, 5, needs.MaxFileBytes)
	require.Equal(t, len("echo hi")+5+3, needs.MaxBootstrapBytes, "script and files together")
	require.Equal(t, 90, needs.MaxBootstrapLimit)

	require.Equal(t, exerciseModel.LabNeeds{}, exerciseModel.NeedsOf(exerciseModel.Topology{}, nil))
}

func TestPinnedVariablesKeepStoredValuesAndDrawTheRest(t *testing.T) {
	variant, _ := variantWithBootstraps()
	// Nothing stored: both variables are resolved, only the template is drawn.
	values, fresh, err := variant.PinnedVariables(nil, nil)
	require.NoError(t, err)
	require.Equal(t, "ctf", values["mode"])
	require.Regexp(t, `^[a-z]{8}$`, values["pw"])
	require.Equal(t, []string{"pw"}, fresh)

	// Stored: the value is kept as it is, however many times the lab is built, and nothing is drawn.
	for range 3 {
		again, drawn, err := variant.PinnedVariables(map[string]string{"pw": "storedpw", "gone": "x"}, nil)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"pw": "storedpw", "mode": "ctf"}, again, "a value of a removed variable is dropped")
		require.Empty(t, drawn)
	}

	// A constant always follows the document, even if a stale value was stored for it.
	changed, _ := variantWithBootstraps()
	again, _, err := changed.PinnedVariables(map[string]string{"mode": "old", "pw": "abcdefgh"}, nil)
	require.NoError(t, err)
	require.Equal(t, "ctf", again["mode"])
}

func TestWithBootstrapFilesReadsArchiveFiles(t *testing.T) {
	file := uuid.Must(uuid.NewV7())
	topology := exerciseModel.Topology{Devices: []exerciseModel.Device{{
		Name: "web", Type: exerciseModel.DeviceTypeNode,
		Bootstrap: exerciseModel.Bootstrap{Files: []exerciseModel.BootstrapFile{
			{Path: "/a", Content: "inline"},
			{Path: "/b", Source: &exerciseModel.AttachmentRef{FileID: file, Name: "b"}},
		}},
	}}}
	out, err := topology.WithBootstrapFiles(func(ref exerciseModel.AttachmentRef) ([]byte, error) {
		require.Equal(t, file, ref.FileID)
		return []byte{0, 1, 2}, nil
	})
	require.NoError(t, err)
	require.Equal(t, "inline", out.Devices[0].Bootstrap.Files[0].Content)
	require.Equal(t, "\x00\x01\x02", out.Devices[0].Bootstrap.Files[1].Content)
	require.Nil(t, out.Devices[0].Bootstrap.Files[1].Source)
	require.NotNil(t, topology.Devices[0].Bootstrap.Files[1].Source, "the stored topology is not changed")

	_, err = topology.WithBootstrapFiles(func(exerciseModel.AttachmentRef) ([]byte, error) { return nil, errTest })
	require.ErrorIs(t, err, exerciseModel.ErrDeviceRuntimeInvalid.Err())
}

var errTest = &testError{}

type testError struct{}

func (*testError) Error() string { return "test" }
