package labMonitoring

import (
	"encoding/json"
	"strings"
	"testing"
)

func decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

func names(t *testing.T, state map[string]any, collection string) []string {
	t.Helper()
	list, _ := state[collection].([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, item.(map[string]any)["name"].(string))
	}
	return out
}

func TestMergeDeltaUpdatesOnlyReportedResources(t *testing.T) {
	snapshot := json.RawMessage(`{"groups":[{"name":"g","status":{"phase":"Ready"}}],"labs":[{"namespace":"ns","name":"web","status":{"phase":"Pending"}},{"namespace":"ns","name":"db","status":{"phase":"Ready"}}]}`)
	delta := json.RawMessage(`{"labs":[{"namespace":"ns","name":"web","status":{"phase":"Ready"}}]}`)

	merged, err := Merge(snapshot, delta, false)
	if err != nil {
		t.Fatal(err)
	}
	state := decode(t, merged)
	if got := names(t, state, "groups"); len(got) != 1 || got[0] != "g" {
		t.Fatalf("groups lost by a labs-only delta: %v", got)
	}
	labs := state["labs"].([]any)
	if len(labs) != 2 {
		t.Fatalf("labs = %d, want 2 (untouched db kept)", len(labs))
	}
	web := labs[0].(map[string]any)
	if web["name"] != "web" || web["status"].(map[string]any)["phase"] != "Ready" {
		t.Fatalf("web not updated in place: %v", web)
	}
}

func TestMergeSnapshotReplacesState(t *testing.T) {
	current := json.RawMessage(`{"labs":[{"namespace":"ns","name":"old"}]}`)
	snapshot := json.RawMessage(`{"labs":[{"namespace":"ns","name":"new"}]}`)

	merged, err := Merge(current, snapshot, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, decode(t, merged), "labs"); len(got) != 1 || got[0] != "new" {
		t.Fatalf("snapshot did not replace the state: %v", got)
	}
}

func TestMergeAppliesDeletedKeys(t *testing.T) {
	current := json.RawMessage(`{"groups":[{"name":"g"}],"labs":[{"namespace":"ns","name":"a"},{"namespace":"ns","name":"b"}],"clients":[{"namespace":"ns","name":"c"}],"policies":[{"labGroupName":"g","namespace":"ns"}]}`)
	delta := json.RawMessage(`{"deletedKeys":[{"kind":"lab","namespace":"ns","name":"a"},{"kind":"client","namespace":"ns","name":"c"},{"kind":"access_policy","labGroupName":"g","namespace":"ns","name":"access-policy"},{"kind":"unknown","name":"g"}]}`)

	merged, err := Merge(current, delta, false)
	if err != nil {
		t.Fatal(err)
	}
	state := decode(t, merged)
	if got := names(t, state, "labs"); len(got) != 1 || got[0] != "b" {
		t.Fatalf("labs = %v, want only b", got)
	}
	if _, ok := state["clients"]; ok {
		t.Fatalf("deleted client still present: %v", state["clients"])
	}
	if _, ok := state["policies"]; ok {
		t.Fatalf("deleted policy still present: %v", state["policies"])
	}
	if got := names(t, state, "groups"); len(got) != 1 {
		t.Fatalf("unknown deleted kind must not remove anything: %v", got)
	}
	if _, ok := state["deletedKeys"]; ok {
		t.Fatal("deleted keys are transient and must not be stored")
	}
}

func TestMergeStripsSecretFields(t *testing.T) {
	delta := json.RawMessage(`{"labs":[{"namespace":"ns","name":"web","specJson":"c2VjcmV0","env":[{"device":"d","vars":{"FLAG":"x"}}],"status":{"phase":"Ready"}}],"clients":[{"namespace":"ns","name":"c","publicKey":"pk","status":{"assignedIp":"10.0.0.2","config":"[Interface]","statistics":{"rxBytes":"5"}}}]}`)

	merged, err := Merge(nil, delta, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"specJson", "FLAG", "publicKey", "[Interface]", "c2VjcmV0"} {
		if contains(string(merged), secret) {
			t.Fatalf("merged state leaks %q: %s", secret, merged)
		}
	}
	if !contains(string(merged), "10.0.0.2") || !contains(string(merged), "rxBytes") {
		t.Fatalf("approved client facts lost: %s", merged)
	}
}

func TestMergeRejectsBrokenJSON(t *testing.T) {
	if _, err := Merge(json.RawMessage(`{`), json.RawMessage(`{}`), false); err == nil {
		t.Fatal("broken current state must be reported")
	}
	if _, err := Merge(nil, json.RawMessage(`[`), false); err == nil {
		t.Fatal("broken update must be reported")
	}
}

func TestSanitizePayloadFallsBackToEmptyObject(t *testing.T) {
	if got := string(SanitizePayload(json.RawMessage(`not json`))); got != `{}` {
		t.Fatalf("SanitizePayload = %s, want {}", got)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
