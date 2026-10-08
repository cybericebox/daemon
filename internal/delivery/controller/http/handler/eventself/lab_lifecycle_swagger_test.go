package eventself

import (
	"encoding/json"
	"os"
	"testing"
)

func TestGeneratedSwaggerMatchesFrozenLabPascalCaseAndNullability(t *testing.T) {
	raw, err := os.ReadFile("../apidocs/swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Definitions map[string]struct{ Properties map[string]map[string]any }
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"eventself.ownChallengeResponse", "eventself.submitChallengeResponse"} {
		if doc.Definitions[name].Properties["Lab"]["x-nullable"] != true {
			t.Errorf("nullable outer Lab missing in %s", name)
		}
	}
	wire, err := json.Marshal(labLinkResponse{})
	if err != nil {
		t.Fatal(err)
	}
	var link map[string]json.RawMessage
	if err = json.Unmarshal(wire, &link); err != nil {
		t.Fatal(err)
	}
	props := doc.Definitions["eventself.labLinkResponse"].Properties
	if len(props) != len(link) {
		t.Errorf("link schema fields %v disagree with wire %s", props, wire)
	}
	for key := range link {
		if props[key] == nil {
			t.Errorf("link wire %s missing from schema %v", key, props)
		}
	}
	if props["LabID"]["type"] != "string" || props["Revision"]["type"] != "string" {
		t.Errorf("link fence must use PascalCase string fields: %v", props)
	}
	p := doc.Definitions["labview.ParticipantLabResponse"].Properties
	for _, key := range []string{"ID", "EventExerciseID", "Revision", "LogicalClosed", "CloseReason", "ClosedAt", "RuntimeState", "CanStop", "CanRestart", "SnapshotPolicy", "RetentionUntil"} {
		if p[key] == nil {
			t.Fatalf("missing PascalCase %s in %v", key, p)
		}
	}
	for _, key := range []string{"CloseReason", "ClosedAt", "RetentionUntil"} {
		if p[key]["x-nullable"] != true {
			t.Fatalf("missing nullable %s: %v", key, p[key])
		}
	}
	if p["Revision"]["type"] != "string" {
		t.Fatal(p["Revision"])
	}
	for _, key := range []string{"CPUMillicores", "MemoryBytes"} {
		if doc.Definitions["event.ComputeView"].Properties[key]["type"] != "string" {
			t.Fatalf("quantity %s", key)
		}
	}
}
