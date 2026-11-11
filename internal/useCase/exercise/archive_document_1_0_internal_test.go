package exercise

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func variantDoc(tasks, topology string) string {
	return v2Header + `"exercise":{"name":"x"},"versions":[{"status":"draft","variants":[{"tasks":[` + tasks + `],"topology":` + topology + `}]}]}`
}

func parseDoc(t *testing.T, doc string) (ParsedExerciseArchive, error) {
	t.Helper()
	decoded, err := decodeArchiveV2([]byte(doc))
	if err != nil {
		return ParsedExerciseArchive{}, err
	}
	return decoded.toParsed(nil)
}

const webTopology = `{"devices":[{"name":"web","type":"node"}]}`

// The keys of the shape before 1.0 are unknown keys now: one shape, no compatibility.
func TestDecodeArchiveV2_FormerKeysAreUnknown(t *testing.T) {
	for _, tc := range []struct{ name, doc, key string }{
		{"flag", variantDoc(`{"name":"t","flag":["ICE{a}"]}`, `{}`), "flag"},
		{"linked_device", variantDoc(`{"name":"t","linked_device":"web"}`, webTopology), "linked_device"},
		{"linked_device_id", variantDoc(`{"name":"t","linked_device_id":"0192f0a0-0000-7000-8000-000000000001"}`, webTopology), "linked_device_id"},
		{"device_flag_var", variantDoc(`{"name":"t","device_flag_var":"FLAG"}`, webTopology), "device_flag_var"},
		{"env_vars", variantDoc(`{"name":"t"}`, `{"devices":[{"name":"web","type":"node","env_vars":[{"name":"A","value":"b"}]}]}`), "env_vars"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeArchiveV2([]byte(tc.doc))
			require.ErrorIs(t, err, exerciseModel.ErrArchiveInvalid.Err())
			require.Contains(t, publicContext(t, err)["reason"], `unknown key "`+tc.key+`"`)
		})
	}
}

func TestToParsed_CheckShape(t *testing.T) {
	reason := func(t *testing.T, doc string) string {
		t.Helper()
		_, err := parseDoc(t, doc)
		require.ErrorIs(t, err, exerciseModel.ErrArchiveInvalid.Err())
		return publicContext(t, err)["reason"].(string)
	}
	answer := func(body string) string { return variantDoc(`{"name":"t","check":{"answer":`+body+`}}`, `{}`) }

	t.Run("both answer and state", func(t *testing.T) {
		require.Contains(
			t,
			reason(
				t,
				variantDoc(
					`{"name":"t","check":{"answer":{"type":"flag"},"state":{"conditions":[]}}}`,
					`{}`,
				),
			),
			"exactly one of answer and state",
		)
	})
	t.Run("answer type is required and named", func(t *testing.T) {
		require.Contains(
			t,
			reason(t, answer(`{"values":["ICE{a}"]}`)),
			"check.answer.type: required",
		)
		require.Contains(t, reason(t, answer(`{"type":"number"}`)), `unknown value "number"`)
	})
	t.Run("ignore_case and trim are for text only", func(t *testing.T) {
		require.Contains(
			t,
			reason(t, answer(`{"type":"flag","ignore_case":false}`)),
			"ignore_case is only for the type text",
		)
		require.Contains(
			t,
			reason(t, answer(`{"type":"flag","trim":true}`)),
			"trim is only for the type text",
		)
		parsed, err := parseDoc(
			t,
			answer(`{"type":"text","values":["a"],"ignore_case":true,"trim":false}`),
		)
		require.NoError(t, err)
		got := parsed.Versions[0].Variants[0].Tasks[0].Check.Answer
		require.True(t, got.IgnoreCase)
		require.False(t, got.TrimEffective())
	})
	t.Run("a condition names one device and one observation", func(t *testing.T) {
		state := func(condition string) string {
			return variantDoc(
				`{"name":"t","check":{"state":{"conditions":[`+condition+`]}}}`,
				webTopology,
			)
		}
		require.Contains(
			t,
			reason(t, state(`{"file":{"path":"/x"}}`)),
			"conditions[1].device: required",
		)
		require.Contains(
			t,
			reason(t, state(`{"device":"ghost","file":{"path":"/x"}}`)),
			`no single device named "ghost"`,
		)
		require.Contains(t, reason(t, state(`{"device":"web"}`)), "exactly one of file and custom")
		require.Contains(
			t,
			reason(t, state(`{"device":"web","file":{"path":"/x"},"custom":{"run":"true"}}`)),
			"exactly one of file and custom",
		)
	})
	t.Run("interval takes a duration or 0", func(t *testing.T) {
		parse := func(interval string) error {
			_, err := parseDoc(
				t,
				variantDoc(
					`{"name":"t","check":{"state":{"interval":`+interval+`,"conditions":[{"device":"web","file":{"path":"/x"}}]}}}`,
					webTopology,
				),
			)
			return err
		}
		require.NoError(t, parse(`"30s"`))
		require.NoError(t, parse(`0`))
		require.Error(t, parse(`"1s"`))
		require.Error(t, parse(`30`), "a bare number other than 0 is not a duration")
	})
}

func TestToParsed_BootstrapAndDeviceKeys(t *testing.T) {
	parse := func(device string) (ParsedExerciseArchive, error) {
		return parseDoc(t, variantDoc(`{"name":"t"}`, `{"devices":[`+device+`]}`))
	}
	t.Run("files take content or source, never both or none", func(t *testing.T) {
		for _, file := range []string{`{"path":"/a"}`, `{"path":"/a","content":"x","source":"files/a"}`} {
			_, err := parse(`{"name":"web","type":"node","bootstrap":{"files":[` + file + `]}}`)
			require.ErrorIs(t, err, exerciseModel.ErrArchiveInvalid.Err(), file)
		}
	})
	t.Run("a bad runtime key is the device's error with the path", func(t *testing.T) {
		_, err := parse(`{"name":"web","type":"node","bootstrap":{"timeout":"1ms"}}`)
		require.ErrorIs(t, err, exerciseModel.ErrDeviceRuntimeInvalid.Err())
		require.Equal(t, "versions[1].variants[1]", publicContext(t, err)["path"])
		require.Equal(t, "bootstrap.timeout", publicContext(t, err)["field"])
	})
	t.Run("a bad substitution is the author's error", func(t *testing.T) {
		_, err := parse(
			`{"name":"web","type":"node","bootstrap":{"env":[{"name":"A","value":"{{task.ghost}}"}]}}`,
		)
		require.ErrorIs(t, err, exerciseModel.ErrSubstitutionUnresolved.Err())
	})
	t.Run("a switch carries none of them", func(t *testing.T) {
		_, err := parse(`{"name":"sw","type":"unmanaged-switch","terminal":{"enabled":true}}`)
		require.ErrorIs(t, err, exerciseModel.ErrDeviceTypeInvalid.Err())
	})
	t.Run(
		"a task without a key gets a stable one, keys of a document are kept",
		func(t *testing.T) {
			parsed, err := parseDoc(
				t,
				variantDoc(
					`{"name":"first"},{"name":"second","key":"mine"},{"name":"third"}`,
					`{}`,
				),
			)
			require.NoError(t, err)
			tasks := parsed.Versions[0].Variants[0].Tasks
			require.Equal(
				t,
				[]string{"task_1", "mine", "task_3"},
				[]string{tasks[0].Key, tasks[1].Key, tasks[2].Key},
			)
		},
	)
}

// A version with every new key goes out as a document and comes back the same (ids aside).
func TestExportThenParse_EveryKeyOfTheDocumentSurvives(t *testing.T) {
	no, yes := false, true
	web, db := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	source := uuid.Must(uuid.NewV7())
	secret := exerciseModel.Task{
		ID:         uuid.Must(uuid.NewV7()),
		Key:        "secret",
		Name:       "Secret",
		Difficulty: exerciseModel.DifficultyEasy,
		Check: exerciseModel.Check{
			Answer: &exerciseModel.AnswerCheck{
				Type:      exerciseModel.AnswerTypeFlag,
				Values:    []string{"ICE{a}", `template:ICE{\d}`},
				InputHint: "ICE{...}",
			},
		},
	}
	word := exerciseModel.Task{
		ID:         uuid.Must(uuid.NewV7()),
		Key:        "word",
		Name:       "Word",
		Difficulty: exerciseModel.DifficultyEasy,
		Requires:   []string{"secret"},
		Check: exerciseModel.Check{
			Answer: &exerciseModel.AnswerCheck{
				Type:       exerciseModel.AnswerTypeText,
				Values:     []string{"OSPF"},
				IgnoreCase: true,
				Trim:       &no,
			},
		},
	}
	state := exerciseModel.Task{
		ID:         uuid.Must(uuid.NewV7()),
		Key:        "state",
		Name:       "State",
		Difficulty: exerciseModel.DifficultyHard,
		Requires:   []string{"word", "secret"},
		Check: exerciseModel.Check{
			State: &exerciseModel.StateCheck{
				Mode:     exerciseModel.StateModeContinuous,
				Interval: "30s",
				Manual:   true,
				Conditions: []exerciseModel.Condition{
					{
						DeviceID: web,
						Timeout:  "10s",
						File: &exerciseModel.FileObservation{
							Path:     "/etc/x",
							Exists:   &yes,
							Contains: "{{var.pw}}",
							Matches:  `a+`,
							Mode:     "0640",
							Owner:    "root",
							Group:    "root",
						},
					},
					{
						DeviceID: db,
						Custom: &exerciseModel.CustomObservation{
							Run:            "true",
							Interpreter:    "/bin/sh",
							User:           "root",
							OutputContains: "{{task.secret}}",
							OutputMatches:  "^ok$",
						},
					},
				},
			},
		},
	}
	version := exerciseModel.ExerciseVersion{
		ID:     uuid.Must(uuid.NewV7()),
		Status: exerciseModel.VersionStatusDraft,
		Name:   "x",
		Variants: []exerciseModel.Variant{{
			ID: uuid.Must(uuid.NewV7()), Tasks: []exerciseModel.Task{secret, word, state},
			Topology: exerciseModel.Topology{
				Variables: []exerciseModel.Variable{
					{Name: "pw", Template: "[a-z]{8}"},
					{Name: "mode", Value: "ctf"},
				},
				Devices: []exerciseModel.Device{
					{
						ID:      web,
						Name:    "web",
						Type:    exerciseModel.DeviceTypeNode,
						Profile: exerciseModel.NodeProfileContainer,
						Image:   "nginx:1",
						Command: []string{
							"nginx",
						},
						Args:     []string{"-g", "daemon off;"},
						System:   exerciseModel.SystemLinux,
						Hardware: exerciseModel.HardwareFast,
						Terminal: exerciseModel.Terminal{
							Enabled: true,
							Command: "/bin/sh",
							User:    "root",
						},
						Bootstrap: exerciseModel.Bootstrap{
							Timeout:     "60s",
							Interpreter: "/bin/sh",
							Script:      "echo {{var.pw}}",
							Env: []exerciseModel.EnvVar{
								{Name: "FLAG", Value: "{{task.secret}}"},
								{Name: "S", Value: "sealed", Secret: true},
							},
							Files: []exerciseModel.BootstrapFile{
								{
									Path:    "/a",
									Content: "{{var.mode}}",
									Owner:   "root",
									Group:   "root",
									Mode:    "0400",
								},
								{
									Path: "/b",
									Source: &exerciseModel.AttachmentRef{
										FileID: source,
										Name:   "b.cfg",
									},
								},
							},
						},
					},
					{
						ID:      db,
						Name:    "db",
						Type:    exerciseModel.DeviceTypeNode,
						Profile: exerciseModel.NodeProfileContainer,
						Image:   "redis:7",
					},
				},
			},
		}},
	}
	paths := map[uuid.UUID]string{source: "files/b.cfg"}
	document, err := buildV2Document(
		exerciseModel.Exercise{Name: "x"},
		[]exerciseModel.ExerciseVersion{version},
		paths,
	)
	require.NoError(t, err)

	// The document names devices by name and carries no id of a task link.
	for _, gone := range []string{`"flag":`, `"linked_device`, `"device_flag_var"`, `"env_vars"`} {
		require.NotContains(t, strings.ReplaceAll(string(document), `": `, `":`), gone)
	}
	require.Regexp(t, `"device": "web"`, string(document))

	decoded, err := decodeArchiveV2(document)
	require.NoError(t, err)
	files := map[string][]byte{"files/b.cfg": []byte("cfg")}
	parsed, err := decoded.toParsed(files)
	require.NoError(t, err)
	back := parsed.Versions[0].Variants[0]
	original := version.Variants[0]

	// Same content once the ids a document does not carry back are set aside: compare as JSON with ids blanked.
	blank := func(v exerciseModel.Variant) string {
		raw, marshalErr := json.Marshal(v)
		require.NoError(t, marshalErr)
		out := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`).
			ReplaceAllString(string(raw), "<id>")
		return out
	}
	require.JSONEq(t, blank(original), blank(back))
	require.Equal(
		t,
		back.Topology.Devices[0].ID,
		back.Tasks[2].Check.State.Conditions[0].DeviceID,
		"a condition finds its device by name",
	)
	require.Equal(t, back.Topology.Devices[1].ID, back.Tasks[2].Check.State.Conditions[1].DeviceID)
	require.Equal(
		t,
		"files/b.cfg",
		parsed.FilePaths[back.Topology.Devices[0].Bootstrap.Files[1].Source.FileID],
		"a source file is linked like an attachment",
	)
}

// A linked variant may set check.answer.values and attachments of a task, nothing else of the task's check.
func TestDecodeArchiveV2_LinkedVariantCheckKeys(t *testing.T) {
	linked := func(check string) string {
		return v2Header + `"exercise":{"name":"x"},"versions":[{"status":"draft","variants":[` +
			`{"tasks":[{"name":"t","check":{"answer":{"type":"flag"}}}]},{"linked_to":1,"tasks":[{"check":` + check + `}]}]}]}`
	}
	_, err := decodeArchiveV2([]byte(linked(`{"answer":{"values":["ICE{b}"]}}`)))
	require.NoError(t, err)
	for field, check := range map[string]string{
		"check.answer.type":        `{"answer":{"type":"flag"}}`,
		"check.answer.input_hint":  `{"answer":{"input_hint":"x"}}`,
		"check.answer.ignore_case": `{"answer":{"ignore_case":true}}`,
		"check.state":              `{"state":{"conditions":[]}}`,
	} {
		_, err = decodeArchiveV2([]byte(linked(check)))
		require.ErrorIs(t, err, exerciseModel.ErrArchiveLinkedVariantField.Err(), field)
		require.Equal(t, "versions[1].variants[2].tasks[1]."+field, publicContext(t, err)["field"])
	}
	// Topology keys of a linked variant stay forbidden, the new ones included.
	_, err = decodeArchiveV2(
		[]byte(
			v2Header + `"exercise":{"name":"x"},"versions":[{"status":"draft","variants":[{},{"linked_to":1,"topology":{"variables":[]}}]}]}`,
		),
	)
	require.ErrorIs(t, err, exerciseModel.ErrArchiveLinkedVariantField.Err())
}

func TestStatusIsOptionalAndIgnoredByTheDocument(t *testing.T) {
	doc := v2Header + `"exercise":{"name":"x"},"versions":[{"variants":[]}]}`
	parsed, err := parseDoc(t, doc)
	require.NoError(t, err)
	require.Equal(t, exerciseModel.VersionStatusDraft, parsed.Versions[0].Status)
	_, err = parseDoc(t, strings.Replace(doc, `"variants"`, `"status":"bogus","variants"`, 1))
	require.ErrorIs(t, err, exerciseModel.ErrArchiveInvalid.Err())
}

// An archive file over the bootstrap ceiling is refused at import with the path of the file in the device.
func TestToParsed_BootstrapSourceSize(t *testing.T) {
	doc := func(source string) string {
		return variantDoc(
			`{"name":"t"}`,
			`{"devices":[{"name":"web","type":"node","bootstrap":{"files":[{"path":"/etc/app.bin","source":"`+source+`"}]}}]}`,
		)
	}
	decode := func(source string, size int) (ParsedExerciseArchive, error) {
		decoded, err := decodeArchiveV2([]byte(doc(source)))
		require.NoError(t, err)
		return decoded.toParsed(map[string][]byte{source: make([]byte, size)})
	}
	_, err := decode("files/ok.bin", exerciseModel.MaxBootstrapFileBytes)
	require.NoError(t, err)
	_, err = decode("files/big.bin", exerciseModel.MaxBootstrapFileBytes+1)
	require.ErrorIs(t, err, exerciseModel.ErrBootstrapTooLarge.Err())
	ctx := publicContext(t, err)
	require.Equal(t, "/etc/app.bin", ctx["file"])
	require.Equal(t, "web", ctx["device"])
	require.Equal(t, "bootstrap.files[1].source", ctx["field"])
	require.Equal(t, "versions[1].variants[1]", ctx["path"])
}
