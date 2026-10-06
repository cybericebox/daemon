package inAppModel_test

import (
	"encoding/json"
	"testing"

	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
)

// L21: Link and Actions were stored as typed; a manager's template could carry javascript:.
func TestValidLink(t *testing.T) {
	for link, want := range map[string]bool{
		"":                            true,
		"/profile":                    true,
		"/events/1?tab=team":          true,
		"https://ctf.example.test/x":  true,
		"HTTPS://ctf.example.test/x":  true,
		"javascript:alert(1)":         false,
		"JaVaScRiPt:alert(1)":         false,
		"data:text/html,<script>":     false,
		"vbscript:x":                  false,
		"http://ctf.example.test/x":   false,
		"//evil.test/x":               false,
		"/\\evil.test":                false,
		"evil.test/x":                 false,
		"https://a b":                 false,
		"{{event_url}}":               false, // only a template may start with a variable
		"{{event_url}}/participation": false,
	} {
		if got := inAppModel.ValidLink(link, false); got != want {
			t.Errorf("rendered %q: got %v, want %v", link, got, want)
		}
	}
	if !inAppModel.ValidLink("{{event_url}}/participation", true) {
		t.Error("a template may start with a variable")
	}
	if inAppModel.ValidLink("javascript:{{x}}", true) {
		t.Error("a template may not start with javascript:")
	}
}

func TestActions(t *testing.T) {
	good := json.RawMessage(`[{"label":"Open","href":"https://ctf.example.test/x"},{"label":"Go","href":"/profile"}]`)
	bad := json.RawMessage(`[{"label":"Open","href":"https://ctf.example.test/x"},{"label":"Evil","href":"javascript:alert(1)"}]`)
	if !inAppModel.ValidActions(good, true) || inAppModel.ValidActions(bad, true) {
		t.Fatal("ValidActions")
	}
	if !inAppModel.ValidActions(nil, true) || !inAppModel.ValidActions(json.RawMessage(`null`), true) {
		t.Fatal("no actions is valid")
	}
	if inAppModel.ValidActions(json.RawMessage(`{"not":"a list"}`), true) {
		t.Fatal("a non-list is invalid")
	}
	kept := inAppModel.SafeActions(bad)
	var list []map[string]any
	if err := json.Unmarshal(kept, &list); err != nil || len(list) != 1 || list[0]["label"] != "Open" {
		t.Fatalf("SafeActions kept %s", kept)
	}
	if got := inAppModel.SafeActions(json.RawMessage(`garbage`)); got != nil {
		t.Fatalf("an unreadable document yields none, got %s", got)
	}
}
