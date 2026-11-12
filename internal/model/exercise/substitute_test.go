package exerciseModel_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestSubstitutionRefs(t *testing.T) {
	refs, err := exerciseModel.SubstitutionRefs(
		`a {{task.one}} b {{var.db_password}} c {{task.one}}`,
	)
	require.NoError(t, err)
	require.Equal(
		t,
		[]exerciseModel.Ref{
			{Kind: exerciseModel.RefTask, Name: "one"},
			{Kind: exerciseModel.RefVar, Name: "db_password"},
			{Kind: exerciseModel.RefTask, Name: "one"},
		},
		refs,
	)

	// An escaped opener, a placeholder of a description and any other braces are plain text.
	refs, err = exerciseModel.SubstitutionRefs(`\{{task.one}} {{server}} {{ .Values }} {x}`)
	require.NoError(t, err)
	require.Empty(t, refs)

	// A reference that starts like ours and is not well-formed is an error, not silently plain text.
	for _, bad := range []string{`{{task.}}`, `{{task.Upper}}`, `{{var.a-b}}`, `{{task.one`, `{{task.1x}}`} {
		_, err = exerciseModel.SubstitutionRefs(bad)
		require.Errorf(t, err, "%q", bad)
	}
}

// variantWithBootstraps: a lab of two devices whose bootstrap places values, and a state task that expects them.
func variantWithBootstraps() (exerciseModel.Variant, uuid.UUID) {
	web, db := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	secret := validTask()
	secret.Key = "secret"
	state := validTask()
	state.Key, state.Name = "state", "Configure it"
	state.Check = exerciseModel.Check{
		State: &exerciseModel.StateCheck{Conditions: []exerciseModel.Condition{
			{
				DeviceID: web,
				File: &exerciseModel.FileObservation{
					Path:     "/etc/app/users",
					Contains: "admin:{{var.pw}}",
					Matches:  `key={{task.secret}};id=\d+`,
				},
			},
			{
				DeviceID: db,
				Custom: &exerciseModel.CustomObservation{
					Run:            "printenv FLAG",
					OutputContains: `{{task.secret}}`,
					OutputMatches:  `^{{var.pw}}$`,
				},
			},
		}},
	}
	return exerciseModel.Variant{
		Tasks: []exerciseModel.Task{secret, state},
		Topology: exerciseModel.Topology{
			Variables: []exerciseModel.Variable{
				{Name: "pw", Template: "[a-z]{8}"},
				{Name: "mode", Value: "ctf"},
			},
			Devices: []exerciseModel.Device{
				{
					ID:   web,
					Name: "web",
					Type: exerciseModel.DeviceTypeNode,
					Bootstrap: exerciseModel.Bootstrap{
						Env: []exerciseModel.EnvVar{
							{Name: "FLAG", Value: "{{task.secret}}"},
							{Name: "MODE", Value: "{{var.mode}}"},
							{Name: "PLAIN", Value: "x"},
						},
						Files: []exerciseModel.BootstrapFile{
							{Path: "/srv/flag.txt", Content: "flag={{task.secret}}\n"},
							{Path: "/srv/literal", Content: `\{{task.secret}} stays`},
						},
						Script: `echo "admin:{{var.pw}}" > /etc/app/users`,
					},
				},
				{ID: db, Name: "db", Type: exerciseModel.DeviceTypeNode},
				{
					ID:   uuid.Must(uuid.NewV7()),
					Name: "sw",
					Type: exerciseModel.DeviceTypeUnmanagedSwitch,
				},
			},
		},
	}, web
}

func TestResolveInstance(t *testing.T) {
	variant, web := variantWithBootstraps()
	resolved, err := variant.ResolveInstance(exerciseModel.PinnedValues{
		Tasks: map[string]string{
			"secret": "ICE{a.b}",
		},
		Vars: map[string]string{"pw": "hunter22", "mode": "ctf"},
	})
	require.NoError(t, err)

	// Only devices with a bootstrap are listed, in topology order; the stored variant is untouched.
	require.Len(t, resolved.Bootstraps, 1)
	got := resolved.Bootstraps[0]
	require.Equal(t, web, got.DeviceID)
	require.Equal(
		t,
		[]exerciseModel.EnvVar{
			{Name: "FLAG", Value: "ICE{a.b}"},
			{Name: "MODE", Value: "ctf"},
			{Name: "PLAIN", Value: "x"},
		},
		got.Bootstrap.Env,
	)
	require.Equal(t, "flag=ICE{a.b}\n", got.Bootstrap.Files[0].Content)
	require.Equal(
		t,
		"{{task.secret}} stays",
		got.Bootstrap.Files[1].Content,
		`\{{ gives a literal {{`,
	)
	require.Equal(t, `echo "admin:hunter22" > /etc/app/users`, got.Bootstrap.Script)
	require.Equal(t, "{{task.secret}}", variant.Topology.Devices[0].Bootstrap.Env[0].Value)

	// Conditions: plain expectations get the values as they are, patterns get them quoted as literals.
	require.Len(t, resolved.Conditions, 1)
	require.Equal(t, "state", resolved.Conditions[0].TaskKey)
	file, custom := resolved.Conditions[0].Conditions[0].File, resolved.Conditions[0].Conditions[1].Custom
	require.Equal(t, "admin:hunter22", file.Contains)
	require.Equal(t, `key=ICE\{a\.b\};id=\d+`, file.Matches)
	require.Equal(t, "ICE{a.b}", custom.OutputContains)
	require.Equal(t, "^hunter22$", custom.OutputMatches)
	require.Equal(
		t,
		"{{var.pw}}",
		variant.Tasks[1].Check.State.Conditions[0].File.Contains[len("admin:"):],
		"the stored conditions keep the references",
	)
}

func TestResolveInstanceNeedsEveryPinnedValue(t *testing.T) {
	variant, _ := variantWithBootstraps()
	_, err := variant.ResolveInstance(
		exerciseModel.PinnedValues{
			Tasks: map[string]string{},
			Vars:  map[string]string{"pw": "x", "mode": "ctf"},
		},
	)
	require.ErrorIs(t, err, exerciseModel.ErrSubstitutionUnresolved.Err())
	_, err = variant.ResolveInstance(
		exerciseModel.PinnedValues{
			Tasks: map[string]string{"secret": "ICE{x}"},
			Vars:  map[string]string{"mode": "ctf"},
		},
	)
	require.ErrorIs(t, err, exerciseModel.ErrSubstitutionUnresolved.Err())
}

func TestValidateSubstitutionsAndDelivery(t *testing.T) {
	variant, _ := variantWithBootstraps()
	require.NoError(t, variant.ValidateSubstitutions())
	require.Equal(
		t,
		map[string]struct{}{"secret": {}},
		variant.DeliveredTaskKeys(),
		"a bootstrap references the task; a condition does not deliver",
	)

	for name, mutate := range map[string]func(*exerciseModel.Variant){
		"unknown task in an env":          func(v *exerciseModel.Variant) { v.Topology.Devices[0].Bootstrap.Env[0].Value = "{{task.ghost}}" },
		"a state task has no value":       func(v *exerciseModel.Variant) { v.Topology.Devices[0].Bootstrap.Env[0].Value = "{{task.state}}" },
		"unknown variable in a script":    func(v *exerciseModel.Variant) { v.Topology.Devices[0].Bootstrap.Script = "{{var.ghost}}" },
		"unknown variable in a condition": func(v *exerciseModel.Variant) { v.Tasks[1].Check.State.Conditions[0].File.Contains = "{{var.ghost}}" },
		"malformed reference":             func(v *exerciseModel.Variant) { v.Topology.Devices[0].Bootstrap.Files[0].Content = "{{task.}}" },
	} {
		t.Run(name, func(t *testing.T) {
			v, _ := variantWithBootstraps()
			mutate(&v)
			err := v.ValidateSubstitutions()
			require.ErrorIs(t, err, exerciseModel.ErrSubstitutionUnresolved.Err())
		})
	}
	// A secret env value is sealed in a stored variant: it is not read.
	variant.Topology.Devices[0].Bootstrap.Env[0] = exerciseModel.EnvVar{
		Name:   "FLAG",
		Value:  "ciphertext{{task.ghost}}",
		Secret: true,
	}
	require.NoError(t, variant.ValidateSubstitutions())
}

func TestWithoutEnvOfTasksAndTopologyWithBootstraps(t *testing.T) {
	variant, web := variantWithBootstraps()
	trimmed := variant.WithoutEnvOfTasks(map[string]struct{}{"secret": {}})
	require.Equal(
		t,
		[]exerciseModel.EnvVar{{Name: "MODE", Value: "{{var.mode}}"}, {Name: "PLAIN", Value: "x"}},
		trimmed.Topology.Devices[0].Bootstrap.Env,
	)
	require.Len(t, variant.Topology.Devices[0].Bootstrap.Env, 3, "the original keeps them")

	topology, err := variant.Topology.WithBootstraps(
		[]exerciseModel.ResolvedBootstrap{
			{
				DeviceID: web,
				Bootstrap: exerciseModel.Bootstrap{
					Env: []exerciseModel.EnvVar{{Name: "A", Value: "1"}},
				},
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, "A", topology.Devices[0].Bootstrap.Env[0].Name)
	require.Equal(
		t,
		"FLAG",
		variant.Topology.Devices[0].Bootstrap.Env[0].Name,
		"the stored topology is not changed",
	)
	_, err = variant.Topology.WithBootstraps(
		[]exerciseModel.ResolvedBootstrap{{DeviceID: uuid.Must(uuid.NewV7())}},
	)
	require.ErrorIs(t, err, exerciseModel.ErrRuntimeDeviceUnresolved.Err())
}

func TestPinVariables(t *testing.T) {
	variant, _ := variantWithBootstraps()
	pinned, err := variant.PinVariables(nil)
	require.NoError(t, err)
	require.Equal(t, "ctf", pinned["mode"])
	require.Regexp(t, `^[a-z]{8}$`, pinned["pw"])
}

func TestRequireLabSupportedRefusesWhatNeedsAGuestAgent(t *testing.T) {
	// A container applies everything: files, script, command, args, terminal and a state task.
	variant, _ := variantWithBootstraps()
	require.NoError(t, variant.RequireLabSupported())
	require.Empty(t, variant.UnsupportedByLab())

	// The vm profile has no guest agent yet: its bootstrap list, command, args and terminal are not applied.
	plain := exerciseModel.Variant{
		Tasks: []exerciseModel.Task{validTask()},
		Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{
			{
				Name:    "web",
				Type:    exerciseModel.DeviceTypeNode,
				Profile: exerciseModel.NodeProfileVM,
				Bootstrap: exerciseModel.Bootstrap{
					Env: []exerciseModel.EnvVar{{Name: "A", Value: "b"}},
				},
			},
		}},
	}
	require.NoError(t, plain.RequireLabSupported(), "env alone is applied")
	for key, mutate := range map[string]func(*exerciseModel.Device){
		"command":         func(d *exerciseModel.Device) { d.Command = []string{"nginx"} },
		"args":            func(d *exerciseModel.Device) { d.Args = []string{"-q"} },
		"access.terminal": func(d *exerciseModel.Device) { d.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessTerminal}} },
		"bootstrap.files": func(d *exerciseModel.Device) {
			d.Bootstrap.Files = []exerciseModel.BootstrapFile{{Path: "/a", Content: "x"}}
		},
		"bootstrap.script": func(d *exerciseModel.Device) { d.Bootstrap.Script = "true" },
	} {
		v := plain
		v.Topology.Devices = []exerciseModel.Device{plain.Topology.Devices[0]}
		mutate(&v.Topology.Devices[0])
		require.Equal(t, []string{"device web: " + key}, v.UnsupportedByLab())
		require.ErrorIs(t, v.RequireLabSupported(), exerciseModel.ErrRuntimeNotSupported.Err())
	}
}

// vnc and rdp are accepted and stored, but no lab serves them on any profile: a lab that needs them is refused, by key.
func TestRequireLabSupportedRefusesTheReservedAccessKinds(t *testing.T) {
	for _, kind := range []exerciseModel.AccessKind{exerciseModel.AccessVNC, exerciseModel.AccessRDP} {
		device := exerciseModel.Device{
			Name: "web", Type: exerciseModel.DeviceTypeNode, Profile: exerciseModel.NodeProfileContainer,
			Access: []exerciseModel.Access{{Kind: exerciseModel.AccessHTTP, Port: 80}, {Kind: kind, Port: 5900}},
		}
		v := exerciseModel.Variant{Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{device}}}
		require.Equal(t, []string{"device web: access." + string(kind)}, v.UnsupportedByLab())
		require.ErrorIs(t, v.RequireLabSupported(), exerciseModel.ErrRuntimeNotSupported.Err())
	}
}
