package event

import (
	"encoding/json"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"os"
	"strings"
	"testing"
)

func TestLabPolicySwaggerMatchesPascalCaseResponse(t *testing.T) {
	raw, err := json.Marshal(toConfigResponse(eventUseCase.EventConfigView{LabPolicy: eventLabModel.DefaultPolicy()}))
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err = json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	policy := response["LabPolicy"].(map[string]any)
	source, err := os.ReadFile("../apidocs/swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err = json.Unmarshal(source, &spec); err != nil {
		t.Fatal(err)
	}
	definitions := spec["definitions"].(map[string]any)
	config := definitions["event.configResponse"].(map[string]any)["properties"].(map[string]any)
	ref := config["LabPolicy"].(map[string]any)["$ref"].(string)
	properties := definitions[strings.TrimPrefix(ref, "#/definitions/")].(map[string]any)["properties"].(map[string]any)
	for key := range policy {
		if _, ok := properties[key]; !ok {
			t.Errorf("Swagger policy misses actual response field %q", key)
		}
	}
}
