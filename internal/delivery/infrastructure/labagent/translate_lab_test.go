package labagent

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// stableJSON turns a message into indented JSON with a fixed layout (protojson's own spacing is deliberately unstable).
func stableJSON(t *testing.T, m proto.Message) any {
	t.Helper()
	raw, err := protojson.Marshal(m)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func golden(t *testing.T, name string, got any) {
	t.Helper()
	encoded, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	encoded = append(encoded, '\n')
	path := filepath.Join("testdata", name)
	if *updateGolden {
		require.NoError(t, os.WriteFile(path, encoded, 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run the test with -update to write the golden file")
	require.Equal(t, string(want), string(encoded))
}

// contractFixture is a lab of a router with a bootstrap list, a terminal and the image's service, a web server with the
// author's command, and one state task with a file and a custom condition on each of the two devices.
func contractFixture() (exerciseModel.Topology, []exerciseModel.LabCheck) {
	r1, srv := uuid.FromStringOrNil("00000000-0000-7000-8000-000000000001"), uuid.FromStringOrNil("00000000-0000-7000-8000-000000000002")
	absent := false
	topology := exerciseModel.Topology{Devices: []exerciseModel.Device{
		{
			ID: r1, Name: "r1", Type: exerciseModel.DeviceTypeNode, Image: "registry.example/frr:10",
			ImageService: &exerciseModel.ImageService{
				Entrypoint: []string{"/sbin/tini", "--"}, Cmd: []string{"/usr/lib/frr/docker-start"}, User: "frr", WorkingDir: "/var/run/frr",
			},
			Access: []exerciseModel.Access{{Kind: exerciseModel.AccessTerminal, Command: "/bin/bash", User: "root"}},
			Bootstrap: exerciseModel.Bootstrap{
				Timeout:     "90s",
				Interpreter: "/bin/bash",
				Script:      "echo admin:s3cret > /etc/app/users",
				Env:         []exerciseModel.EnvVar{{Name: "MODE", Value: "ctf"}},
				Files: []exerciseModel.BootstrapFile{
					{Path: "/etc/frr/frr.conf", Content: "router ospf\n", Owner: "frr", Group: "frr", Mode: "0640"},
					{Path: "/root/flag.txt", Content: "ICE{abc}", Mode: "0400"},
				},
			},
		},
		{
			ID: srv, Name: "srv", Type: exerciseModel.DeviceTypeNode, Image: "registry.example/web:1",
			Command: []string{"/app/serve"}, Args: []string{"--port", "80"},
			ImageService: &exerciseModel.ImageService{Entrypoint: []string{"nginx"}, User: "www-data"},
		},
		{
			ID: uuid.FromStringOrNil("00000000-0000-7000-8000-000000000003"), Name: "attack", Type: exerciseModel.DeviceTypeVPNGateway,
			DHCP: true, DHCPRanges: []exerciseModel.DHCPRange{{Start: 100, End: 200}},
		},
	}}
	topology.Connections = []exerciseModel.Connection{{Endpoints: []exerciseModel.Endpoint{
		{Kind: exerciseModel.EndpointDevice, DeviceID: topology.Devices[2].ID, Interface: "eth0"},
		{Kind: exerciseModel.EndpointDevice, DeviceID: r1, Interface: "eth0"},
	}}}
	checks := []exerciseModel.LabCheck{
		{
			ID: "ospf:1", TaskKey: "ospf", Device: "r1", Mode: exerciseModel.StateModeContinuous,
			ScheduleSeconds: 30, TimeLimitSeconds: 20, Manual: true,
			Parts: []exerciseModel.LabCheckPart{
				{Condition: exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/etc/frr/frr.conf", Contains: "router ospf", Matches: `area 0`, Mode: "0640", Owner: "frr"}}},
				{Condition: exerciseModel.Condition{Custom: &exerciseModel.CustomObservation{Run: "vtysh -c 'show ip ospf neighbor' | grep -q Full", Interpreter: "/bin/sh", User: "root", OutputMatches: "^Full$"}}},
			},
		},
		{
			ID: "ospf:2", TaskKey: "ospf", Device: "srv", Mode: exerciseModel.StateModeOnce, TimeLimitSeconds: 30,
			Parts: []exerciseModel.LabCheckPart{
				{Condition: exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/tmp/gone", Exists: &absent}}},
				{Condition: exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/tmp/there"}}},
			},
		},
	}
	return topology, checks
}

func TestBuildLab_ContractGolden(t *testing.T) {
	topology, checks := contractFixture()
	build, err := BuildLab(topology, checks)
	require.NoError(t, err)

	var spec any
	require.NoError(t, json.Unmarshal(build.SpecJSON, &spec))
	golden(t, "lab_spec.golden.json", spec)

	env := &labpb.LabItem{Env: build.Env, Bootstrap: build.Bootstrap, Checks: build.Checks}
	golden(t, "lab_item.golden.json", stableJSON(t, env))
}

func TestBuildLab_NoBootstrapNoChecksIsTheOldLab(t *testing.T) {
	topology := exerciseModel.Topology{Devices: []exerciseModel.Device{{
		ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeNode, Image: "nginx",
		ImageService: &exerciseModel.ImageService{Entrypoint: []string{"nginx"}, User: "www"},
		Bootstrap:    exerciseModel.Bootstrap{Env: []exerciseModel.EnvVar{{Name: "A", Value: "1"}}},
	}}}
	build, err := BuildLab(topology, nil)
	require.NoError(t, err)
	require.Empty(t, build.Bootstrap, "env alone travels on the environment path")
	require.Nil(t, build.Checks)
	require.Len(t, build.Env, 1)
	var spec labSpec
	require.NoError(t, json.Unmarshal(build.SpecJSON, &spec))
	require.Nil(t, spec.Devices[0].Service, "the image decides: nothing is told")
	require.Empty(t, spec.Devices[0].Access)
}

func TestBuildLab_ServiceUnknownRefusesAndNamesTheDevice(t *testing.T) {
	topology := exerciseModel.Topology{Devices: []exerciseModel.Device{{
		ID: uuid.Must(uuid.NewV7()), Name: "legacy", Type: exerciseModel.DeviceTypeNode, Image: "registry.example/x:1",
		Bootstrap: exerciseModel.Bootstrap{Script: "true"},
	}}}
	_, err := BuildLab(topology, nil)
	require.ErrorIs(t, err, exerciseModel.ErrServiceCommandUnknown.Err())
	require.Contains(t, err.Error(), "legacy")
}

func TestBuildLab_DoesNotPutSecretsInTheSpec(t *testing.T) {
	topology, checks := contractFixture()
	build, err := BuildLab(topology, checks)
	require.NoError(t, err)
	for _, secret := range []string{"s3cret", "ICE{abc}", "router ospf", "Full"} {
		require.NotContains(t, string(build.SpecJSON), secret)
	}
}

// Every observation maps to its laboratory message with the keys the contract expects; the expectations of a kind are only
// carried to the lab as the contract lists them. A condition with no observation is an error, never a part left out.
func TestBuildLabChecks_MapsEveryObservation(t *testing.T) {
	yes, no := true, false
	check := func(c exerciseModel.Condition) []exerciseModel.LabCheck {
		return []exerciseModel.LabCheck{{ID: "s:1", Device: "web", Parts: []exerciseModel.LabCheckPart{{Condition: c}}}}
	}
	part := func(c exerciseModel.Condition) *labpb.CheckPart {
		out, err := buildLabChecks(check(c))
		require.NoError(t, err)
		return out.GetItems()[0].GetParts()[0]
	}

	file := part(exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/x", Type: "symlink", Checksum: strings.Repeat("A", 64)}}).GetFile()
	require.Equal(t, "symlink", file.GetType())
	require.Equal(t, strings.Repeat("a", 64), file.GetChecksum(), "the lab wants lower-case hex")

	dir := part(exerciseModel.Condition{Directory: &exerciseModel.DirectoryObservation{Path: "/srv", Mode: "0750", Owner: "www", HasEntries: []string{"a"}, LacksEntries: []string{"b"}}}).GetDirectory()
	require.Equal(t, "/srv", dir.GetPath())
	require.True(t, dir.GetExists(), "exists is true unless the author said false")
	require.EqualValues(t, 0o750, dir.GetMode())
	require.Equal(t, []string{"a"}, dir.GetHasEntries())
	require.Equal(t, []string{"b"}, dir.GetLacksEntries())
	gone := part(exerciseModel.Condition{Directory: &exerciseModel.DirectoryObservation{Path: "/srv", Exists: &no}}).GetDirectory()
	require.False(t, gone.GetExists())

	proc := part(exerciseModel.Condition{Process: &exerciseModel.ProcessObservation{Name: "nginx", User: "www", Running: &no}}).GetProcess()
	require.Equal(t, "nginx", proc.GetName())
	require.Nil(t, proc.Matches, "a key not written is not sent")
	require.False(t, proc.GetRunning())

	port := part(exerciseModel.Condition{Port: &exerciseModel.PortObservation{Port: 8080, Protocol: "udp", Listening: &yes, Address: "127.0.0.1", Process: "nginx"}}).GetPort()
	require.EqualValues(t, 8080, port.GetPort())
	require.Equal(t, "udp", port.GetProtocol())
	require.Equal(t, "127.0.0.1", port.GetAddress())

	kernel := part(exerciseModel.Condition{KernelParam: &exerciseModel.KernelParamObservation{Name: "net.ipv4.ip_forward", Equals: "1"}}).GetKernelParam()
	require.Equal(t, "net.ipv4.ip_forward", kernel.GetName())
	require.Equal(t, "1", kernel.GetEquals())
	require.Nil(t, kernel.Matches)

	request := part(exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "http", Port: 80, Method: "HEAD", Path: "/h", Status: 204, BodyContains: "ok"}}).GetRequest()
	require.Equal(t, "HEAD", request.GetMethod())
	require.EqualValues(t, 204, request.GetStatus())
	require.Equal(t, "ok", request.GetBodyContains())
	plain := part(exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "tcp", Port: 5432}}).GetRequest()
	require.Nil(t, plain.Status, "no status written: the lab's default of 200 applies")

	permission := part(exerciseModel.Condition{Permission: &exerciseModel.PermissionObservation{User: "www", Path: "/etc/shadow", Read: &no, Write: &no}}).GetPermission()
	require.Equal(t, "www", permission.GetUser())
	require.False(t, permission.GetRead())
	require.Nil(t, permission.Execute)

	custom := part(exerciseModel.Condition{Custom: &exerciseModel.CustomObservation{Run: "true"}}).GetCustom()
	require.Equal(t, "true", custom.GetScript())
	require.Empty(t, custom.GetUser(), "an empty user is nobody in the lab; the daemon writes no default")

	_, err := buildLabChecks(check(exerciseModel.Condition{}))
	require.ErrorIs(t, err, exerciseModel.ErrRuntimeNotSupported.Err())
}

// The service is always told a user: the image's, else the root account; an image whose configuration is not known cannot be
// started through the launcher (no empty user is ever sent: it would run the service as root).
func TestBuildLab_ServiceUserIsAlwaysSent(t *testing.T) {
	device := func(image *exerciseModel.ImageService) exerciseModel.Topology {
		return exerciseModel.Topology{Devices: []exerciseModel.Device{{
			ID: uuid.FromStringOrNil("00000000-0000-7000-8000-000000000001"), Name: "web", Type: exerciseModel.DeviceTypeNode,
			Image: "platform:web@sha256:" + strings.Repeat("a", 64), Command: []string{"/app"}, ImageService: image,
		}}}
	}
	user := func(topology exerciseModel.Topology) (string, error) {
		build, err := BuildLab(topology, nil)
		if err != nil {
			return "", err
		}
		return decode(t, build.SpecJSON).Devices[0].Service.User, nil
	}
	for name, tc := range map[string]struct{ image, want string }{
		"declared name":    {"www-data", "www-data"},
		"declared number":  {"1000:1000", "1000:1000"},
		"declared nothing": {"", "0"},
	} {
		got, err := user(device(&exerciseModel.ImageService{User: tc.image}))
		require.NoError(t, err, name)
		require.Equal(t, tc.want, got, name)
	}
	_, err := user(device(nil))
	require.ErrorIs(t, err, exerciseModel.ErrServiceUserUnknown.Err())
}
