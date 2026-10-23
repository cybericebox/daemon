package exercise_test

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/apidocs"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/pagination"
)

func TestCommitImportRequestAndResponseSchemasMatchActualPascalCaseKeys(t *testing.T) {
	raw, err := os.ReadFile("../apidocs/swagger.json")
	require.NoError(t, err)
	var doc, embedded, yamlDoc importContractDocument
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.NoError(t, json.Unmarshal([]byte(apidocs.SwaggerInfo.ReadDoc()), &embedded))
	raw, err = os.ReadFile("../apidocs/swagger.yaml")
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, &yamlDoc))
	source, target := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	body := `{"UploadID":"` + source.String() + `","Password":"private","OwnerEventID":null,"Targets":[{"ItemKey":".","Mode":"update","ExerciseID":"` + target.String() + `"}],"FixedExerciseID":"` + target.String() + `","ConfirmedUpdates":[{"ItemKey":".","Revision":"source-target-revision","ReplaceDraft":true}]}`
	uc := &fakeUC{archiveImport: archiveImportCalls{start: exerciseUseCase.ArchiveImportStart{Exercises: []exerciseUseCase.ExerciseView{}, Results: []exerciseModel.ImportResult{{ItemKey: ".", Mode: "update", ExerciseID: target}}, Created: 0, Updated: 1}}}
	w := jsonRequest(newEngine(uc, jobCaller), http.MethodPost, "/api/exercises/import/upload", body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var input any
	require.NoError(t, json.Unmarshal([]byte(body), &input))
	var output struct{ Data any }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &output))
	visited := map[string]bool{}
	assertImportContractKeys(t, doc.Definitions, importContractSchema{Ref: "#/definitions/exercise.importUploadRequest"}, input, "commit request", visited)
	assertImportContractKeys(t, doc.Definitions, importContractSchema{Ref: "#/definitions/exercise.importCommitResponse"}, output.Data, "commit response", visited)
	for _, name := range []string{"exercise.importUploadRequest", "exercise.ImportTarget", "exercise.ConfirmedImportUpdate", "exercise.importCommitResponse", "exerciseModel.ImportResult"} {
		require.Contains(t, doc.Definitions, name)
		require.Equal(t, doc.Definitions[name], embedded.Definitions[name])
		require.Equal(t, doc.Definitions[name], yamlDoc.Definitions[name])
	}
	require.NotContains(t, w.Body.String(), "private")
}

type importContractSchema struct {
	Ref         string                          `json:"$ref" yaml:"$ref"`
	Type        string                          `json:"type" yaml:"type"`
	Description string                          `json:"description" yaml:"description"`
	Properties  map[string]importContractSchema `json:"properties" yaml:"properties"`
	Items       *importContractSchema           `json:"items" yaml:"items"`
}

type importContractDocument struct {
	Definitions map[string]importContractSchema `json:"definitions" yaml:"definitions"`
}

// Compare actual HTTP response objects to the generated schema recursively,
// rather than repeating a second handwritten list of expected field names.
// Traverse all new public DTOs and compare the JSON/YAML/embedded Go artifacts.
func TestReimportPublicSchemasMatchRuntimeKeysAndAllGeneratedArtifacts(t *testing.T) {
	data, err := os.ReadFile("../apidocs/swagger.json")
	require.NoError(t, err)
	var document, embedded, yamlDocument importContractDocument
	require.NoError(t, json.Unmarshal(data, &document))
	require.NoError(t, json.Unmarshal([]byte(apidocs.SwaggerInfo.ReadDoc()), &embedded))
	data, err = os.ReadFile("../apidocs/swagger.yaml")
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(data, &yamlDocument))

	id, event, source := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	target := exerciseUseCase.ImportTarget{ItemKey: "folder", Mode: "update", ExerciseID: id}
	context := exerciseUseCase.ImportTargetExercise{ID: id, Name: "Target name", Scope: "event", OwnerEventID: &event, OwnerEventName: "Owner event", OwnerEventTag: "owner", HasDraft: true}
	uc := &fakeUC{reimport: previewCalls{
		view: exerciseUseCase.ArchiveImportPreview{Items: []exerciseUseCase.ArchiveImportPreviewItem{{
			ItemKey: "folder", SourceExerciseID: &source, Name: "Source name", SourceVersion: exerciseUseCase.ImportSourceVersion{Position: 0, Status: "draft"},
			SuggestedTarget: target, Target: target, TargetExercise: &context, SourceIDMismatch: true, ConfirmationRevision: "bound-revision", ReplacesDraft: true,
			Summary: exerciseUseCase.ArchiveImportSummary{Variants: exerciseUseCase.ImportCountChange{Added: 1, Removed: 2, Changed: 3}, Subtasks: exerciseUseCase.ImportSubtaskCount{Before: 1, After: 2}, FlagsChanged: 3, Files: exerciseUseCase.ImportFileChange{Added: 1, Removed: 2}, Images: exerciseUseCase.ImportImageChange{New: 1, Reused: 2}, MetadataChanged: true},
		}}},
		targets: pagination.NewCursorPage([]exerciseUseCase.ImportTargetExercise{context}, false, uuid.Nil, 1),
	}}
	engine := newEngine(uc, jobCaller)
	preview := jsonRequest(engine, http.MethodPost, "/api/exercises/import/preview", `{"UploadID":"`+source.String()+`"}`)
	require.Equal(t, http.StatusOK, preview.Code, preview.Body.String())
	targets := jsonRequest(engine, http.MethodGet, "/api/exercises/import/targets", "")
	require.Equal(t, http.StatusOK, targets.Code, targets.Body.String())
	var previewResponse, targetResponse struct{ Data any }
	require.NoError(t, json.Unmarshal(preview.Body.Bytes(), &previewResponse))
	require.NoError(t, json.Unmarshal(targets.Body.Bytes(), &targetResponse))
	visited := map[string]bool{}
	assertImportContractKeys(t, document.Definitions, importContractSchema{Ref: "#/definitions/exercise.ArchiveImportPreview"}, previewResponse.Data, "preview", visited)
	targetItems := targetResponse.Data.(map[string]any)["Items"].([]any)
	require.Len(t, targetItems, 1)
	assertImportContractKeys(t, document.Definitions, importContractSchema{Ref: "#/definitions/exercise.ImportTargetExercise"}, targetItems[0], "target", visited)
	require.Len(t, visited, 10, "every new reachable response DTO must be exercised, including nested counts and target choices")
	for name := range visited {
		require.Equal(t, document.Definitions[name], embedded.Definitions[name], name+": docs.go schema drift")
		require.Equal(t, document.Definitions[name], yamlDocument.Definitions[name], name+": YAML schema drift")
	}
	require.Contains(t, document.Definitions["exercise.ImportSourceVersion"].Properties["Position"].Description, "-1 when the archive has no versions")
}

func assertImportContractKeys(t *testing.T, definitions map[string]importContractSchema, schema importContractSchema, value any, path string, visited map[string]bool) {
	t.Helper()
	if schema.Ref != "" {
		name := strings.TrimPrefix(schema.Ref, "#/definitions/")
		var ok bool
		schema, ok = definitions[name]
		require.True(t, ok, path+": missing referenced schema "+name)
		if strings.HasPrefix(name, "exercise.") {
			visited[name] = true
		}
	}
	if schema.Type == "array" {
		items, ok := value.([]any)
		require.True(t, ok, path+": runtime array")
		require.NotNil(t, schema.Items, path+": schema array items")
		for _, item := range items {
			assertImportContractKeys(t, definitions, *schema.Items, item, path+"[]", visited)
		}
		return
	}
	if schema.Type != "object" {
		return
	}
	object, ok := value.(map[string]any)
	require.True(t, ok, path+": runtime object")
	runtimeKeys, schemaKeys := make([]string, 0, len(object)), make([]string, 0, len(schema.Properties))
	for key := range object {
		runtimeKeys = append(runtimeKeys, key)
	}
	for key := range schema.Properties {
		schemaKeys = append(schemaKeys, key)
	}
	slices.Sort(runtimeKeys)
	slices.Sort(schemaKeys)
	require.Equal(t, runtimeKeys, schemaKeys, path+": generated keys must match the actual HTTP object exactly")
	for key, property := range schema.Properties {
		assertImportContractKeys(t, definitions, property, object[key], path+"."+key, visited)
	}
}

func TestReimportPublicTagsPreserveZeroAndNullFieldPresence(t *testing.T) {
	encoded, err := json.Marshal(exerciseUseCase.ArchiveImportPreviewItem{})
	require.NoError(t, err)
	var item map[string]any
	require.NoError(t, json.Unmarshal(encoded, &item))
	require.Contains(t, item, "SourceExerciseID")
	require.Nil(t, item["SourceExerciseID"])
	require.Contains(t, item, "TargetExercise")
	require.Nil(t, item["TargetExercise"])
	require.Contains(t, item, "ReplacesDraft")
	require.Equal(t, false, item["ReplacesDraft"])
	require.Equal(t, map[string]any{"ItemKey": "", "Mode": ""}, item["Target"], "only zero ExerciseID is omitted; empty controls remain present")
	summary := item["Summary"].(map[string]any)
	require.Contains(t, summary, "MetadataChanged")
	require.Equal(t, false, summary["MetadataChanged"])
	require.Equal(t, map[string]any{"Added": float64(0), "Removed": float64(0), "Changed": float64(0)}, summary["Variants"])
	encoded, err = json.Marshal(exerciseUseCase.ArchiveImportPreview{})
	require.NoError(t, err)
	require.JSONEq(t, `{"Items":null}`, string(encoded), "adding tags must not turn the preexisting nil slice into an empty array or omit it")
	encoded, err = json.Marshal(exerciseUseCase.ImportTargetExercise{})
	require.NoError(t, err)
	var targetContext map[string]any
	require.NoError(t, json.Unmarshal(encoded, &targetContext))
	require.Contains(t, targetContext, "OwnerEventID")
	require.Nil(t, targetContext["OwnerEventID"], "the nullable ownership context remains present")

}
