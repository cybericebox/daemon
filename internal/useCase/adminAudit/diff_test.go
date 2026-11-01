package adminAudit

import (
	"encoding/json"
	"strings"
	"testing"
)

type sampleSettings struct {
	Days         int
	Host         string
	SMTPPassword string
	APIToken     string
	Nested       struct {
		Limit      int
		PrivateKey string
		Keyword    string
	}
	Tags []string
}

func TestDiffListsOnlyChangedFieldsAndOnlyAllowedValues(t *testing.T) {
	before := sampleSettings{Days: 30, Host: "a"}
	after := sampleSettings{Days: 60, Host: "a"}
	after.Nested.Limit = 5
	got := Diff(before, after, "Days")
	if len(got) != 2 || got[0].Field != "Days" || got[0].Before != float64(30) ||
		got[0].After != float64(60) ||
		got[0].Redacted {
		t.Fatalf("an allowed field carries its values: %+v", got)
	}
	if got[1].Field != "Nested.Limit" || !got[1].Redacted || got[1].After != nil {
		t.Fatalf("a field that is not allowed only says it changed: %+v", got[1])
	}
	if Diff(before, before, "Days") != nil {
		t.Fatal("equal values have no changes")
	}
	if names := Diff(before, after); names[0].Before != nil || names[0].After != nil {
		t.Fatalf("with no allow-list no value is recorded: %+v", names)
	}
}

// A secret never reaches the stored record even when the caller allowed it: not as the old value, not as the new
// one, not under a nested path and not in the JSON that goes to the database.
func TestDiffNeverCarriesASecretEvenWhenAllowed(t *testing.T) {
	before := sampleSettings{SMTPPassword: "old-hunter2", APIToken: "tok-old"}
	after := sampleSettings{SMTPPassword: "new-hunter3", APIToken: "tok-new"}
	before.Nested.PrivateKey, after.Nested.PrivateKey = "-----OLD KEY-----", "-----NEW KEY-----"
	before.Nested.Keyword, after.Nested.Keyword = "alpha", "beta"

	got := Diff(before, after, "SMTPPassword", "APIToken", "Nested.PrivateKey", "Nested.Keyword")
	for _, c := range got {
		secret := c.Field != "Nested.Keyword"
		if secret != c.Redacted || (secret && (c.Before != nil || c.After != nil)) {
			t.Fatalf("%s: %+v", c.Field, c)
		}
	}
	raw, _ := json.Marshal(got)
	for _, leaked := range []string{"hunter", "tok-", "OLD KEY", "NEW KEY"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("%q leaked into %s", leaked, raw)
		}
	}
}

// Arrays are walked, so a secret inside a list of objects and the values of a list that is not allowed stay out.
func TestDiffWalksArraysAndScalarValues(t *testing.T) {
	type entry struct {
		Name     string
		Password string
	}
	got := Diff(
		map[string]any{"Items": []entry{{"a", "old-pw"}}, "Tags": []string{"x"}},
		map[string]any{
			"Items": []entry{{"a", "new-pw"}, {"b", "second-pw"}},
			"Tags":  []string{"y"},
		},
		"Items.Name",
		"Tags",
	)
	raw, _ := json.Marshal(got)
	for _, leaked := range []string{"old-pw", "new-pw", "second-pw"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("%q leaked from inside an array: %s", leaked, raw)
		}
	}
	byField := map[string]Change{}
	for _, c := range got {
		byField[c.Field] = c
	}
	if c := byField["Items[0].Password"]; !c.Redacted {
		t.Fatalf("the secret in the array is reported as changed without a value: %+v", got)
	}
	if c := byField["Items[1].Name"]; c.Redacted || c.After != "b" {
		t.Fatalf("an allowed field inside an array keeps its value: %+v", c)
	}
	if c := byField["Tags[0]"]; c.Redacted || c.After != "y" {
		t.Fatalf("an allowed list keeps its values: %+v", c)
	}

	scalar := Diff(json.RawMessage(`"old-secret-text"`), json.RawMessage(`"new-secret-text"`))
	if len(scalar) != 1 || scalar[0].Field != "Value" || !scalar[0].Redacted ||
		scalar[0].Before != nil ||
		scalar[0].After != nil {
		t.Fatalf("a scalar value that is not allowed is not recorded: %+v", scalar)
	}
}

// Whatever the caller builds by hand is cleaned at the single place that writes: a Change that names a secret
// field loses its values, and a structured value loses the secret keys inside it.
func TestSanitizeStripsSecretsFromHandBuiltChanges(t *testing.T) {
	got := sanitize([]Change{
		{Field: "Password", Before: "a", After: "b"},
		{Field: "Settings.recoveryCode", After: "123456"},
		{Field: "Role", Before: "user", After: "admin"},
		{Field: "Bio", After: strings.Repeat("я", 500)},
		{
			Field: "Form",
			After: map[string]any{
				"name":     "n",
				"apiToken": "tok",
				"rows":     []any{map[string]any{"password": "pw"}},
			},
		},
	})
	if !got[0].Redacted || got[0].Before != nil || got[0].After != nil || !got[1].Redacted ||
		got[1].After != nil {
		t.Fatalf("secrets kept: %+v", got)
	}
	if got[2].Before != "user" || got[2].After != "admin" || got[2].Redacted {
		t.Fatalf("an ordinary field is kept: %+v", got[2])
	}
	if text, _ := got[3].After.(string); len([]rune(text)) != maxValueRunes+1 {
		t.Fatalf("long text is cut: %d", len([]rune(text)))
	}
	raw, _ := json.Marshal(got[4])
	if strings.Contains(string(raw), "tok") || strings.Contains(string(raw), `"pw"`) ||
		!strings.Contains(string(raw), `"n"`) {
		t.Fatalf("secret keys inside a structured value are scrubbed at any depth: %s", raw)
	}
}
