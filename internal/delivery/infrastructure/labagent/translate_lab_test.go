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
	}}
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
	require.Nil(t, spec.Devices[0].Terminal)
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

// A condition the laboratory contract has no message for is an error that names it; a part is never left out silently.
// The kinds are mapped in checks_parts.go when the contract carries them.
func TestBuildLabChecks_RefusesWhatTheContractLacks(t *testing.T) {
	check := func(c exerciseModel.Condition) []exerciseModel.LabCheck {
		return []exerciseModel.LabCheck{{ID: "s:1", Device: "web", Parts: []exerciseModel.LabCheckPart{{Condition: c}}}}
	}
	for name, c := range map[string]exerciseModel.Condition{
		"directory":     {Directory: &exerciseModel.DirectoryObservation{Path: "/srv"}},
		"process":       {Process: &exerciseModel.ProcessObservation{Name: "nginx"}},
		"port":          {Port: &exerciseModel.PortObservation{Port: 80}},
		"kernel_param":  {KernelParam: &exerciseModel.KernelParamObservation{Name: "a.b", Equals: "1"}},
		"request":       {Request: &exerciseModel.RequestObservation{Protocol: "tcp", Port: 80}},
		"permission":    {Permission: &exerciseModel.PermissionObservation{User: "u", Path: "/p", Read: new(bool)}},
		"file checksum": {File: &exerciseModel.FileObservation{Path: "/x", Checksum: strings.Repeat("a", 64)}},
		"file type":     {File: &exerciseModel.FileObservation{Path: "/x", Type: "symlink"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := buildLabChecks(check(c))
			require.ErrorIs(t, err, exerciseModel.ErrRuntimeNotSupported.Err())
		})
	}
	out, err := buildLabChecks(check(exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/x", Contains: "a"}}))
	require.NoError(t, err)
	require.Len(t, out.GetItems()[0].GetParts(), 1)
}
