package exercise

import (
	"encoding/json"
	"testing"
	"time"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/gofrs/uuid"
)

func TestDeployDTOs_CarryKind(t *testing.T) {
	t1 := uuid.Must(uuid.NewV4())
	resp := deployHandleToResponse(exerciseModel.DeployHandle{
		Group: "g", Static: true,
		Flags: []exerciseModel.DeployFlag{{TaskID: t1, Name: "A", Flag: "secret"}},
	})
	if resp.Kind != "static" {
		t.Fatalf("Kind = %q, want static", resp.Kind)
	}
	if resp.Lab != "" {
		t.Fatalf("Lab = %q, want empty", resp.Lab)
	}
	if len(resp.Tasks) != 1 || resp.Tasks[0].TaskID != t1.String() || resp.Tasks[0].Name != "A" {
		t.Fatalf("Tasks = %+v", resp.Tasks)
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || contains(string(raw), "secret") {
		t.Fatalf("flag leaked: %s", raw)
	}
	if !contains(string(raw), `"Kind":"static"`) {
		t.Fatalf("no Kind in JSON: %s", raw)
	}

	if k := deployHandleToResponse(exerciseModel.DeployHandle{Group: "g", Lab: "l"}).Kind; k != "lab" {
		t.Fatalf("lab handle Kind = %q", k)
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lab := exerciseModel.TestDeploy{GroupName: "g", LabName: "l", CreatedAt: now}
	if k := deployListItemOf(lab).Kind; k != "lab" {
		t.Fatalf("lab row Kind = %q", k)
	}
	static := exerciseModel.TestDeploy{CreatedAt: now}
	if k := deployListItemOf(static).Kind; k != "static" {
		t.Fatalf("static row Kind = %q", k)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
