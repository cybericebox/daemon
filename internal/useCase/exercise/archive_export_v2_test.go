package exercise_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/text/unicode/norm"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/secret"
)

func TestAssignExportPaths_Levels(t *testing.T) {
	cases := []struct {
		name string
		uses []exercise.FileUse
		want map[string]string
	}{
		{"one use", []exercise.FileUse{{Hash: "h1", Name: "a.txt", Version: 1, Variant: 2, Task: 3}},
			map[string]string{"h1": "files/v1/variant-2/3/a.txt"}},
		{"two subtasks of one variant", []exercise.FileUse{
			{Hash: "h1", Name: "a.txt", Version: 1, Variant: 2, Task: 1},
			{Hash: "h1", Name: "a.txt", Version: 1, Variant: 2, Task: 3},
		}, map[string]string{"h1": "files/v1/variant-2/shared/a.txt"}},
		{"two variants of one version", []exercise.FileUse{
			{Hash: "h1", Name: "a.txt", Version: 1, Variant: 1, Task: 1},
			{Hash: "h1", Name: "a.txt", Version: 1, Variant: 2, Task: 1},
		}, map[string]string{"h1": "files/v1/shared/a.txt"}},
		{"two versions", []exercise.FileUse{
			{Hash: "h1", Name: "a.txt", Version: 1, Variant: 1, Task: 1},
			{Hash: "h1", Name: "a.txt", Version: 2, Variant: 1, Task: 1},
		}, map[string]string{"h1": "files/shared/a.txt"}},
		{"name clash in one folder", []exercise.FileUse{
			{Hash: "h1", Name: "a.txt", Version: 1, Variant: 1, Task: 1},
			{Hash: "h2", Name: "a.txt", Version: 1, Variant: 1, Task: 1},
		}, map[string]string{"h1": "files/v1/variant-1/1/a.txt", "h2": "files/v1/variant-1/1/a_2.txt"}},
		{"clash without extension", []exercise.FileUse{
			{Hash: "h1", Name: "README", Version: 1, Variant: 1, Task: 1},
			{Hash: "h2", Name: "README", Version: 1, Variant: 1, Task: 1},
			{Hash: "h3", Name: "README", Version: 1, Variant: 1, Task: 1},
		}, map[string]string{"h1": "files/v1/variant-1/1/README", "h2": "files/v1/variant-1/1/README_2", "h3": "files/v1/variant-1/1/README_3"}},
		{"first use names the file", []exercise.FileUse{
			{Hash: "h1", Name: "second.txt", Version: 1, Variant: 2, Task: 1},
			{Hash: "h1", Name: "first.txt", Version: 1, Variant: 1, Task: 1},
		}, map[string]string{"h1": "files/v1/shared/first.txt"}},
		{"name sanitized", []exercise.FileUse{{Hash: "h1", Name: "../x/y.txt", Version: 1, Variant: 1, Task: 1}},
			map[string]string{"h1": "files/v1/variant-1/1/y.txt"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, exercise.AssignExportPaths("files", tc.uses))
		})
	}
	require.Equal(t, map[string]string{"h1": "images/v1/variant-2/3/a.txt"},
		exercise.AssignExportPaths("images", []exercise.FileUse{{Hash: "h1", Name: "a.txt", Version: 1, Variant: 2, Task: 3}}))
}

func TestAssignExportPaths_Deterministic(t *testing.T) {
	uses := []exercise.FileUse{
		{Hash: "h1", Name: "a.txt", Version: 1, Variant: 1, Task: 1},
		{Hash: "h2", Name: "a.txt", Version: 1, Variant: 1, Task: 1},
		{Hash: "h3", Name: "b.bin", Version: 1, Variant: 1, Task: 2},
		{Hash: "h3", Name: "c.bin", Version: 2, Variant: 1, Task: 1},
		{Hash: "h4", Name: "b.bin", Version: 2, Variant: 3, Task: 1},
		{Hash: "h4", Name: "b.bin", Version: 2, Variant: 3, Task: 4},
	}
	want := exercise.AssignExportPaths("files", uses)
	// Every rotation and the reverse of the input give the same map.
	for shift := range uses {
		rotated := append(append([]exercise.FileUse{}, uses[shift:]...), uses[:shift]...)
		require.Equal(t, want, exercise.AssignExportPaths("files", rotated))
		reversed := make([]exercise.FileUse, len(rotated))
		for i := range rotated {
			reversed[len(rotated)-1-i] = rotated[i]
		}
		require.Equal(t, want, exercise.AssignExportPaths("files", reversed))
	}
}

func TestExportName_Sanitizes(t *testing.T) {
	cases := map[string]string{
		"../x/y.txt":         "y.txt",
		`a\b`:                "a_b",
		"":                   "file",
		".":                  "_",
		"..":                 "__",
		"dir/":               "file",
		"tab\there":          "tab_here",
		".DS_Store":          "_.DS_Store",
		"exercise.json":      "_exercise.json",
		"exercise.yaml":      "_exercise.yaml",
		norm.NFD.String("й"): "й",
		"Звіт.pdf":           "Звіт.pdf",
	}
	for in, want := range cases {
		require.Equal(t, want, exercise.ExportName(in), "input %q", in)
	}
	long := exercise.ExportName(strings.Repeat("ш", 300) + ".txt")
	require.LessOrEqual(t, len(long), 255, "at most 255 bytes")
	require.True(t, utf8.ValidString(long))
	require.True(t, strings.HasSuffix(long, ".txt"), "the extension is kept")
	require.Equal(t, strings.Repeat("ш", 125)+".txt", long)

	// The clash suffix fits inside the cap too.
	paths := exercise.AssignExportPaths("files", []exercise.FileUse{
		{Hash: "h1", Name: strings.Repeat("ш", 300) + ".txt", Version: 1, Variant: 1, Task: 1, Attachment: 1},
		{Hash: "h2", Name: strings.Repeat("ш", 300) + ".txt", Version: 1, Variant: 1, Task: 1, Attachment: 2},
	})
	second := strings.TrimPrefix(paths["h2"], "files/v1/variant-1/1/")
	require.LessOrEqual(t, len(second), 255)
	require.True(t, strings.HasSuffix(second, "_2.txt"), second)
}

// The attachment order inside one subtask, not the name, decides which file is first.
func TestAssignExportPaths_AttachmentOrder(t *testing.T) {
	paths := exercise.AssignExportPaths("files", []exercise.FileUse{
		{Hash: "h2", Name: "x.txt", Version: 1, Variant: 1, Task: 1, Attachment: 2},
		{Hash: "h3", Name: "x.txt", Version: 1, Variant: 1, Task: 1, Attachment: 1},
		{Hash: "h1", Name: "z.txt", Version: 1, Variant: 1, Task: 1, Attachment: 3},
		{Hash: "h1", Name: "a.txt", Version: 1, Variant: 1, Task: 1, Attachment: 4},
	})
	require.Equal(t, map[string]string{
		"h3": "files/v1/variant-1/1/x.txt",
		"h2": "files/v1/variant-1/1/x_2.txt",
		"h1": "files/v1/variant-1/1/z.txt",
	}, paths)
}

// exportMedia is fakeMedia plus the stored files the export streams.
type exportMedia struct {
	*fakeMedia
	files map[uuid.UUID]storedFile
}

type storedFile struct {
	name string
	body []byte
}

func (m *exportMedia) StreamFile(_ context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	f, ok := m.files[id]
	if !ok {
		return nil, mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	sum := sha256.Sum256(f.body)
	return io.NopCloser(bytes.NewReader(f.body)), mediaModel.File{ID: id, Name: f.name, ContentHash: hex.EncodeToString(sum[:])}, nil
}

var (
	exportExerciseID = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000e001")
	fileOnce         = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000f001")
	fileCommon       = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000f002")
	fileCopy1        = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000f003")
	fileCopy2        = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000f004")
	fileUnicode      = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000f005")
)

func exportID(n int) uuid.UUID {
	return uuid.FromStringOrNil("0192f0a0-0000-7000-8000-" + strings.Repeat("0", 8) + hex.EncodeToString([]byte{byte(n >> 8), byte(n)}))
}

func exportFixtureMedia() *exportMedia {
	return &exportMedia{fakeMedia: newFakeMedia(), files: map[uuid.UUID]storedFile{
		fileOnce:    {name: "once.txt", body: []byte("used once")},
		fileCommon:  {name: "common.bin", body: []byte("used in two variants")},
		fileCopy1:   {name: "copy1.txt", body: []byte("same bytes")},
		fileCopy2:   {name: "copy2.txt", body: []byte("same bytes")},
		fileUnicode: {name: "x", body: []byte("unicode")},
	}}
}

// exportFixtureVersions: version 1 has two variants (once.txt in v1/1/1, common.bin in both variants,
// two files with equal bytes in variant 2 tasks 1 and 2); version 2 holds an NFD-named attachment.
func exportFixtureVersions() []exerciseModel.ExerciseVersion {
	device := func(id int, name string) exerciseModel.Device {
		return exerciseModel.Device{
			ID: exportID(id), Name: name, Type: exerciseModel.DeviceTypeContainer, Image: "nginx:1.27",
			SecurityPreset: exerciseModel.SecurityPresetBasic, ResourcePreset: "micro",
			EnvVars: []exerciseModel.EnvVar{{Name: "MODE", Value: "prod"}},
		}
	}
	topology := func(base int) exerciseModel.Topology {
		return exerciseModel.Topology{
			VPN:     exerciseModel.NetworkSpec{Enabled: true},
			Devices: []exerciseModel.Device{device(base, "web"), device(base+1, "db")},
			Connections: []exerciseModel.Connection{{Endpoints: []exerciseModel.Endpoint{
				{Kind: exerciseModel.EndpointVPN}, {Kind: exerciseModel.EndpointDevice, DeviceID: exportID(base)},
			}}},
			VisualRender: json.RawMessage(`{"nodes":[]}`),
		}
	}
	task := func(id int, name string, attachments ...exerciseModel.AttachmentRef) exerciseModel.Task {
		return exerciseModel.Task{
			ID: exportID(id), Name: name, Description: json.RawMessage(`{"root":{"type":"text","text":"Опис"}}`),
			Difficulty: exerciseModel.DifficultyMedium, Flag: []string{"ICE{" + name + "}"},
			LinkedDeviceID: uuid.NullUUID{UUID: exportID(100), Valid: true}, DeviceFlagVar: "FLAG",
			Attachments: attachments,
			Hints:       []exerciseModel.Hint{{ID: exportID(id + 50), Text: "підказка", Level: exerciseModel.HintLevelSteps}},
		}
	}
	return []exerciseModel.ExerciseVersion{
		{ID: exportID(900), ExerciseID: exportExerciseID, Status: exerciseModel.VersionStatusDraft, AdminNote: "нотатка", Variants: []exerciseModel.Variant{
			{ID: exportID(1), Index: 1, Note: "перший", Topology: topology(100), Tasks: []exerciseModel.Task{
				task(10, "one", exerciseModel.AttachmentRef{FileID: fileOnce, Name: "once.txt"}),
				task(11, "two", exerciseModel.AttachmentRef{FileID: fileCommon, Name: "common.bin"}),
			}},
			{ID: exportID(2), Index: 2, Topology: topology(100), Tasks: []exerciseModel.Task{
				task(10, "one", exerciseModel.AttachmentRef{FileID: fileCommon, Name: "common.bin"},
					exerciseModel.AttachmentRef{FileID: fileCopy1, Name: "copy1.txt"}),
				task(11, "two", exerciseModel.AttachmentRef{FileID: fileCopy2, Name: "copy2.txt"}),
			}},
		}},
		{ID: exportID(901), ExerciseID: exportExerciseID, Status: exerciseModel.VersionStatusCheckpoint, Label: "Знімок", Variants: []exerciseModel.Variant{
			{ID: exportID(3), Topology: topology(200), Tasks: []exerciseModel.Task{
				func() exerciseModel.Task {
					t := task(12, "three", exerciseModel.AttachmentRef{FileID: fileUnicode, Name: norm.NFD.String("Йод.txt")})
					t.LinkedDeviceID = uuid.NullUUID{UUID: exportID(200), Valid: true}
					return t
				}(),
			}},
		}},
	}
}

// expectExport mocks the two reads an export does.
func expectExport(t *testing.T, q *postgresMocks.MockQuerier, id uuid.UUID, name string, versions []exerciseModel.ExerciseVersion) {
	t.Helper()
	q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(postgres.Exercise{ID: id, Name: name, Description: "опис", Tags: []string{"web"}}, nil)
	rows := make([]postgres.ExerciseVersion, 0, len(versions))
	for _, version := range versions {
		rows = append(rows, postgres.ExerciseVersion{
			ID: version.ID, ExerciseID: id, Status: string(version.Status), AdminNote: version.AdminNote, Label: version.Label,
			Variants: mustMarshal(t, version.Variants),
		})
	}
	q.EXPECT().ListExerciseVersions(gomock.Any(), id).Return(rows, nil)
}

func exportFixture(t *testing.T) []byte {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	expectExport(t, q, exportExerciseID, "Експорт", exportFixtureVersions())
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: exportFixtureMedia()})
	data, err := uc.ExportExerciseArchive(context.Background(), exportExerciseID, exercise.ExportOptions{}, exercise.DefaultLimits())
	require.NoError(t, err)
	return data
}

func exportedDocument(t *testing.T, data []byte) []byte {
	t.Helper()
	files, err := exercise.ReadArchiveV1(bytes.NewReader(data), exercise.DefaultLimits())
	require.NoError(t, err)
	return files["exercise.json"]
}

// objectKeys lists the keys of a JSON object in document order.
func objectKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	_, err := decoder.Token()
	require.NoError(t, err)
	var keys []string
	for decoder.More() {
		key, err := decoder.Token()
		require.NoError(t, err)
		keys = append(keys, key.(string))
		var skip json.RawMessage
		require.NoError(t, decoder.Decode(&skip))
	}
	return keys
}

// allKeys collects every object key at any depth, except inside opaque editor documents.
func allKeys(value any, out map[string]bool) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			out[key] = true
			if key == "description" || key == "visual_render" {
				continue
			}
			allKeys(child, out)
		}
	case []any:
		for _, child := range v {
			allKeys(child, out)
		}
	}
}

func TestExportExerciseArchive_WritesSchema(t *testing.T) {
	doc := exportedDocument(t, exportFixture(t))
	require.True(t, json.Valid(doc), "plain JSON, no comments")
	require.Equal(t, []string{"schema", "exercise", "versions"}, objectKeys(t, doc))
	var top struct {
		Schema   string         `json:"schema"`
		Exercise map[string]any `json:"exercise"`
	}
	require.NoError(t, json.Unmarshal(doc, &top))
	require.Equal(t, "cib-exercise/1", top.Schema)
	require.Equal(t, map[string]any{"name": "Експорт", "description": "опис", "tags": []any{"web"}}, top.Exercise)
	var generic any
	require.NoError(t, json.Unmarshal(doc, &generic))
	keys := map[string]bool{}
	allKeys(generic, keys)
	for key := range keys {
		require.False(t, strings.ContainsFunc(key, unicode.IsUpper), "upper-case key %q", key)
	}
	require.NotContains(t, string(doc), "//")
}

func TestExportExerciseArchive_EnumsByName(t *testing.T) {
	var doc struct {
		Versions []struct {
			Status   any `json:"status"`
			Variants []struct {
				Tasks []struct {
					Difficulty any `json:"difficulty"`
					Hints      []struct {
						Level any `json:"level"`
					} `json:"hints"`
				} `json:"tasks"`
				Topology struct {
					Devices []struct {
						Type           any `json:"type"`
						SecurityPreset any `json:"security_preset"`
					} `json:"devices"`
					Connections []struct {
						Endpoints []struct {
							Kind   any    `json:"kind"`
							Device string `json:"device"`
						} `json:"endpoints"`
					} `json:"connections"`
				} `json:"topology"`
			} `json:"variants"`
		} `json:"versions"`
	}
	require.NoError(t, json.Unmarshal(exportedDocument(t, exportFixture(t)), &doc))
	require.Equal(t, "draft", doc.Versions[0].Status)
	require.Equal(t, "checkpoint", doc.Versions[1].Status)
	variant := doc.Versions[0].Variants[0]
	require.Equal(t, "medium", variant.Tasks[0].Difficulty)
	require.Equal(t, "steps", variant.Tasks[0].Hints[0].Level)
	require.Equal(t, "container", variant.Topology.Devices[0].Type)
	require.Equal(t, "basic", variant.Topology.Devices[0].SecurityPreset)
	require.Equal(t, "vpn", variant.Topology.Connections[0].Endpoints[0].Kind)
	require.Equal(t, "device", variant.Topology.Connections[0].Endpoints[1].Kind)
	require.Equal(t, "web", variant.Topology.Connections[0].Endpoints[1].Device)
}

func TestExportExerciseArchive_LayoutAndDedupe(t *testing.T) {
	data := exportFixture(t)
	files, err := exercise.ReadArchiveV1(bytes.NewReader(data), exercise.DefaultLimits())
	require.NoError(t, err)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	require.Equal(t, []string{
		"exercise.json",
		"files/v1/shared/common.bin",
		"files/v1/variant-1/1/once.txt",
		"files/v1/variant-2/shared/copy1.txt",
		"files/v2/variant-1/1/Йод.txt",
	}, names)
	require.Equal(t, []byte("same bytes"), files["files/v1/variant-2/shared/copy1.txt"])

	var doc struct {
		Versions []struct {
			Variants []struct {
				Tasks []struct {
					Attachments []map[string]string `json:"attachments"`
				} `json:"tasks"`
			} `json:"variants"`
		} `json:"versions"`
	}
	require.NoError(t, json.Unmarshal(files["exercise.json"], &doc))
	v1 := doc.Versions[0].Variants
	require.Equal(t, []map[string]string{{"file": "files/v1/variant-1/1/once.txt"}}, v1[0].Tasks[0].Attachments)
	require.Equal(t, []map[string]string{{"file": "files/v1/shared/common.bin"}}, v1[0].Tasks[1].Attachments)
	require.Equal(t, []map[string]string{{"file": "files/v1/shared/common.bin"}, {"file": "files/v1/variant-2/shared/copy1.txt"}}, v1[1].Tasks[0].Attachments)
	require.Equal(t, []map[string]string{{"file": "files/v1/variant-2/shared/copy1.txt", "name": "copy2.txt"}}, v1[1].Tasks[1].Attachments)
	require.Equal(t, []map[string]string{{"file": "files/v2/variant-1/1/Йод.txt", "name": norm.NFD.String("Йод.txt")}},
		doc.Versions[1].Variants[0].Tasks[0].Attachments)

	// The same content exports byte for byte the same.
	require.Equal(t, data, exportFixture(t))
}

// contentIDs replaces every attachment file id by an id derived from its bytes, so a source and its
// re-import compare equal.
func contentIDs(t *testing.T, versions []exerciseModel.ExerciseVersion, body func(uuid.UUID) []byte) []byte {
	t.Helper()
	type comparable struct {
		Status    exerciseModel.VersionStatus
		AdminNote string
		Label     string
		Variants  []exerciseModel.Variant
	}
	out := make([]comparable, 0, len(versions))
	for _, version := range versions {
		variants := make([]exerciseModel.Variant, 0, len(version.Variants))
		require.NoError(t, json.Unmarshal(mustMarshal(t, version.Variants), &variants))
		for vi := range variants {
			for ti := range variants[vi].Tasks {
				for ai := range variants[vi].Tasks[ti].Attachments {
					ref := &variants[vi].Tasks[ti].Attachments[ai]
					ref.FileID = uuid.NewV5(uuid.NamespaceOID, string(body(ref.FileID)))
				}
			}
		}
		out = append(out, comparable{Status: version.Status, AdminNote: version.AdminNote, Label: version.Label, Variants: variants})
	}
	data, err := json.Marshal(out)
	require.NoError(t, err)
	return data
}

func TestExportThenImport_RoundTripV2(t *testing.T) {
	parsed, err := exercise.ParseExerciseArchive(exportFixture(t), "", exercise.DefaultLimits())
	require.NoError(t, err)
	for i := range parsed.Versions {
		exercise.NormalizeContentIDs(parsed.Versions[i].Variants)
	}
	require.Equal(t, exerciseModel.Exercise{Name: "Експорт", Description: "опис", Tags: []string{"web"}}, parsed.Exercise)
	media := exportFixtureMedia()
	want := contentIDs(t, exportFixtureVersions(), func(id uuid.UUID) []byte { return media.files[id].body })
	got := contentIDs(t, parsed.Versions, func(id uuid.UUID) []byte {
		return parsed.Files[parsed.FilePaths[id]]
	})
	require.JSONEq(t, string(want), string(got))
}

func TestExportExerciseArchive_ProtectedV2(t *testing.T) {
	cipher, err := secret.New(testKey)
	require.NoError(t, err)
	versions := exportFixtureVersions()
	variant := &versions[0].Variants[0]
	sealed, err := cipher.EncryptWithContext([]byte("plain-secret-value"), exercise.EnvSecretContext(variant.ID, "web", "DB_PASS"))
	require.NoError(t, err)
	variant.Topology.Devices[0].EnvVars = append(variant.Topology.Devices[0].EnvVars, exerciseModel.EnvVar{Name: "DB_PASS", Value: sealed, Secret: true})

	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	expectExport(t, q, exportExerciseID, "Експорт", versions)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher, Media: exportFixtureMedia()})
	data, err := uc.ExportExerciseArchive(context.Background(), exportExerciseID, exercise.ExportOptions{IncludeSecrets: true, Password: "strong password"}, exercise.DefaultLimits())
	require.NoError(t, err)
	require.NotContains(t, string(data), "plain-secret-value")
	_, err = exercise.ReadArchiveV1(bytes.NewReader(data), exercise.DefaultLimits())
	require.Error(t, err, "a protected export is not readable without the password")

	files, err := exercise.ReadProtectedArchiveV1(data, "strong password", exercise.DefaultLimits())
	require.NoError(t, err)
	require.Contains(t, string(files["exercise.json"]), "plain-secret-value")
	require.Contains(t, files, "files/v1/shared/common.bin")
	parsed, err := exercise.ParseExerciseArchive(data, "strong password", exercise.DefaultLimits())
	require.NoError(t, err)
	require.Equal(t, "plain-secret-value", parsed.Versions[0].Variants[0].Topology.Devices[0].EnvVars[1].Value)
}

func TestExportSeveralTasks_FoldersInOneZip(t *testing.T) {
	second := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000e002")
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	expectExport(t, q, exportExerciseID, "Веб", exportFixtureVersions())
	expectExport(t, q, second, "Веб", []exerciseModel.ExerciseVersion{{ID: exportID(902), Status: exerciseModel.VersionStatusDraft}})
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: exportFixtureMedia()})
	var items []exercise.NamedArchive
	for _, id := range []uuid.UUID{exportExerciseID, second} {
		data, err := uc.ExportExerciseArchive(context.Background(), id, exercise.ExportOptions{}, exercise.DefaultLimits())
		require.NoError(t, err)
		items = append(items, exercise.NamedArchive{Name: "Веб", Archive: data})
	}
	bundle, err := exercise.BundleExerciseArchives(items, "", exercise.DefaultLimits())
	require.NoError(t, err)

	raw, err := exercise.ReadArchiveV1(bytes.NewReader(bundle), exercise.DefaultLimits())
	require.NoError(t, err)
	require.Contains(t, raw, "Веб/exercise.json")
	require.Contains(t, raw, "Веб/files/v1/shared/common.bin")
	require.Contains(t, raw, "Веб_2/exercise.json")
	for name := range raw {
		require.False(t, strings.HasSuffix(name, "/manifest.json"), "only one root manifest: %s", name)
	}
	require.Contains(t, rawEntries(t, bundle), "manifest.json")

	imports, err := exercise.ExpandExerciseArchiveBundle(bundle, "", exercise.DefaultLimits())
	require.NoError(t, err)
	require.Len(t, imports, 2)
	for _, item := range imports {
		_, parseErr := exercise.ParseExerciseArchive(item.Archive, item.Password, exercise.DefaultLimits())
		require.NoError(t, parseErr)
	}
}

func TestExportSeveralTasks_ProtectedBundle(t *testing.T) {
	cipher, err := secret.New(testKey)
	require.NoError(t, err)
	versions := exportFixtureVersions()
	variant := &versions[0].Variants[0]
	sealed, err := cipher.EncryptWithContext([]byte("plain-secret-value"), exercise.EnvSecretContext(variant.ID, "web", "DB_PASS"))
	require.NoError(t, err)
	variant.Topology.Devices[0].EnvVars = append(variant.Topology.Devices[0].EnvVars, exerciseModel.EnvVar{Name: "DB_PASS", Value: sealed, Secret: true})
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	expectExport(t, q, exportExerciseID, "Веб", versions)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher, Media: exportFixtureMedia()})
	data, err := uc.ExportExerciseArchive(context.Background(), exportExerciseID, exercise.ExportOptions{IncludeSecrets: true, Password: "pw"}, exercise.DefaultLimits())
	require.NoError(t, err)
	bundle, err := exercise.BundleExerciseArchives([]exercise.NamedArchive{{Name: "Веб", Archive: data}, {Name: "manifest.json", Archive: data}}, "pw", exercise.DefaultLimits())
	require.NoError(t, err)
	require.NotContains(t, string(bundle), "plain-secret-value")
	files, err := exercise.ReadProtectedArchiveV1(bundle, "pw", exercise.DefaultLimits())
	require.NoError(t, err)
	require.Contains(t, string(files["Веб/exercise.json"]), "plain-secret-value")
	require.Contains(t, files, "manifest_2.json/exercise.json")
}

// rawEntries lists the zip entries as stored, the manifest included.
func rawEntries(t *testing.T, data []byte) []string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	names := make([]string, 0, len(reader.File))
	for _, f := range reader.File {
		names = append(names, f.Name)
	}
	return names
}

// richVersions covers the rest of the document: placeholders, interfaces with references, external
// access, persistence, the internet network, two devices sharing a name, an endpoint to a device outside
// the variant, an attachment without a stored name and a name clash that gives _2.
func richVersions() []exerciseModel.ExerciseVersion {
	ref := &exerciseModel.NetworkIPRef{Network: "vpn", Host: 10}
	devices := []exerciseModel.Device{
		{
			ID: exportID(300), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx:1.27",
			ResourcePreset: "small", SecurityPreset: exerciseModel.SecurityPresetNet,
			Interfaces: []exerciseModel.Interface{{Name: "eth0", MAC: "02:00:00:00:00:01", IP: exerciseModel.IPConfig{
				Type: exerciseModel.IPConfigTypeStatic, AddressRef: ref, GatewayRef: &exerciseModel.NetworkIPRef{Network: "vpn", Host: 1},
				Routes: []exerciseModel.Route{{DstRef: &exerciseModel.NetworkSubnetRef{Network: "internet"}, Via: "10.0.0.1"}},
			}}},
			External:    &exerciseModel.ExternalAccess{Port: 8080, Protocol: "http"},
			Persistence: &exerciseModel.DevicePersistence{Enabled: true, Debounce: "5s"},
		},
		{ID: exportID(301), Name: "twin", Type: exerciseModel.DeviceTypeContainer, Image: "alpine"},
		{ID: exportID(302), Name: "twin", Type: exerciseModel.DeviceTypeContainer, Image: "alpine",
			Interfaces: []exerciseModel.Interface{{Name: "eth0", IP: exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeDHCP}}}},
	}
	return []exerciseModel.ExerciseVersion{{ID: exportID(910), Status: exerciseModel.VersionStatusDraft, Variants: []exerciseModel.Variant{{
		ID: exportID(20), Index: 1,
		Topology: exerciseModel.Topology{
			VPN:      exerciseModel.NetworkSpec{Enabled: true, DHCP: true, DHCPRanges: []exerciseModel.DHCPRange{{Start: 100, End: 200}}},
			Internet: exerciseModel.NetworkSpec{Enabled: true, DNS: "1.1.1.1"},
			Devices:  devices,
			Connections: []exerciseModel.Connection{
				{Endpoints: []exerciseModel.Endpoint{{Kind: exerciseModel.EndpointInternet}, {Kind: exerciseModel.EndpointDevice, DeviceID: exportID(302), Interface: "eth0"}}},
				{Endpoints: []exerciseModel.Endpoint{{Kind: exerciseModel.EndpointDevice, DeviceID: exportID(301)}, {Kind: exerciseModel.EndpointDevice, DeviceID: exportID(399)}}},
			},
		},
		Tasks: []exerciseModel.Task{{
			ID: exportID(30), Name: "rich", Difficulty: exerciseModel.DifficultyHard,
			Description:    json.RawMessage(`{"root":{"type":"root","children":[]}}`),
			LinkedDeviceID: uuid.NullUUID{UUID: exportID(302), Valid: true}, DeviceFlagVar: "FLAG",
			Placeholders: []exerciseModel.Placeholder{
				{Key: "server", Kind: exerciseModel.PlaceholderIP, IPReference: "vpn", LastOctet: 10, ShowMask: true},
				{Key: "site", Kind: exerciseModel.PlaceholderExternalLink, AsLink: true, Scheme: "https"},
			},
			Attachments: []exerciseModel.AttachmentRef{
				{FileID: fileNameless},
				{FileID: fileClashA, Name: "notes.txt"},
				{FileID: fileClashB, Name: "notes.txt"},
				{FileID: fileReserved, Name: "exercise.json"},
			},
		}},
	}}}}
}

var (
	fileNameless = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000f101")
	fileClashA   = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000f102")
	fileClashB   = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000f103")
	fileReserved = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000f104")
)

func richMedia() *exportMedia {
	return &exportMedia{fakeMedia: newFakeMedia(), files: map[uuid.UUID]storedFile{
		fileNameless: {name: "notes.txt", body: []byte("stored without a shown name")},
		fileClashA:   {name: "a", body: []byte("first notes")},
		fileClashB:   {name: "b", body: []byte("second notes")},
		fileReserved: {name: "c", body: []byte("named like the document")},
	}}
}

func TestExportThenImport_RoundTripRich(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	expectExport(t, q, exportExerciseID, "Повний", richVersions())
	media := richMedia()
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: media})
	data, err := uc.ExportExerciseArchive(context.Background(), exportExerciseID, exercise.ExportOptions{}, exercise.DefaultLimits())
	require.NoError(t, err)

	files, err := exercise.ReadArchiveV1(bytes.NewReader(data), exercise.DefaultLimits())
	require.NoError(t, err)
	require.Contains(t, files, "files/v1/variant-1/1/notes.txt")
	require.Contains(t, files, "files/v1/variant-1/1/notes_2.txt")
	require.Contains(t, files, "files/v1/variant-1/1/notes_3.txt")
	require.Contains(t, files, "files/v1/variant-1/1/_exercise.json")
	doc := string(files["exercise.json"])
	require.Contains(t, doc, `"device_id": "`+exportID(302).String()+`"`, "a shared name is written with the id")
	require.Contains(t, doc, `"device_id": "`+exportID(399).String()+`"`, "a device outside the variant keeps its id")

	parsed, err := exercise.ParseExerciseArchive(data, "", exercise.DefaultLimits())
	require.NoError(t, err)
	exercise.NormalizeContentIDs(parsed.Versions[0].Variants)
	// The attachment without a stored name comes back with the stored file's name.
	want := richVersions()
	want[0].Variants[0].Tasks[0].Attachments[0].Name = "notes.txt"
	body := func(id uuid.UUID) []byte { return media.files[id].body }
	require.JSONEq(t, string(contentIDs(t, want, body)), string(contentIDs(t, parsed.Versions, func(id uuid.UUID) []byte {
		return parsed.Files[parsed.FilePaths[id]]
	})))
}

// exportVersions exports versions of one exercise with the given media.
func exportVersions(t *testing.T, versions []exerciseModel.ExerciseVersion, media *exportMedia) []byte {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	expectExport(t, q, exportExerciseID, "Повний", versions)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: media})
	data, err := uc.ExportExerciseArchive(context.Background(), exportExerciseID, exercise.ExportOptions{}, exercise.DefaultLimits())
	require.NoError(t, err)
	return data
}

// A file stored as exercise.yaml, attached without a shown name, is renamed in the archive and keeps its
// shown name.
func TestExportExerciseArchive_MediaNameFallbackReserved(t *testing.T) {
	media := &exportMedia{fakeMedia: newFakeMedia(), files: map[uuid.UUID]storedFile{
		fileNameless: {name: "exercise.yaml", body: []byte("yaml bytes")},
	}}
	versions := []exerciseModel.ExerciseVersion{{ID: exportID(920), Status: exerciseModel.VersionStatusDraft, Variants: []exerciseModel.Variant{{
		ID: exportID(40), Tasks: []exerciseModel.Task{{ID: exportID(41), Name: "t", Attachments: []exerciseModel.AttachmentRef{{FileID: fileNameless}}}},
	}}}}
	data := exportVersions(t, versions, media)
	var doc struct {
		Versions []struct {
			Variants []struct {
				Tasks []struct {
					Attachments []map[string]string `json:"attachments"`
				} `json:"tasks"`
			} `json:"variants"`
		} `json:"versions"`
	}
	require.NoError(t, json.Unmarshal(exportedDocument(t, data), &doc))
	require.Equal(t, []map[string]string{{"file": "files/v1/variant-1/1/_exercise.yaml", "name": "exercise.yaml"}},
		doc.Versions[0].Variants[0].Tasks[0].Attachments)
	parsed, err := exercise.ParseExerciseArchive(data, "", exercise.DefaultLimits())
	require.NoError(t, err)
	ref := parsed.Versions[0].Variants[0].Tasks[0].Attachments[0]
	require.Equal(t, "exercise.yaml", ref.Name)
	require.Equal(t, []byte("yaml bytes"), parsed.Files[parsed.FilePaths[ref.FileID]])
}

// Export, re-import, export again: the same document and files, _exercise.json at the same path with the
// shown name exercise.json.
func TestExportImportExport_Idempotent(t *testing.T) {
	first := exportVersions(t, richVersions(), richMedia())
	parsed, err := exercise.ParseExerciseArchive(first, "", exercise.DefaultLimits())
	require.NoError(t, err)
	exercise.NormalizeContentIDs(parsed.Versions[0].Variants)
	media := &exportMedia{fakeMedia: newFakeMedia(), files: map[uuid.UUID]storedFile{}}
	for id, filePath := range parsed.FilePaths {
		media.files[id] = storedFile{name: parsed.FileMeta[id].Name, body: parsed.Files[filePath]}
	}
	second := exportVersions(t, parsed.Versions, media)

	firstFiles, err := exercise.ReadArchiveV1(bytes.NewReader(first), exercise.DefaultLimits())
	require.NoError(t, err)
	secondFiles, err := exercise.ReadArchiveV1(bytes.NewReader(second), exercise.DefaultLimits())
	require.NoError(t, err)
	require.Equal(t, firstFiles, secondFiles)
	require.Contains(t, secondFiles, "files/v1/variant-1/1/_exercise.json")
	require.Contains(t, string(secondFiles["exercise.json"]), `"file": "files/v1/variant-1/1/_exercise.json",
                  "name": "exercise.json"`)
}
