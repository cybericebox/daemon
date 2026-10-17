package exercise_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

var (
	linkedBaseID   = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000b001")
	linkedOwnID    = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000b002")
	linkedTaskOne  = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000b011")
	linkedTaskTwo  = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000b012")
	linkedTaskElse = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000b0ff")
)

// linkedArchive wraps one version (status, variants JSON) and extra files into a zip.
func linkedArchive(t *testing.T, status, variants string, files map[string]string) []byte {
	t.Helper()
	contents := map[string][]byte{"exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Пов'язані"},` +
		`"versions":[{"status":"` + status + `","variants":` + variants + `}]}`)}
	for name, body := range files {
		contents[name] = []byte(body)
	}
	archive, err := exercise.BuildArchiveV1(contents)
	require.NoError(t, err)
	return archive
}

// fullBase is a complete base variant with two tasks.
const fullBase = `{"id":"0192f0a0-0000-7000-8000-00000000b001","note":"основний","tasks":[
	{"id":"0192f0a0-0000-7000-8000-00000000b011","name":"Перше","difficulty":"easy","flag":["ICE{base-1}"],
	 "description":"Знайди мережу {{net}}","placeholders":[{"key":"net","kind":"vpn.subnet"}],
	 "hints":[{"text":"підказка","level":"steps"}],"linked_device":"web","device_flag_var":"FLAG",
	 "attachments":[{"file":"files/base.txt"}]},
	{"id":"0192f0a0-0000-7000-8000-00000000b012","name":"Друге","difficulty":"hard","flag":["ICE{base-2}"]}],
	"topology":{"vpn":{"enabled":true},"devices":[{"name":"web","type":"container","image":"nginx:1.27"}]}}`

func errorContext(t *testing.T, err error) map[string]any {
	t.Helper()
	var withContext interface{ PublicContext() map[string]any }
	require.True(t, errors.As(err, &withContext), "error %v has no public context", err)
	return withContext.PublicContext()
}

func TestToParsed_LinkedVariantKeepsOnlyOwnParts(t *testing.T) {
	archive := linkedArchive(t, "draft", `[`+fullBase+`,
		{"linked_to":1,"id":"0192f0a0-0000-7000-8000-00000000b002","index":2,"note":"згенеровано",
		 "tasks":[{"id":"0192f0a0-0000-7000-8000-00000000b011","flag":["ICE{own-1}"],"attachments":[{"file":"files/own.txt","name":"мій.txt"}]},
		          {"flag":["ICE{own-2}"]}]},
		{"linked_to":1}]`, map[string]string{"files/base.txt": "base", "files/own.txt": "own"})
	parsed, err := exercise.ParseExerciseArchive(archive, "")
	require.NoError(t, err)
	variants := parsed.Versions[0].Variants
	require.Len(t, variants, 3)

	linked := variants[1]
	require.Equal(t, linkedOwnID, linked.ID)
	require.Equal(t, int32(2), linked.Index)
	require.Equal(t, "згенеровано", linked.Note)
	require.Equal(t, uuid.NullUUID{UUID: linkedBaseID, Valid: true}, linked.LinkedTo)
	require.Equal(t, exerciseModel.Topology{}, linked.Topology)
	require.Len(t, linked.Tasks, 2)
	first := linked.Tasks[0]
	require.Equal(t, linkedTaskOne, first.ID)
	require.Equal(t, []string{"ICE{own-1}"}, first.Flag)
	require.Len(t, first.Attachments, 1)
	require.Equal(t, "мій.txt", first.Attachments[0].Name)
	require.Equal(t, "files/own.txt", parsed.FilePaths[first.Attachments[0].FileID])
	first.Flag, first.Attachments, first.ID = nil, nil, uuid.Nil
	require.Equal(t, exerciseModel.Task{}, first, "only the own parts are mapped")
	require.Equal(t, exerciseModel.Task{Flag: []string{"ICE{own-2}"}}, linked.Tasks[1])

	// Absent tasks: one empty task per base task; an id-less variant gets a fresh id.
	bare := variants[2]
	require.NotEqual(t, uuid.Nil, bare.ID)
	require.Equal(t, uuid.NullUUID{UUID: linkedBaseID, Valid: true}, bare.LinkedTo)
	require.Equal(t, []exerciseModel.Task{{}, {}}, bare.Tasks)
}

// An id-less base is linked by the id the import gives it.
func TestToParsed_LinkedToIdlessBase(t *testing.T) {
	archive := linkedArchive(t, "draft", `[{"linked_to":2},{"tasks":[{"name":"t"}]}]`, nil)
	parsed, err := exercise.ParseExerciseArchive(archive, "")
	require.NoError(t, err)
	variants := parsed.Versions[0].Variants
	require.NotEqual(t, uuid.Nil, variants[1].ID)
	require.Equal(t, uuid.NullUUID{UUID: variants[1].ID, Valid: true}, variants[0].LinkedTo)
}

func TestDecodeArchiveV2_LinkedVariantForbiddenField(t *testing.T) {
	taskValues := map[string]string{
		"name":             `"x"`,
		"description":      `"x"`,
		"difficulty":       `"easy"`,
		"hints":            `[{"text":"h"}]`,
		"placeholders":     `[]`,
		"linked_device":    `"web"`,
		"linked_device_id": `"0192f0a0-0000-7000-8000-00000000b0ff"`,
		"device_flag_var":  `"FLAG"`,
		"unknown":          `1`,
	}
	cases := map[string]struct{ variant, field string }{
		"topology":        {`{"linked_to":1,"topology":{}}`, "versions[1].variants[2].topology"},
		"variant unknown": {`{"linked_to":1,"label":"x"}`, "versions[1].variants[2].label"},
	}
	for key, value := range taskValues {
		cases["task "+key] = struct{ variant, field string }{
			`{"linked_to":1,"tasks":[{"flag":["ICE{x}"],"` + key + `":` + value + `},{}]}`,
			"versions[1].variants[2].tasks[1]." + key,
		}
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			archive := linkedArchive(t, "draft", `[`+fullBase+`,`+tc.variant+`]`, map[string]string{"files/base.txt": "base"})
			_, err := exercise.ParseExerciseArchive(archive, "")
			require.ErrorIs(t, err, exerciseModel.ErrArchiveLinkedVariantField.Err())
			require.Equal(t, tc.field, errorContext(t, err)["field"])
		})
	}
}

func TestToParsed_LinkedToInvalid(t *testing.T) {
	cases := map[string]struct {
		variants string
		variant  string
		linkedTo int
	}{
		"zero":         {`[{"tasks":[{"name":"t"}]},{"linked_to":0}]`, "versions[1].variants[2]", 0},
		"negative":     {`[{"tasks":[{"name":"t"}]},{"linked_to":-1}]`, "versions[1].variants[2]", -1},
		"out of range": {`[{"tasks":[{"name":"t"}]},{"linked_to":3}]`, "versions[1].variants[2]", 3},
		"self":         {`[{"tasks":[{"name":"t"}]},{"linked_to":2}]`, "versions[1].variants[2]", 2},
		"chain":        {`[{"tasks":[{"name":"t"}]},{"linked_to":1},{"linked_to":2}]`, "versions[1].variants[3]", 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := exercise.ParseExerciseArchive(linkedArchive(t, "draft", tc.variants, nil), "")
			require.ErrorIs(t, err, exerciseModel.ErrArchiveLinkedToInvalid.Err())
			ctx := errorContext(t, err)
			require.Equal(t, tc.variant, ctx["variant"])
			require.EqualValues(t, tc.linkedTo, ctx["linked_to"])
		})
	}
}

func TestToParsed_LinkedTaskCountMismatch(t *testing.T) {
	files := map[string]string{"files/base.txt": "base"}
	cases := map[string]struct{ linked, field string }{
		"fewer tasks":  {`{"linked_to":1,"tasks":[{"flag":["ICE{x}"]}]}`, "versions[1].variants[2].tasks"},
		"more tasks":   {`{"linked_to":1,"tasks":[{},{},{}]}`, "versions[1].variants[2].tasks"},
		"empty list":   {`{"linked_to":1,"tasks":[]}`, "versions[1].variants[2].tasks"},
		"other task":   {`{"linked_to":1,"tasks":[{},{"id":"` + linkedTaskElse.String() + `"}]}`, "versions[1].variants[2].tasks[2].id"},
		"swapped task": {`{"linked_to":1,"tasks":[{"id":"` + linkedTaskTwo.String() + `"},{}]}`, "versions[1].variants[2].tasks[1].id"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := exercise.ParseExerciseArchive(linkedArchive(t, "draft", `[`+fullBase+`,`+tc.linked+`]`, files), "")
			require.ErrorIs(t, err, exerciseModel.ErrArchiveLinkedVariantField.Err())
			require.Equal(t, tc.field, errorContext(t, err)["field"])
		})
	}
}

// expectImport mocks the writes of a one-version import and hands the written variants to check.
func expectImport(q *postgresMocks.MockQuerier, check func([]exerciseModel.Variant)) {
	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			return postgres.Exercise{ID: arg.ID, Name: arg.Name, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		})
	q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
			var variants []exerciseModel.Variant
			if err := json.Unmarshal(arg.Variants, &variants); err != nil {
				return postgres.ExerciseVersion{}, err
			}
			check(variants)
			return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, Variants: arg.Variants, CreatedAt: arg.CreatedAt}, nil
		})
	q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).Return(nil)
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func TestImportExerciseArchive_LinkedVariantInheritsBase(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	archive := linkedArchive(t, "draft", `[`+fullBase+`,
		{"linked_to":1,"note":"згенеровано","tasks":[{"flag":["ICE{own}"],"attachments":[{"file":"files/own.txt"}]},{}]}]`,
		map[string]string{"files/base.txt": "base", "files/own.txt": "own"})
	media := newFakeMedia()
	var written []exerciseModel.Variant
	expectImport(q, func(variants []exerciseModel.Variant) { written = variants })
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: media})

	_, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
	require.NoError(t, err)
	require.Len(t, written, 2)
	base, linked := written[0], written[1]
	require.Equal(t, uuid.NullUUID{UUID: base.ID, Valid: true}, linked.LinkedTo)
	require.Equal(t, "згенеровано", linked.Note)
	require.JSONEq(t, mustJSON(t, base.Topology), mustJSON(t, linked.Topology))
	require.Len(t, linked.Tasks, 2)
	for i := range base.Tasks {
		own, inherited := linked.Tasks[i], base.Tasks[i]
		require.Equal(t, inherited.ID, own.ID)
		require.Equal(t, inherited.Name, own.Name)
		require.Equal(t, inherited.Difficulty, own.Difficulty)
		require.Equal(t, inherited.DeviceFlagVar, own.DeviceFlagVar)
		require.Equal(t, inherited.LinkedDeviceID, own.LinkedDeviceID)
		if len(inherited.Description) == 0 {
			require.Empty(t, own.Description)
		} else {
			require.JSONEq(t, string(inherited.Description), string(own.Description))
		}
		require.JSONEq(t, mustJSON(t, inherited.Hints), mustJSON(t, own.Hints))
		require.JSONEq(t, mustJSON(t, inherited.Placeholders), mustJSON(t, own.Placeholders))
	}
	require.Equal(t, []string{"ICE{own}"}, linked.Tasks[0].Flag)
	require.Nil(t, linked.Tasks[1].Flag)
	require.Empty(t, linked.Tasks[1].Attachments)
	require.Len(t, linked.Tasks[0].Attachments, 1)
	uploaded := map[uuid.UUID]string{}
	for _, upload := range media.uploads {
		uploaded[upload.ID] = string(upload.Body)
	}
	require.Equal(t, "own", uploaded[linked.Tasks[0].Attachments[0].FileID])
	require.Equal(t, "base", uploaded[base.Tasks[0].Attachments[0].FileID])
}

func TestImportExerciseArchive_LinkedSameFlagRejected(t *testing.T) {
	variants := `[{"tasks":[{"name":"Тест","difficulty":"easy","flag":["ICE{same}"],
		"description":{"root":{"type":"text","text":"Опис"}}}]},
		{"linked_to":1,"tasks":[{"flag":["ICE{same}"]}]}]`
	t.Run("published", func(t *testing.T) {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
		_, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: linkedArchive(t, "published", variants, nil)})
		require.ErrorIs(t, err, exerciseModel.ErrLinkedFlagsNotDistinct.Err())
	})
	t.Run("draft", func(t *testing.T) {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		expectImport(q, func(written []exerciseModel.Variant) {
			require.Equal(t, uuid.NullUUID{UUID: written[0].ID, Valid: true}, written[1].LinkedTo)
		})
		uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
		_, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: linkedArchive(t, "draft", variants, nil)})
		require.NoError(t, err)
	})
}

// linkedExportVersions: version 1's variant 2 is linked to variant 1; version 2's single variant links to a
// variant that is not in its version.
func linkedExportVersions() []exerciseModel.ExerciseVersion {
	versions := exportFixtureVersions()
	versions[0].Variants[1].LinkedTo = uuid.NullUUID{UUID: versions[0].Variants[0].ID, Valid: true}
	versions[0].Variants[1].Note = "згенеровано"
	versions[1].Variants[0].LinkedTo = uuid.NullUUID{UUID: versions[0].Variants[0].ID, Valid: true}
	return versions
}

func exportLinked(t *testing.T) []byte {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	expectExport(t, q, exportExerciseID, "Експорт", linkedExportVersions())
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: exportFixtureMedia()})
	data, err := uc.ExportExerciseArchive(context.Background(), exportExerciseID, exercise.ExportOptions{})
	require.NoError(t, err)
	return data
}

func TestExportExerciseArchive_LinkedVariantCompact(t *testing.T) {
	var doc struct {
		Versions []struct {
			Variants []json.RawMessage `json:"variants"`
		} `json:"versions"`
	}
	require.NoError(t, json.Unmarshal(exportedDocument(t, exportLinked(t)), &doc))
	linked := doc.Versions[0].Variants[1]
	require.Equal(t, []string{"id", "index", "note", "linked_to", "tasks"}, objectKeys(t, linked))
	require.JSONEq(t, fmt.Sprintf(`{"id":%q,"index":2,"note":"згенеровано","linked_to":1,"tasks":[
		{"id":%q,"flag":["ICE{one}"],"attachments":[{"file":"files/v1/shared/common.bin"},{"file":"files/v1/variant-2/shared/copy1.txt"}]},
		{"id":%q,"flag":["ICE{two}"],"attachments":[{"file":"files/v1/variant-2/shared/copy1.txt","name":"copy2.txt"}]}]}`,
		exportID(2), exportID(10), exportID(11)), string(linked))

	// The base stays whole; a link to a variant outside the version is written as a full variant without it.
	require.Contains(t, objectKeys(t, doc.Versions[0].Variants[0]), "topology")
	dangling := objectKeys(t, doc.Versions[1].Variants[0])
	require.NotContains(t, dangling, "linked_to")
	require.Contains(t, dangling, "topology")
}

func TestExportThenImport_LinkedRoundTrip(t *testing.T) {
	data := exportLinked(t)
	parsed, err := exercise.ParseExerciseArchive(data, "")
	require.NoError(t, err)
	for i := range parsed.Versions {
		require.NoError(t, parsed.Versions[i].SyncLinkedVariants())
		exercise.NormalizeContentIDs(parsed.Versions[i].Variants)
		require.NoError(t, parsed.Versions[i].SyncLinkedVariants())
	}
	source := linkedExportVersions()
	source[1].Variants[0].LinkedTo = uuid.NullUUID{} // a dangling link is not carried
	media := exportFixtureMedia()
	want := contentIDs(t, source, func(id uuid.UUID) []byte { return media.files[id].body })
	got := contentIDs(t, parsed.Versions, func(id uuid.UUID) []byte { return parsed.Files[parsed.FilePaths[id]] })
	require.JSONEq(t, string(want), string(got))
}

// Two variants sharing an id would let linked_to inherit from the wrong one.
func TestToParsed_RepeatedVariantIDRejected(t *testing.T) {
	variants := `[{"id":"` + linkedBaseID.String() + `","tasks":[{"name":"a"}]},
		{"id":"` + linkedBaseID.String() + `","tasks":[{"name":"b"}]},
		{"linked_to":2}]`
	_, err := exercise.ParseExerciseArchive(linkedArchive(t, "draft", variants, nil), "")
	require.ErrorIs(t, err, exerciseModel.ErrArchiveInvalid.Err())
	require.Contains(t, errorContext(t, err)["reason"], "versions[1].variants[2].id")
}

// An invalid task id of the base is reported at the base, not as a mismatch of the linked task.
func TestToParsed_LinkedTaskIDAgainstInvalidBaseID(t *testing.T) {
	variants := `[{"linked_to":2,"tasks":[{"id":"` + linkedTaskOne.String() + `"}]},{"tasks":[{"id":"nope","name":"t"}]}]`
	_, err := exercise.ParseExerciseArchive(linkedArchive(t, "draft", variants, nil), "")
	require.ErrorIs(t, err, exerciseModel.ErrArchiveInvalid.Err())
	require.Contains(t, errorContext(t, err)["reason"], "versions[1].variants[2].tasks[1].id")
}

// The ids and placeholder keys the import gives the base reach its linked variant (the second sync).
func TestImportExerciseArchive_LinkedGetsBaseGeneratedIDs(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	variants := `[{"linked_to":2,"tasks":[{"flag":["ICE{own}"]}]},
		{"tasks":[{"name":"Тест","difficulty":"easy","flag":["ICE{base}"],"placeholders":[{"kind":"vpn.subnet"}],
		  "hints":[{"text":"підказка"}]}]}]`
	var written []exerciseModel.Variant
	expectImport(q, func(variants []exerciseModel.Variant) { written = variants })
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	_, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: linkedArchive(t, "draft", variants, nil)})
	require.NoError(t, err)
	linked, base := written[0], written[1]
	require.Equal(t, uuid.NullUUID{UUID: base.ID, Valid: true}, linked.LinkedTo)
	require.NotEmpty(t, base.Tasks[0].Placeholders[0].Key)
	require.Equal(t, base.Tasks[0].Placeholders[0].Key, linked.Tasks[0].Placeholders[0].Key)
	require.NotEqual(t, uuid.Nil, base.Tasks[0].ID)
	require.Equal(t, base.Tasks[0].ID, linked.Tasks[0].ID)
	require.Equal(t, base.Tasks[0].Hints[0].ID, linked.Tasks[0].Hints[0].ID)
	require.Equal(t, []string{"ICE{own}"}, linked.Tasks[0].Flag)
}
