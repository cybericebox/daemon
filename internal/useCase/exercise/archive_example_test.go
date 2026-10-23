package exercise_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.yaml.in/yaml/v3"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

const (
	tagRequired = "обовʼязкове."
	tagOptional = "можна пропустити."
	// neededHere marks an optional key the example cannot drop alone: something else in it refers to the key.
	neededHere = "тут потрібн"
)

// exampleEntries unpacks the example archive.
func exampleEntries(t *testing.T) map[string][]byte {
	t.Helper()
	archive, err := exercise.ExampleArchive()
	require.NoError(t, err)
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	require.NoError(t, err)
	entries := map[string][]byte{}
	for _, file := range reader.File {
		rc, openErr := file.Open()
		require.NoError(t, openErr)
		body, readErr := io.ReadAll(rc)
		require.NoError(t, rc.Close())
		require.NoError(t, readErr)
		entries[file.Name] = body
	}
	return entries
}

func exampleRoot(t *testing.T, doc []byte) *yaml.Node {
	t.Helper()
	var root yaml.Node
	require.NoError(t, yaml.Unmarshal(doc, &root))
	require.Equal(t, yaml.DocumentNode, root.Kind)
	require.Equal(t, yaml.MappingNode, root.Content[0].Kind)
	return &root
}

// exampleKey is one key of the example document: its 1-based path, its comment, the steps (Content indices from
// the document node) to the mapping holding it and its index there.
type exampleKey struct {
	Path    string
	Name    string
	Comment string
	Parent  []int
	Index   int
	Value   *yaml.Node
}

func exampleKeys(root *yaml.Node) []exampleKey {
	var keys []exampleKey
	var walk func(node *yaml.Node, path string, steps []int)
	walk = func(node *yaml.Node, path string, steps []int) {
		switch node.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(node.Content); i += 2 {
				key, value := node.Content[i], node.Content[i+1]
				child := key.Value
				if path != "" {
					child = path + "." + key.Value
				}
				comment := key.LineComment
				if comment == "" {
					comment = value.LineComment
				}
				keys = append(keys, exampleKey{
					Path: child, Name: key.Value, Comment: strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(comment), "#")),
					Parent: append([]int(nil), steps...), Index: i, Value: value,
				})
				walk(value, child, append(append([]int(nil), steps...), i+1))
			}
		case yaml.SequenceNode:
			for j, item := range node.Content {
				walk(item, path+"["+strconv.Itoa(j+1)+"]", append(append([]int(nil), steps...), j))
			}
		}
	}
	walk(root.Content[0], "", []int{0})
	return keys
}

// withoutKey is the example document with one key (and its value) removed.
func withoutKey(t *testing.T, doc []byte, key exampleKey) []byte {
	t.Helper()
	root := exampleRoot(t, doc)
	node := root
	for _, step := range key.Parent {
		node = node.Content[step]
	}
	node.Content = append(node.Content[:key.Index:key.Index], node.Content[key.Index+2:]...)
	if len(node.Content) == 0 {
		node.Style = yaml.FlowStyle // the encoder writes an emptied block mapping that carries a comment as invalid YAML
	}
	out, err := yaml.Marshal(root)
	require.NoError(t, err)
	return out
}

// importExample imports one task folder (the example's files, document replaced) through the use case.
func importExample(t *testing.T, files map[string][]byte) (*fakeMedia, [][]byte, error) {
	t.Helper()
	archive, err := exercise.BuildArchiveNoManifest(files)
	require.NoError(t, err)
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	expectUserNames(q)
	expectFilesAdoptedAsIs(q)
	media := newFakeMedia()
	var written [][]byte
	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			return postgres.Exercise{ID: arg.ID, Name: arg.Name, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		}).AnyTimes()
	q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
			require.EqualValues(t, exerciseModel.VersionStatusPublished, arg.Status)
			written = append(written, arg.Variants)
			return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, Variants: arg.Variants, CreatedAt: arg.CreatedAt}, nil
		}).AnyTimes()
	q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	uc := newTestExerciseUseCase(exercise.Dependencies{Repo: q, Media: media})
	_, err = uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{
		Archive: archive, CreatedBy: uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000aa"),
	})
	return media, written, err
}

func replaced(files map[string][]byte, name string, body []byte) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for key, value := range files {
		if key != "exercise.yaml" {
			out[key] = value
		}
	}
	out[name] = body
	return out
}

func TestExampleArchive_ContainsOnlyYAMLAndFiles(t *testing.T) {
	entries := exampleEntries(t)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	require.Equal(t, []string{"exercise.yaml", "files/notes-2.txt", "files/notes.txt"}, names)
	require.Equal(t, "example-task.cybericebox.zip", exercise.ExampleArchiveFilename)
}

func TestExampleArchive_StartsWithSchema(t *testing.T) {
	root := exampleRoot(t, exampleEntries(t)["exercise.yaml"]).Content[0]
	require.Equal(t, "schema", root.Content[0].Value)
	require.Equal(t, "cib-exercise/1", root.Content[1].Value)
}

func TestExampleArchive_ImportsCleanly(t *testing.T) {
	archive, err := exercise.ExampleArchive()
	require.NoError(t, err)
	imports, err := exercise.ExpandExerciseArchiveBundle(archive, "", exercise.DefaultLimits())
	require.NoError(t, err)
	require.Len(t, imports, 1)

	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	expectUserNames(q)
	expectFilesAdoptedAsIs(q)
	media := newFakeMedia()
	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			require.Equal(t, "Приклад завдання", arg.Name)
			return postgres.Exercise{ID: arg.ID, Name: arg.Name, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		})
	var variants []exerciseModel.Variant
	q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
			require.EqualValues(t, exerciseModel.VersionStatusPublished, arg.Status)
			require.NoError(t, json.Unmarshal(arg.Variants, &variants))
			return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, Variants: arg.Variants, CreatedAt: arg.CreatedAt}, nil
		})
	q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).Return(nil)
	uc := newTestExerciseUseCase(exercise.Dependencies{Repo: q, Media: media})
	imported, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{
		Archive: imports[0].Archive, CreatedBy: uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000aa"),
	})
	require.NoError(t, err)
	require.Equal(t, "Приклад завдання", imported.Name)

	require.Len(t, variants, 2)
	base, linked := variants[0], variants[1]
	require.Len(t, base.Tasks, 2)
	require.Contains(t, string(base.Tasks[0].Description), `"type":"variable"`)
	require.Contains(t, string(base.Tasks[0].Description), `"varName":"server"`)
	require.Len(t, base.Topology.Devices, 4)
	devices := map[string]exerciseModel.Device{}
	for _, device := range base.Topology.Devices {
		devices[device.Name] = device
	}
	web := devices["web"]
	require.Equal(t, exerciseModel.DeviceTypeUnmanagedSwitch, devices["sw1"].Type)
	require.Equal(t, exerciseModel.DeviceTypeHub, devices["hub1"].Type)
	require.Equal(t, exerciseModel.SecurityPresetStandard, web.SecurityPreset)
	require.Equal(t, exerciseModel.SecurityPresetExtended, devices["db"].SecurityPreset)
	require.Nil(t, base.Topology.VisualRender, "visual_render: null is no layout")
	require.True(t, web.WaitReadyFile)
	require.NotNil(t, web.External)
	require.EqualValues(t, 80, web.External.Port)
	require.Equal(t, "http", web.External.Protocol)
	require.NotNil(t, web.Persistence)
	require.True(t, web.Persistence.Enabled)
	require.Equal(t, "5s", web.Persistence.Debounce)
	require.Len(t, web.EnvVars, 1)
	require.False(t, web.EnvVars[0].Secret)
	require.Len(t, base.Topology.Connections, 6)
	known := map[uuid.UUID]bool{}
	for _, device := range base.Topology.Devices {
		known[device.ID] = true
	}
	for _, connection := range base.Topology.Connections {
		for _, endpoint := range connection.Endpoints {
			if endpoint.Kind == exerciseModel.EndpointDevice {
				require.True(t, known[endpoint.DeviceID], "a device is referenced by its name")
			}
		}
	}
	second := base.Tasks[1]
	require.Equal(t, uuid.NullUUID{UUID: web.ID, Valid: true}, second.LinkedDeviceID)
	require.Equal(t, "FLAG", second.DeviceFlagVar)
	require.Len(t, second.Flag, 2)
	require.Len(t, second.Placeholders, 5)
	require.Len(t, second.Hints, 4)
	require.Equal(t, uuid.NullUUID{UUID: base.ID, Valid: true}, linked.LinkedTo)
	require.Equal(t, []string{"ICE{example_flag_2}"}, linked.Tasks[0].Flag)
	require.Equal(t, []exerciseModel.ImageOverride{{DeviceID: devices["db"].ID, Image: "redis:7.4"}}, linked.ImageOverrides)
	for _, device := range linked.Topology.Devices {
		if device.Name == "db" {
			require.Equal(t, "redis:7.4", device.Image, "the linked variant runs its own db image")
		} else {
			require.Equal(t, devices[device.Name].Image, device.Image)
		}
	}
	require.Equal(t, []string{"ICE{example_flag}"}, base.Tasks[0].Flag)
	require.Len(t, media.uploads, 2)
}

func TestExampleArchive_EveryKeyHasEndOfLineComment(t *testing.T) {
	keys := exampleKeys(exampleRoot(t, exampleEntries(t)["exercise.yaml"]))
	require.NotEmpty(t, keys)
	for _, key := range keys {
		require.NotEmpty(t, key.Comment, "%s has no end-of-line comment", key.Path)
		require.True(t, strings.HasPrefix(key.Comment, tagRequired) || strings.HasPrefix(key.Comment, tagOptional),
			"%s: the comment must start with %q or %q: %q", key.Path, tagRequired, tagOptional, key.Comment)
		require.Greater(t, len(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(key.Comment, tagRequired), tagOptional))), 0,
			"%s: the comment must say what the key is", key.Path)
	}
}

// requiredKeyErrors is, per required key of the example, what the import error names when the key is deleted:
// the key, its path or the object it belongs to. versions and variants carry the attachments, so without them
// the first error is the file nothing links to any more.
var requiredKeyErrors = map[string]string{
	"schema":                                "schema",
	"exercise":                              "exercise name",
	"exercise.name":                         "exercise name",
	"versions":                              "nothing links to",
	"versions[1].status":                    "versions[1].status: required",
	"versions[1].variants":                  "nothing links to",
	"versions[1].variants[1].tasks[1].name": "task name must be 3-50 characters",
	"versions[1].variants[1].tasks[1].description":                                           "task description is required before publication",
	"versions[1].variants[1].tasks[1].difficulty":                                            "task difficulty is invalid",
	"versions[1].variants[1].tasks[1].flag":                                                  "task flag must use ice{...} without whitespace",
	"versions[1].variants[1].tasks[1].attachments[1].file":                                   "an archive path is not allowed",
	"versions[1].variants[1].tasks[1].placeholders[1].kind":                                  "description placeholder is malformed",
	"versions[1].variants[1].tasks[1].placeholders[1].ip_reference":                          "description placeholder is malformed",
	"versions[1].variants[1].tasks[1].hints[1].text":                                         "hint text is required before publication",
	"versions[1].variants[1].tasks[2].name":                                                  "task name must be 3-50 characters",
	"versions[1].variants[1].tasks[2].description":                                           "task description is required before publication",
	"versions[1].variants[1].tasks[2].difficulty":                                            "task difficulty is invalid",
	"versions[1].variants[1].tasks[2].placeholders[1].kind":                                  "description placeholder is malformed",
	"versions[1].variants[1].tasks[2].placeholders[2].kind":                                  "description placeholder is malformed",
	"versions[1].variants[1].tasks[2].placeholders[2].ip_reference":                          "description placeholder is malformed",
	"versions[1].variants[1].tasks[2].placeholders[3].kind":                                  "description placeholder is malformed",
	"versions[1].variants[1].tasks[2].placeholders[3].ip_reference":                          "description placeholder is malformed",
	"versions[1].variants[1].tasks[2].placeholders[4].kind":                                  "description placeholder is malformed",
	"versions[1].variants[1].tasks[2].placeholders[5].kind":                                  "description placeholder is malformed",
	"versions[1].variants[1].tasks[2].hints[1].text":                                         "hint text is required before publication",
	"versions[1].variants[1].tasks[2].hints[2].text":                                         "hint text is required before publication",
	"versions[1].variants[1].tasks[2].hints[3].text":                                         "hint text is required before publication",
	"versions[1].variants[1].tasks[2].hints[4].text":                                         "hint text is required before publication",
	"versions[1].variants[1].topology.internet.dhcp_ranges[1].start":                         "dhcp ranges or internet dns address are invalid",
	"versions[1].variants[1].topology.internet.dhcp_ranges[1].end":                           "dhcp ranges or internet dns address are invalid",
	"versions[1].variants[1].topology.devices[1].name":                                       "device named \"web\"",
	"versions[1].variants[1].topology.devices[1].type":                                       "device type is invalid",
	"versions[1].variants[1].topology.devices[1].external.port":                              "device external access is invalid",
	"versions[1].variants[1].topology.devices[1].external.protocol":                          "device external access is invalid",
	"versions[1].variants[1].topology.devices[1].interfaces[1].name":                         "device interface is invalid",
	"versions[1].variants[1].topology.devices[1].interfaces[1].ip":                           "device interface is invalid",
	"versions[1].variants[1].topology.devices[1].interfaces[1].ip.type":                      "static address reference is invalid or conflicts with another address",
	"versions[1].variants[1].topology.devices[1].interfaces[1].ip.address_ref":               "static interface requires exactly one ip address",
	"versions[1].variants[1].topology.devices[1].interfaces[1].ip.address_ref.network":       "static address reference is invalid or conflicts with another address",
	"versions[1].variants[1].topology.devices[1].interfaces[1].ip.address_ref.host":          "static address reference is invalid or conflicts with another address",
	"versions[1].variants[1].topology.devices[1].interfaces[2].name":                         "device interface is invalid",
	"versions[1].variants[1].topology.devices[1].interfaces[2].ip":                           "device interface is invalid",
	"versions[1].variants[1].topology.devices[1].interfaces[2].ip.type":                      "device interface is invalid",
	"versions[1].variants[1].topology.devices[2].name":                                       "device named \"db\"",
	"versions[1].variants[1].topology.devices[2].type":                                       "device type is invalid",
	"versions[1].variants[1].topology.devices[2].interfaces[1].name":                         "device interface is invalid",
	"versions[1].variants[1].topology.devices[2].interfaces[1].ip":                           "device interface is invalid",
	"versions[1].variants[1].topology.devices[2].interfaces[1].ip.type":                      "static address reference is invalid or conflicts with another address",
	"versions[1].variants[1].topology.devices[2].interfaces[1].ip.address_ref":               "static interface requires exactly one ip address",
	"versions[1].variants[1].topology.devices[2].interfaces[1].ip.address_ref.network":       "static address reference is invalid or conflicts with another address",
	"versions[1].variants[1].topology.devices[2].interfaces[1].ip.address_ref.host":          "static address reference is invalid or conflicts with another address",
	"versions[1].variants[1].topology.devices[2].interfaces[1].ip.gateway_ref.network":       "static address reference is invalid or conflicts with another address",
	"versions[1].variants[1].topology.devices[2].interfaces[1].ip.gateway_ref.host":          "static address reference is invalid or conflicts with another address",
	"versions[1].variants[1].topology.devices[2].interfaces[1].ip.routes[1].dst_ref.network": "static address reference is invalid or conflicts with another address",
	"versions[1].variants[1].topology.devices[2].interfaces[1].ip.routes[1].via_ref.network": "static address reference is invalid or conflicts with another address",
	"versions[1].variants[1].topology.devices[2].interfaces[1].ip.routes[1].via_ref.host":    "static address reference is invalid or conflicts with another address",
	"versions[1].variants[1].topology.devices[2].interfaces[2].name":                         "device interface is invalid",
	"versions[1].variants[1].topology.devices[2].interfaces[2].ip":                           "device interface is invalid",
	"versions[1].variants[1].topology.devices[2].interfaces[2].ip.type":                      "device interface is invalid",
	"versions[1].variants[1].topology.devices[2].interfaces[3].name":                         "device interface is invalid",
	"versions[1].variants[1].topology.devices[2].interfaces[3].ip":                           "device interface is invalid",
	"versions[1].variants[1].topology.devices[2].interfaces[3].ip.type":                      "device interface is invalid",
	"versions[1].variants[1].topology.devices[3].name":                                       "device named \"sw1\"",
	"versions[1].variants[1].topology.devices[3].type":                                       "device type is invalid",
	"versions[1].variants[1].topology.devices[4].name":                                       "device named \"hub1\"",
	"versions[1].variants[1].topology.devices[4].type":                                       "device type is invalid",
	"versions[1].variants[1].topology.connections[1].endpoints":                              "connection must have exactly two endpoints",
	"versions[1].variants[1].topology.connections[1].endpoints[1].kind":                      "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[1].endpoints[1].interface":                 "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[1].endpoints[2].kind":                      "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[1].endpoints[2].device":                    "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[1].endpoints[2].interface":                 "switch or hub port must be gigabitethernet0/1 through gigabitethernet0/48",
	"versions[1].variants[1].topology.connections[2].endpoints":                              "connection must have exactly two endpoints",
	"versions[1].variants[1].topology.connections[2].endpoints[1].kind":                      "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[2].endpoints[1].device":                    "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[2].endpoints[1].interface":                 "switch or hub port must be gigabitethernet0/1 through gigabitethernet0/48",
	"versions[1].variants[1].topology.connections[2].endpoints[2].kind":                      "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[2].endpoints[2].device":                    "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[2].endpoints[2].interface":                 "connection endpoint references a missing device or interface",
	"versions[1].variants[1].topology.connections[3].endpoints":                              "connection must have exactly two endpoints",
	"versions[1].variants[1].topology.connections[3].endpoints[1].kind":                      "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[3].endpoints[1].device":                    "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[3].endpoints[1].interface":                 "switch or hub port must be gigabitethernet0/1 through gigabitethernet0/48",
	"versions[1].variants[1].topology.connections[3].endpoints[2].kind":                      "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[3].endpoints[2].device":                    "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[3].endpoints[2].interface":                 "connection endpoint references a missing device or interface",
	"versions[1].variants[1].topology.connections[4].endpoints":                              "connection must have exactly two endpoints",
	"versions[1].variants[1].topology.connections[4].endpoints[1].kind":                      "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[4].endpoints[1].interface":                 "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[4].endpoints[2].kind":                      "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[4].endpoints[2].device":                    "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[4].endpoints[2].interface":                 "switch or hub port must be gigabitethernet0/1 through gigabitethernet0/48",
	"versions[1].variants[1].topology.connections[5].endpoints":                              "connection must have exactly two endpoints",
	"versions[1].variants[1].topology.connections[5].endpoints[1].kind":                      "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[5].endpoints[1].device":                    "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[5].endpoints[1].interface":                 "switch or hub port must be gigabitethernet0/1 through gigabitethernet0/48",
	"versions[1].variants[1].topology.connections[5].endpoints[2].kind":                      "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[5].endpoints[2].device":                    "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[5].endpoints[2].interface":                 "connection endpoint references a missing device or interface",
	"versions[1].variants[1].topology.connections[6].endpoints":                              "connection must have exactly two endpoints",
	"versions[1].variants[1].topology.connections[6].endpoints[1].kind":                      "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[6].endpoints[1].device":                    "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[6].endpoints[1].interface":                 "switch or hub port must be gigabitethernet0/1 through gigabitethernet0/48",
	"versions[1].variants[1].topology.connections[6].endpoints[2].kind":                      "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[6].endpoints[2].device":                    "connection must have exactly two valid endpoints",
	"versions[1].variants[1].topology.connections[6].endpoints[2].interface":                 "connection endpoint references a missing device or interface",
	"versions[1].variants[2].linked_to":                                                      "device_images is only for a linked variant (with linked_to)",
	"versions[1].variants[2].tasks[1].flag":                                                  "task flag must use ice{...} without whitespace",
	"versions[1].variants[2].tasks[1].attachments[1].file":                                   "an archive path is not allowed",
}

// Every key, at every level: an optional key may go (unless its comment says something here needs it, and then
// it may not), a required one may not.
func TestExampleArchive_CommentTagsMatchRealOptionality(t *testing.T) {
	files := exampleEntries(t)
	doc := files["exercise.yaml"]
	for _, key := range exampleKeys(exampleRoot(t, doc)) {
		t.Run(key.Path, func(t *testing.T) {
			_, _, err := importExample(t, replaced(files, "exercise.yaml", withoutKey(t, doc, key)))
			switch {
			case strings.HasPrefix(key.Comment, tagRequired):
				require.Error(t, err, "%s is marked required but the example imports without it", key.Path)
				want, listed := requiredKeyErrors[key.Path]
				require.True(t, listed, "%s is required: add what its error says to requiredKeyErrors", key.Path)
				require.Contains(t, strings.ToLower(err.Error()), strings.ToLower(want), "%s: the error must point at the key", key.Path)
			case strings.Contains(key.Comment, neededHere):
				require.Error(t, err, "%s says the example needs it, but the example imports without it", key.Path)
			default:
				require.NoError(t, err, "%s is marked optional but the example fails without it", key.Path)
			}
		})
	}
}

var indexRE = regexp.MustCompile(`\[\d+\]`)

func TestExampleArchive_ListsAllEnumValues(t *testing.T) {
	enums := map[string][]string{
		"versions.status":                                                      {"draft", "published", "unpublished", "checkpoint"},
		"versions.variants.tasks.difficulty":                                   {"elementary", "trivial", "easy", "medium", "hard", "insane"},
		"versions.variants.tasks.hints.level":                                  {"nudge", "direction", "steps", "near_solution"},
		"versions.variants.tasks.placeholders.kind":                            {"vpn.subnet", "internet.subnet", "ip", "external.link"},
		"versions.variants.tasks.placeholders.ip_reference":                    {"vpn", "internet", "static"},
		"versions.variants.topology.devices.type":                              {"container", "unmanaged-switch", "hub"},
		"versions.variants.topology.devices.security_preset":                   {"standard", "extended"},
		"versions.variants.topology.devices.interfaces.ip.type":                {"static", "dhcp", "dhcp-preset", "none"},
		"versions.variants.topology.devices.interfaces.ip.address_ref.network": {"vpn", "internet"},
		"versions.variants.topology.connections.endpoints.kind":                {"device", "vpn", "internet"},
	}
	seen := map[string]bool{}
	for _, key := range exampleKeys(exampleRoot(t, exampleEntries(t)["exercise.yaml"])) {
		values, ok := enums[indexRE.ReplaceAllString(key.Path, "")]
		if !ok {
			continue
		}
		seen[indexRE.ReplaceAllString(key.Path, "")] = true
		for _, value := range values {
			require.Regexp(t, `(^|[^a-z_.-])`+regexp.QuoteMeta(value)+`($|[^a-z_.-])`, key.Comment, "%s must list %q", key.Path, value)
		}
	}
	for path := range enums {
		require.True(t, seen[path], "the example has no %s", path)
	}
}

func TestExampleArchive_ImageCommentMentionsComingFileForm(t *testing.T) {
	found := false
	for _, key := range exampleKeys(exampleRoot(t, exampleEntries(t)["exercise.yaml"])) {
		if key.Name == "image" {
			found = true
			require.Equal(t, yaml.ScalarNode, key.Value.Kind, "the example uses the reference string form")
			require.Contains(t, key.Comment, "image: {file: images/")
		}
	}
	require.True(t, found)
}

func TestExampleArchive_NoIDs(t *testing.T) {
	for _, key := range exampleKeys(exampleRoot(t, exampleEntries(t)["exercise.yaml"])) {
		require.False(t, key.Name == "id" || strings.HasSuffix(key.Name, "_id"), "%s is an id", key.Path)
	}
}

func TestExampleArchive_LinkedVariantMinimal(t *testing.T) {
	var doc struct {
		Versions []struct {
			Variants []map[string]any `yaml:"variants"`
		} `yaml:"versions"`
	}
	require.NoError(t, yaml.Unmarshal(exampleEntries(t)["exercise.yaml"], &doc))
	require.Len(t, doc.Versions, 1)
	require.Len(t, doc.Versions[0].Variants, 2)
	linked := doc.Versions[0].Variants[1]
	require.Equal(t, 1, linked["linked_to"])
	for key := range linked {
		require.Contains(t, []string{"linked_to", "index", "note", "weight", "device_images", "tasks"}, key)
	}
	tasks, ok := linked["tasks"].([]any)
	require.True(t, ok)
	require.Len(t, tasks, 2)
	for _, item := range tasks {
		task, isMap := item.(map[string]any)
		require.True(t, isMap)
		for key := range task {
			require.Contains(t, []string{"flag", "attachments"}, key)
		}
	}
	first := tasks[0].(map[string]any)
	require.Equal(t, []any{"ICE{example_flag_2}"}, first["flag"])
}

func TestExampleArchive_FlagCommentWarnsAboutSpaceHash(t *testing.T) {
	found := false
	for _, key := range exampleKeys(exampleRoot(t, exampleEntries(t)["exercise.yaml"])) {
		if key.Path == "versions[1].variants[1].tasks[1].flag" {
			found = true
			require.Contains(t, key.Comment, " #")
			require.Contains(t, key.Comment, "лапк")
		}
	}
	require.True(t, found)
}

var uuidRE = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// parsedContent is what an archive parses to, with the fresh ids blanked.
func parsedContent(t *testing.T, files map[string][]byte) string {
	t.Helper()
	archive, err := exercise.BuildArchiveNoManifest(files)
	require.NoError(t, err)
	parsed, err := exercise.ParseExerciseArchive(archive, "", exercise.DefaultLimits())
	require.NoError(t, err)
	out, err := json.Marshal(struct {
		Exercise exerciseModel.Exercise
		Versions []exerciseModel.ExerciseVersion
	}{parsed.Exercise, parsed.Versions})
	require.NoError(t, err)
	return uuidRE.ReplaceAllString(string(out), "<id>")
}

func TestExampleArchive_JSONFormImportsToo(t *testing.T) {
	files := exampleEntries(t)
	var generic any
	require.NoError(t, yaml.Unmarshal(files["exercise.yaml"], &generic))
	body, err := json.MarshalIndent(generic, "", "  ")
	require.NoError(t, err)
	asJSON := append([]byte("// the example task, as JSON\n"), body...)
	jsonFiles := replaced(files, "exercise.json", asJSON)

	require.Equal(t, parsedContent(t, files), parsedContent(t, jsonFiles))
	_, written, err := importExample(t, jsonFiles)
	require.NoError(t, err)
	require.Len(t, written, 1)
}

// The example is also served as text, in both forms: the YAML is the file of the archive as it is, the JSON is the same
// content with the key order of the YAML and without comments, and an archive built from it imports to the same task.
func TestExampleDocuments_BothFormsAgree(t *testing.T) {
	files := exampleEntries(t)
	yamlText, jsonText, err := exercise.ExampleDocuments()
	require.NoError(t, err)
	require.Equal(t, string(files["exercise.yaml"]), yamlText)
	require.True(t, json.Valid([]byte(jsonText)))
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(jsonText), &top))
	require.Contains(t, jsonText[:40], `"schema"`, "schema stays the first key")
	require.NotContains(t, jsonText, "обовʼязкове.", "comments are gone")
	jsonFiles := replaced(files, "exercise.json", []byte(jsonText))
	require.Equal(t, parsedContent(t, files), parsedContent(t, jsonFiles))
}
