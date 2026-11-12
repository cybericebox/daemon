package exerciseModel_test

import (
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func keyed(key string, requires ...string) exerciseModel.Task {
	task := validTask()
	task.Key, task.Requires, task.Name = key, requires, "Task "+key
	return task
}

func TestNormalizeTaskKeys(t *testing.T) {
	variant := exerciseModel.Variant{
		Tasks: []exerciseModel.Task{keyed("task_2"), keyed(""), keyed("mine"), keyed("")},
	}
	variant.NormalizeTaskKeys()
	keys := make([]string, len(variant.Tasks))
	for i, task := range variant.Tasks {
		keys[i] = task.Key
	}
	// task_2 is taken by the author, so the second task takes the next free number; the keys that were set stay.
	require.Equal(t, []string{"task_2", "task_3", "mine", "task_4"}, keys)
	before := append([]exerciseModel.Task(nil), variant.Tasks...)
	variant.NormalizeTaskKeys()
	require.Equal(t, before, variant.Tasks, "idempotent")
}

func TestValidateDocument_KeysAndRequires(t *testing.T) {
	for name, tc := range map[string]struct {
		tasks []exerciseModel.Task
		want  error
	}{
		"ok":                {[]exerciseModel.Task{keyed("a"), keyed("b", "a"), keyed("c", "a", "b")}, nil},
		"bad slug":          {[]exerciseModel.Task{keyed("Bad-Key")}, exerciseModel.ErrTaskKeyInvalid.Err()},
		"starts with digit": {[]exerciseModel.Task{keyed("1a")}, exerciseModel.ErrTaskKeyInvalid.Err()},
		"too long":          {[]exerciseModel.Task{keyed(strings.Repeat("a", 33))}, exerciseModel.ErrTaskKeyInvalid.Err()},
		"duplicate":         {[]exerciseModel.Task{keyed("a"), keyed("a")}, exerciseModel.ErrTaskKeyInvalid.Err()},
		"unknown":           {[]exerciseModel.Task{keyed("a", "ghost")}, exerciseModel.ErrTaskRequiresInvalid.Err()},
		"itself":            {[]exerciseModel.Task{keyed("a", "a")}, exerciseModel.ErrTaskRequiresInvalid.Err()},
		"repeated":          {[]exerciseModel.Task{keyed("a"), keyed("b", "a", "a")}, exerciseModel.ErrTaskRequiresInvalid.Err()},
		"cycle":             {[]exerciseModel.Task{keyed("a", "c"), keyed("b", "a"), keyed("c", "b")}, exerciseModel.ErrTaskRequiresInvalid.Err()},
	} {
		t.Run(name, func(t *testing.T) {
			err := exerciseModel.Variant{Tasks: tc.tasks}.ValidateDocument()
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func answerTask(answer exerciseModel.AnswerCheck) exerciseModel.Task {
	task := keyed("a")
	task.Check = exerciseModel.Check{Answer: &answer}
	return task
}

func TestValidateDocument_Answer(t *testing.T) {
	yes, no := true, false
	longText := strings.Repeat("я", exerciseModel.MaxTextAnswerRunes+1)
	many := make([]string, exerciseModel.MaxAnswerValues+1)
	for i := range many {
		many[i] = strings.Repeat("v", i+1)
	}
	for name, tc := range map[string]struct {
		answer exerciseModel.AnswerCheck
		want   error // nil: valid
	}{
		"flag fixed":                {exerciseModel.AnswerCheck{Type: "flag", Values: []string{"ICE{x}"}}, nil},
		"flag generated":            {exerciseModel.AnswerCheck{Type: "flag"}, nil},
		"flag template":             {exerciseModel.AnswerCheck{Type: "flag", Values: []string{`template:ICE{\d}`}}, nil},
		"flag hint":                 {exerciseModel.AnswerCheck{Type: "flag", InputHint: "ICE{...}"}, nil},
		"flag without edges":        {exerciseModel.AnswerCheck{Type: "flag", Values: []string{"plain"}}, exerciseModel.ErrFlagSourceInvalid.Err()},
		"flag ignore_case":          {exerciseModel.AnswerCheck{Type: "flag", IgnoreCase: true}, exerciseModel.ErrAnswerInvalid.Err()},
		"flag trim":                 {exerciseModel.AnswerCheck{Type: "flag", Trim: &no}, exerciseModel.ErrAnswerInvalid.Err()},
		"unknown type":              {exerciseModel.AnswerCheck{Type: "number", Values: []string{"1"}}, exerciseModel.ErrAnswerInvalid.Err()},
		"no type":                   {exerciseModel.AnswerCheck{Values: []string{"ICE{x}"}}, exerciseModel.ErrAnswerInvalid.Err()},
		"text fixed":                {exerciseModel.AnswerCheck{Type: "text", Values: []string{"OSPF"}, IgnoreCase: true, Trim: &yes}, nil},
		"text several":              {exerciseModel.AnswerCheck{Type: "text", Values: []string{"a b", "c"}}, nil},
		"text template":             {exerciseModel.AnswerCheck{Type: "text", Values: []string{"template:id-[0-9]{6}"}}, nil},
		"text without values":       {exerciseModel.AnswerCheck{Type: "text"}, exerciseModel.ErrAnswerInvalid.Err()},
		"text blank":                {exerciseModel.AnswerCheck{Type: "text", Values: []string{"  "}}, exerciseModel.ErrAnswerInvalid.Err()},
		"text control":              {exerciseModel.AnswerCheck{Type: "text", Values: []string{"a\nb"}}, exerciseModel.ErrAnswerInvalid.Err()},
		"text too long":             {exerciseModel.AnswerCheck{Type: "text", Values: []string{longText}}, exerciseModel.ErrAnswerInvalid.Err()},
		"text too many":             {exerciseModel.AnswerCheck{Type: "text", Values: many}, exerciseModel.ErrAnswerInvalid.Err()},
		"text duplicate":            {exerciseModel.AnswerCheck{Type: "text", Values: []string{"a", "a"}}, exerciseModel.ErrAnswerInvalid.Err()},
		"text bad template":         {exerciseModel.AnswerCheck{Type: "text", Values: []string{"template:fixed"}}, exerciseModel.ErrAnswerInvalid.Err()},
		"hint too long":             {exerciseModel.AnswerCheck{Type: "text", Values: []string{"a"}, InputHint: strings.Repeat("h", exerciseModel.MaxInputHintRunes+1)}, exerciseModel.ErrAnswerInvalid.Err()},
		"hint with a control":       {exerciseModel.AnswerCheck{Type: "text", Values: []string{"a"}, InputHint: "a\tb"}, exerciseModel.ErrAnswerInvalid.Err()},
		"text value with ICE edges": {exerciseModel.AnswerCheck{Type: "text", Values: []string{"ICE{not-a-flag}"}}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			err := exerciseModel.Variant{
				Tasks: []exerciseModel.Task{answerTask(tc.answer)},
			}.ValidateDocument()
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestCheckKindRules(t *testing.T) {
	both := keyed("a")
	both.Check = exerciseModel.Check{
		Answer: &exerciseModel.AnswerCheck{Type: "flag"},
		State:  &exerciseModel.StateCheck{},
	}
	require.ErrorIs(t, exerciseModel.Variant{Tasks: []exerciseModel.Task{both}}.ValidateDocument(), exerciseModel.ErrTaskCheckInvalid.Err())

	// A draft may leave a task without a check; publication asks for one.
	none := keyed("a")
	none.Check = exerciseModel.Check{}
	require.NoError(t, exerciseModel.Variant{Tasks: []exerciseModel.Task{none}}.ValidateDocument())
	version := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{none}}},
	}
	require.ErrorIs(t, version.ValidateForPublish(), exerciseModel.ErrTaskCheckInvalid.Err())

	// Every variant has the same kind of check, and the same answer type, at each position.
	flag := keyed("a")
	flag.Check = exerciseModel.FlagCheck("ICE{x}")
	stateVar := stateVariant(
		exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/x"}},
		exerciseModel.StateCheck{},
	)
	stateVar.Tasks[0].ID, stateVar.Tasks[0].Key = flag.ID, "a"
	text := keyed("a")
	text.ID, text.Check = flag.ID, exerciseModel.TextCheck("x")
	for name, other := range map[string]exerciseModel.Variant{"answer vs state": stateVar, "text vs flag": {Tasks: []exerciseModel.Task{text}}} {
		t.Run(name, func(t *testing.T) {
			v := exerciseModel.ExerciseVersion{
				Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{flag}}, other},
			}
			err := v.ValidateStructure()
			require.ErrorIs(t, err, exerciseModel.ErrTaskCheckInvalid.Err())
		})
	}
}

func stateVariant(
	condition exerciseModel.Condition,
	state exerciseModel.StateCheck,
) exerciseModel.Variant {
	node := uuid.Must(uuid.NewV7())
	condition.DeviceID = node
	state.Conditions = []exerciseModel.Condition{condition}
	task := keyed("s")
	task.Check = exerciseModel.Check{State: &state}
	return exerciseModel.Variant{
		Tasks: []exerciseModel.Task{task},
		Topology: exerciseModel.Topology{
			Variables: []exerciseModel.Variable{{Name: "pw", Value: "x"}},
			Devices: []exerciseModel.Device{
				{ID: node, Name: "web", Type: exerciseModel.DeviceTypeNode},
				{
					ID:   uuid.Must(uuid.NewV7()),
					Name: "sw",
					Type: exerciseModel.DeviceTypeUnmanagedSwitch,
				},
			},
		},
	}
}

func TestValidateDocument_State(t *testing.T) {
	yes, no := true, false
	file := func(f exerciseModel.FileObservation) exerciseModel.Condition {
		return exerciseModel.Condition{File: &f}
	}
	custom := func(o exerciseModel.CustomObservation) exerciseModel.Condition {
		return exerciseModel.Condition{Custom: &o}
	}
	for name, tc := range map[string]struct {
		condition exerciseModel.Condition
		state     exerciseModel.StateCheck
		valid     bool
	}{
		"file exists":              {file(exerciseModel.FileObservation{Path: "/etc/frr/frr.conf", Exists: &yes}), exerciseModel.StateCheck{}, true},
		"file full":                {file(exerciseModel.FileObservation{Path: "/etc/x", Contains: "a", Matches: `network 10\.0\.\d+\.0/24`, Mode: "0640", Owner: "frr", Group: "1000"}), exerciseModel.StateCheck{Mode: "continuous", Interval: "30s", Manual: true}, true},
		"file with substitution":   {file(exerciseModel.FileObservation{Path: "/x", Matches: `key={{var.pw}}`}), exerciseModel.StateCheck{}, true},
		"file absent alone":        {file(exerciseModel.FileObservation{Path: "/x", Exists: &no}), exerciseModel.StateCheck{}, true},
		"file absent with content": {file(exerciseModel.FileObservation{Path: "/x", Exists: &no, Contains: "a"}), exerciseModel.StateCheck{}, false},
		"relative path":            {file(exerciseModel.FileObservation{Path: "etc/x"}), exerciseModel.StateCheck{}, false},
		"dot dot":                  {file(exerciseModel.FileObservation{Path: "/etc/../x"}), exerciseModel.StateCheck{}, false},
		"root":                     {file(exerciseModel.FileObservation{Path: "/"}), exerciseModel.StateCheck{}, false},
		"bad regexp":               {file(exerciseModel.FileObservation{Path: "/x", Matches: `(`}), exerciseModel.StateCheck{}, false},
		"bad mode":                 {file(exerciseModel.FileObservation{Path: "/x", Mode: "0999"}), exerciseModel.StateCheck{}, false},
		"bad owner":                {file(exerciseModel.FileObservation{Path: "/x", Owner: "Root User"}), exerciseModel.StateCheck{}, false},
		"contains too long":        {file(exerciseModel.FileObservation{Path: "/x", Contains: strings.Repeat("a", exerciseModel.MaxExpectationBytes+1)}), exerciseModel.StateCheck{}, false},
		"custom":                   {custom(exerciseModel.CustomObservation{Run: "true", Interpreter: "/bin/sh", User: "root", OutputContains: "ok", OutputMatches: `^o.$`}), exerciseModel.StateCheck{}, true},
		"custom without run":       {custom(exerciseModel.CustomObservation{Run: " "}), exerciseModel.StateCheck{}, false},
		"custom bad interpreter":   {custom(exerciseModel.CustomObservation{Run: "true", Interpreter: "sh"}), exerciseModel.StateCheck{}, false},
		"custom bad regexp":        {custom(exerciseModel.CustomObservation{Run: "true", OutputMatches: `[`}), exerciseModel.StateCheck{}, false},
		"custom run too long":      {custom(exerciseModel.CustomObservation{Run: strings.Repeat("a", exerciseModel.MaxCustomRunBytes+1)}), exerciseModel.StateCheck{}, false},
		"both observations":        {exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/x"}, Custom: &exerciseModel.CustomObservation{Run: "true"}}, exerciseModel.StateCheck{}, false},
		"no observation":           {exerciseModel.Condition{}, exerciseModel.StateCheck{}, false},
		"timeout ok":               {exerciseModel.Condition{Timeout: "10s", Custom: &exerciseModel.CustomObservation{Run: "true"}}, exerciseModel.StateCheck{}, true},
		"timeout too short":        {exerciseModel.Condition{Timeout: "100ms", Custom: &exerciseModel.CustomObservation{Run: "true"}}, exerciseModel.StateCheck{}, false},
		"timeout too long":         {exerciseModel.Condition{Timeout: "2m", Custom: &exerciseModel.CustomObservation{Run: "true"}}, exerciseModel.StateCheck{}, false},
		"interval zero":            {file(exerciseModel.FileObservation{Path: "/x"}), exerciseModel.StateCheck{Interval: "0"}, true},
		"interval too short":       {file(exerciseModel.FileObservation{Path: "/x"}), exerciseModel.StateCheck{Interval: "1s"}, false},
		"interval too long":        {file(exerciseModel.FileObservation{Path: "/x"}), exerciseModel.StateCheck{Interval: "25h"}, false},
		"interval not a duration":  {file(exerciseModel.FileObservation{Path: "/x"}), exerciseModel.StateCheck{Interval: "soon"}, false},
		"unknown mode":             {file(exerciseModel.FileObservation{Path: "/x"}), exerciseModel.StateCheck{Mode: "always"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := stateVariant(tc.condition, tc.state).ValidateDocument()
			if tc.valid {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, exerciseModel.ErrStateInvalid.Err())
		})
	}

	t.Run("conditions: at least one, at most the limit", func(t *testing.T) {
		v := stateVariant(
			file(exerciseModel.FileObservation{Path: "/x"}),
			exerciseModel.StateCheck{},
		)
		v.Tasks[0].Check.State.Conditions = nil
		require.ErrorIs(t, v.ValidateDocument(), exerciseModel.ErrStateInvalid.Err())
		v = stateVariant(
			file(exerciseModel.FileObservation{Path: "/x"}),
			exerciseModel.StateCheck{},
		)
		one := v.Tasks[0].Check.State.Conditions[0]
		for range exerciseModel.MaxStateConditions {
			v.Tasks[0].Check.State.Conditions = append(v.Tasks[0].Check.State.Conditions, one)
		}
		require.ErrorIs(t, v.ValidateDocument(), exerciseModel.ErrStateInvalid.Err(), "1 + 20 conditions")
	})

	t.Run("a condition looks at a node device of the variant", func(t *testing.T) {
		v := stateVariant(
			file(exerciseModel.FileObservation{Path: "/x"}),
			exerciseModel.StateCheck{},
		)
		v.Tasks[0].Check.State.Conditions[0].DeviceID = v.Topology.Devices[1].ID // the switch
		require.ErrorIs(t, v.ValidateDocument(), exerciseModel.ErrStateInvalid.Err())
		v.Tasks[0].Check.State.Conditions[0].DeviceID = uuid.Must(uuid.NewV7()) // none
		require.ErrorIs(t, v.ValidateDocument(), exerciseModel.ErrStateInvalid.Err())
	})
}

func TestValidateDeviceRuntime(t *testing.T) {
	node := func(mutate func(*exerciseModel.Device)) exerciseModel.Topology {
		d := exerciseModel.Device{
			ID:   uuid.Must(uuid.NewV7()),
			Name: "web",
			Type: exerciseModel.DeviceTypeNode,
		}
		mutate(&d)
		return exerciseModel.Topology{Devices: []exerciseModel.Device{d}}
	}
	bad := exerciseModel.ErrDeviceRuntimeInvalid.Err()
	for name, tc := range map[string]struct {
		mutate func(*exerciseModel.Device)
		valid  bool
	}{
		"command and args":    {func(d *exerciseModel.Device) { d.Command, d.Args = []string{"nginx"}, []string{"-g", "daemon off;"} }, true},
		"empty command entry": {func(d *exerciseModel.Device) { d.Command = []string{""} }, false},
		"too many args":       {func(d *exerciseModel.Device) { d.Args = make([]string, exerciseModel.MaxArgsEntries+1) }, false},
		"system windows on vm": {func(d *exerciseModel.Device) {
			d.Profile, d.System, d.Hardware = exerciseModel.NodeProfileVM, "windows", "compatible"
		}, true},
		"windows on container": {func(d *exerciseModel.Device) { d.System = "windows" }, false},
		"compatible on micro":  {func(d *exerciseModel.Device) { d.Hardware = "compatible" }, false},
		"unknown system":       {func(d *exerciseModel.Device) { d.System = "plan9" }, false},
		"terminal": {func(d *exerciseModel.Device) {
			d.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessTerminal, Command: "/bin/sh", User: "root"}}
		}, true},
		"access http default protocol": {func(d *exerciseModel.Device) {
			d.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessHTTP, Port: 80}}
		}, true},
		"access every kind": {func(d *exerciseModel.Device) {
			d.Access = []exerciseModel.Access{
				{Kind: exerciseModel.AccessHTTP, Port: 443, Protocol: "https"}, {Kind: exerciseModel.AccessTerminal},
				{Kind: exerciseModel.AccessVNC, Port: 5900}, {Kind: exerciseModel.AccessRDP, Port: 3389},
			}
		}, true},
		"access http without port": {func(d *exerciseModel.Device) { d.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessHTTP}} }, false},
		"access http port too high": {func(d *exerciseModel.Device) {
			d.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessHTTP, Port: 65536}}
		}, false},
		"access http bad protocol": {func(d *exerciseModel.Device) {
			d.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessHTTP, Port: 80, Protocol: "ftp"}}
		}, false},
		"access http with a command": {func(d *exerciseModel.Device) {
			d.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessHTTP, Port: 80, Command: "/bin/sh"}}
		}, false},
		"access terminal with a port": {func(d *exerciseModel.Device) {
			d.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessTerminal, Port: 22}}
		}, false},
		"access terminal bad user": {func(d *exerciseModel.Device) {
			d.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessTerminal, User: "Root!"}}
		}, false},
		"access vnc without port": {func(d *exerciseModel.Device) { d.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessVNC}} }, false},
		"access rdp with a protocol": {func(d *exerciseModel.Device) {
			d.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessRDP, Port: 1, Protocol: "http"}}
		}, false},
		"access a kind twice": {func(d *exerciseModel.Device) {
			d.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessHTTP, Port: 80}, {Kind: exerciseModel.AccessHTTP, Port: 81}}
		}, false},
		"unknown access kind": {func(d *exerciseModel.Device) {
			d.Access = []exerciseModel.Access{{Kind: "console", Command: "/bin/sh"}}
		}, false},
		"terminal relative shell": {func(d *exerciseModel.Device) {
			d.Access = []exerciseModel.Access{{Kind: exerciseModel.AccessTerminal, Command: "sh"}}
		}, false},
		"bootstrap full": {func(d *exerciseModel.Device) {
			d.Bootstrap = exerciseModel.Bootstrap{Timeout: "60s", Interpreter: "/bin/sh", Script: "true",
				Env:   []exerciseModel.EnvVar{{Name: "A_B1", Value: "x"}},
				Files: []exerciseModel.BootstrapFile{{Path: "/a", Content: "c", Owner: "root", Group: "root", Mode: "0400"}, {Path: "/b", Source: &exerciseModel.AttachmentRef{FileID: uuid.Must(uuid.NewV7())}}}}
		}, true},
		"timeout too short": {func(d *exerciseModel.Device) { d.Bootstrap.Timeout = "10ms" }, false},
		"timeout too long":  {func(d *exerciseModel.Device) { d.Bootstrap.Timeout = "11m" }, false},
		"env bad name":      {func(d *exerciseModel.Device) { d.Bootstrap.Env = []exerciseModel.EnvVar{{Name: "1A"}} }, false},
		"env duplicate":     {func(d *exerciseModel.Device) { d.Bootstrap.Env = []exerciseModel.EnvVar{{Name: "A"}, {Name: "A"}} }, false},
		"env value too long": {func(d *exerciseModel.Device) {
			d.Bootstrap.Env = []exerciseModel.EnvVar{{Name: "A", Value: strings.Repeat("x", exerciseModel.MaxEnvValueBytes+1)}}
		}, false},
		"file relative path": {func(d *exerciseModel.Device) {
			d.Bootstrap.Files = []exerciseModel.BootstrapFile{{Path: "a", Content: "c"}}
		}, false},
		"file both": {func(d *exerciseModel.Device) {
			d.Bootstrap.Files = []exerciseModel.BootstrapFile{{Path: "/a", Content: "c", Source: &exerciseModel.AttachmentRef{FileID: uuid.Must(uuid.NewV7())}}}
		}, false},
		"file neither": {func(d *exerciseModel.Device) { d.Bootstrap.Files = []exerciseModel.BootstrapFile{{Path: "/a"}} }, false},
		"file duplicate path": {func(d *exerciseModel.Device) {
			d.Bootstrap.Files = []exerciseModel.BootstrapFile{{Path: "/a", Content: "c"}, {Path: "/a", Content: "d"}}
		}, false},
		"file bad mode": {func(d *exerciseModel.Device) {
			d.Bootstrap.Files = []exerciseModel.BootstrapFile{{Path: "/a", Content: "c", Mode: "rw"}}
		}, false},
		"file content too large": {func(d *exerciseModel.Device) {
			d.Bootstrap.Files = []exerciseModel.BootstrapFile{{Path: "/a", Content: strings.Repeat("x", exerciseModel.MaxBootstrapContentBytes+1)}}
		}, false},
		"interpreter no script": {func(d *exerciseModel.Device) { d.Bootstrap.Interpreter = "/bin/sh" }, false},
		"script too large": {func(d *exerciseModel.Device) {
			d.Bootstrap.Script = strings.Repeat("x", exerciseModel.MaxBootstrapContentBytes+1)
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := node(tc.mutate).ValidateDeviceRuntime()
			if tc.valid {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, bad)
		})
	}

	t.Run("switches and hubs have none of these keys", func(t *testing.T) {
		for _, kind := range []exerciseModel.DeviceType{exerciseModel.DeviceTypeUnmanagedSwitch, exerciseModel.DeviceTypeHub} {
			topology := exerciseModel.Topology{
				Devices: []exerciseModel.Device{
					{
						ID:     uuid.Must(uuid.NewV7()),
						Name:   "sw",
						Type:   kind,
						Access: []exerciseModel.Access{{Kind: exerciseModel.AccessTerminal}},
					},
				},
			}
			require.ErrorIs(t, topology.ValidateDeviceRuntime(), exerciseModel.ErrDeviceTypeInvalid.Err())
		}
	})
}

func TestValidateVariables(t *testing.T) {
	check := func(vars ...exerciseModel.Variable) error {
		return exerciseModel.Variant{
			Topology: exerciseModel.Topology{Variables: vars},
		}.ValidateDocument()
	}
	require.NoError(
		t,
		check(
			exerciseModel.Variable{Name: "db_password", Template: "[a-z0-9]{16}"},
			exerciseModel.Variable{Name: "app_mode", Value: "ctf"},
		),
	)
	bad := exerciseModel.ErrVariableInvalid.Err()
	for name, vars := range map[string][]exerciseModel.Variable{
		"both":             {{Name: "a", Value: "x", Template: "[ab]"}},
		"neither":          {{Name: "a"}},
		"bad name":         {{Name: "A-b", Value: "x"}},
		"duplicate":        {{Name: "a", Value: "x"}, {Name: "a", Value: "y"}},
		"bad template":     {{Name: "a", Template: "fixed"}},
		"control in value": {{Name: "a", Value: "x\ny"}},
		"value too long":   {{Name: "a", Value: strings.Repeat("x", exerciseModel.MaxVariableValueBytes+1)}},
	} {
		t.Run(
			name,
			func(t *testing.T) { require.ErrorIs(t, check(vars...), bad) },
		)
	}
}

func TestPublishNeedsDeliveryForSeveralValues(t *testing.T) {
	task := keyed("flag")
	task.Check = exerciseModel.FlagCheck("ICE{a}", "ICE{b}")
	web := dev("web", exerciseModel.DeviceTypeContainer, "eth0")
	version := func(env string) exerciseModel.ExerciseVersion {
		device := web
		device.Bootstrap.Env = []exerciseModel.EnvVar{{Name: "FLAG", Value: env}}
		return exerciseModel.ExerciseVersion{
			Variants: []exerciseModel.Variant{
				publishVariant(
					exerciseModel.Topology{Devices: []exerciseModel.Device{device}},
					task,
				),
			},
		}
	}
	notDelivered, delivered := version("none"), version("{{task.flag}}")
	require.ErrorIs(t, notDelivered.ValidateForPublish(), exerciseModel.ErrFlagMultiNeedsDevice.Err(), "no reference to the task: nothing is delivered")
	require.NoError(t, delivered.ValidateForPublish())

	// A text answer is held to the same rule.
	text := keyed("word")
	text.Check = exerciseModel.TextCheck("a", "b")
	v := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{publishVariant(exerciseModel.Topology{}, text)},
	}
	require.ErrorIs(t, v.ValidateForPublish(), exerciseModel.ErrFlagMultiNeedsDevice.Err())
}

func TestWithoutSolutionAndTextMatching(t *testing.T) {
	state := keyed("s")
	state.Check = exerciseModel.Check{
		State: &exerciseModel.StateCheck{
			Mode: "once",
			Conditions: []exerciseModel.Condition{
				{File: &exerciseModel.FileObservation{Path: "/x"}},
			},
		},
	}
	redacted := state.WithoutSolution()
	require.Empty(t, redacted.Check.State.Conditions)
	require.Equal(t, "once", string(redacted.Check.State.Mode))
	require.Len(t, state.Check.State.Conditions, 1, "the task itself is not changed")
	answer := keyed("a")
	answer.Check = exerciseModel.FlagCheck("ICE{x}")
	require.Nil(t, answer.WithoutSolution().Check.AnswerValues())
	require.Equal(t, []string{"ICE{x}"}, answer.Check.AnswerValues())
}

func bootstrapVariant(script string, files ...exerciseModel.BootstrapFile) exerciseModel.Variant {
	return exerciseModel.Variant{Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{
		Name: "web", Type: exerciseModel.DeviceTypeNode, Bootstrap: exerciseModel.Bootstrap{Script: script, Files: files},
	}}}}
}

// The size of a bootstrap is bounded per file and per device; a file of the archive counts by its stored size.
func TestValidateBootstrapSizes(t *testing.T) {
	big, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	sizes := map[uuid.UUID]int64{
		big:   exerciseModel.MaxBootstrapFileBytes + 1,
		other: exerciseModel.MaxBootstrapFileBytes,
	}
	size := func(ref exerciseModel.AttachmentRef) (int64, error) { return sizes[ref.FileID], nil }
	source := func(path string, id uuid.UUID) exerciseModel.BootstrapFile {
		return exerciseModel.BootstrapFile{Path: path, Source: &exerciseModel.AttachmentRef{FileID: id}}
	}

	require.NoError(t, bootstrapVariant("", source("/a", other)).ValidateBootstrapSizes(size), "exactly the ceiling is fine")

	err := bootstrapVariant("", source("/etc/blob.bin", big)).ValidateBootstrapSizes(size)
	require.ErrorIs(t, err, exerciseModel.ErrBootstrapTooLarge.Err())
	ctx := err.(interface{ PublicContext() map[string]any }).PublicContext()
	require.Equal(t, "web", ctx["device"])
	require.Equal(t, "bootstrap.files[1].source", ctx["field"])
	require.Equal(t, "/etc/blob.bin", ctx["file"])
	require.Equal(t, exerciseModel.MaxBootstrapFileBytes, ctx["limit"])

	// Two files under the file ceiling whose sum with the script passes the device ceiling.
	err = bootstrapVariant(strings.Repeat("x", 1024), source("/a", other), source("/b", other)).ValidateBootstrapSizes(size)
	require.ErrorIs(t, err, exerciseModel.ErrBootstrapTooLarge.Err())
	ctx = err.(interface{ PublicContext() map[string]any }).PublicContext()
	require.Equal(t, "bootstrap", ctx["field"])
	require.NotContains(t, ctx, "file")
	require.Equal(t, exerciseModel.MaxBootstrapDeviceBytes, ctx["limit"])

	// Exactly the device ceiling passes, inline text counts by its length.
	require.NoError(t, bootstrapVariant("", source("/a", other), exerciseModel.BootstrapFile{
		Path: "/b", Content: strings.Repeat("y", exerciseModel.MaxBootstrapDeviceBytes-exerciseModel.MaxBootstrapFileBytes),
	}).ValidateBootstrapSizes(size))
}

// The access field of the error names the entry, and the readers take the http entry as the web access of the device.
func TestAccessFieldAndReaders(t *testing.T) {
	device := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeNode, Access: []exerciseModel.Access{
		{Kind: exerciseModel.AccessTerminal}, {Kind: exerciseModel.AccessHTTP, Port: 8080},
	}}
	web, ok := device.HTTPAccess()
	require.True(t, ok)
	require.EqualValues(t, 8080, web.Port)
	require.Equal(t, "http", web.Protocol, "an entry without a protocol is http")
	require.True(t, device.HasAccess(exerciseModel.AccessTerminal))
	_, ok = exerciseModel.Device{}.HTTPAccess()
	require.False(t, ok)

	device.Access[1].Port = 0
	err := exerciseModel.Topology{Devices: []exerciseModel.Device{device}}.ValidateDeviceRuntime()
	require.ErrorIs(t, err, exerciseModel.ErrDeviceRuntimeInvalid.Err())
	var withContext interface{ PublicContext() map[string]any }
	require.ErrorAs(t, err, &withContext)
	require.Equal(t, "access[2].port", withContext.PublicContext()["field"])
}

func TestUsesConsole(t *testing.T) {
	web := exerciseModel.Device{Name: "web", Type: exerciseModel.DeviceTypeNode, Access: []exerciseModel.Access{{Kind: exerciseModel.AccessHTTP, Port: 80}}}
	shell := exerciseModel.Device{Name: "sh", Type: exerciseModel.DeviceTypeNode, Access: []exerciseModel.Access{{Kind: exerciseModel.AccessTerminal}}}
	variant := func(devices ...exerciseModel.Device) exerciseModel.Variant {
		return exerciseModel.Variant{Topology: exerciseModel.Topology{Devices: devices}}
	}
	require.False(t, exerciseModel.UsesConsole(nil))
	require.False(t, exerciseModel.UsesConsole([]exerciseModel.Variant{variant(web)}), "http is not a console")
	require.True(t, exerciseModel.UsesConsole([]exerciseModel.Variant{variant(web), variant(web, shell)}), "any variant counts")
	version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{variant(shell)}}
	require.True(t, version.UsesConsole())
}
