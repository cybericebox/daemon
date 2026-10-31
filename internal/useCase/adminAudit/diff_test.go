package adminAudit

import (
	"encoding/json"
	"strings"
	"testing"
)

type sampleSettings struct {
	Days         int
	Host         string
	SmtpPassword string
	APIToken     string
	Nested       struct {
		Limit      int
		PrivateKey string
		Keyword    string
	}
	Tags []string
}

func TestDiffListsOnlyChangedFields(t *testing.T) {
	before := sampleSettings{Days: 30, Host: "a"}
	after := sampleSettings{Days: 60, Host: "a"}
	after.Nested.Limit = 5
	got := Diff(before, after)
	if len(got) != 2 || got[0].Field != "Days" || got[0].Before != float64(30) || got[0].After != float64(60) ||
		got[1].Field != "Nested.Limit" {
		t.Fatalf("diff = %+v", got)
	}
	if Diff(before, before) != nil {
		t.Fatal("equal values have no changes")
	}
}

// A secret never reaches the stored record: not as the old value, not as the new one, not under a nested path
// and not in the JSON that goes to the database.
func TestDiffNeverCarriesASecret(t *testing.T) {
	before := sampleSettings{SmtpPassword: "old-hunter2", APIToken: "tok-old"}
	after := sampleSettings{SmtpPassword: "new-hunter3", APIToken: "tok-new"}
	before.Nested.PrivateKey, after.Nested.PrivateKey = "-----OLD KEY-----", "-----NEW KEY-----"
	before.Nested.Keyword, after.Nested.Keyword = "alpha", "beta"

	got := Diff(before, after)
	redacted := map[string]bool{}
	for _, c := range got {
		if c.Redacted {
			redacted[c.Field] = true
			if c.Before != nil || c.After != nil {
				t.Fatalf("%s kept a value: %+v", c.Field, c)
			}
		}
	}
	for _, field := range []string{"SmtpPassword", "APIToken", "Nested.PrivateKey"} {
		if !redacted[field] {
			t.Errorf("%s must be recorded as redacted: %+v", field, got)
		}
	}
	if redacted["Nested.Keyword"] {
		t.Error("a harmless field with 'key' inside its name is not a secret")
	}
	raw, _ := json.Marshal(got)
	for _, leaked := range []string{"hunter", "tok-", "OLD KEY", "NEW KEY"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("%q leaked into %s", leaked, raw)
		}
	}
}

// Whatever the caller builds by hand is cleaned at the single place that writes: a Change that names a secret
// field loses its values there.
func TestSanitizeStripsSecretsFromHandBuiltChanges(t *testing.T) {
	got := sanitize([]Change{
		{Field: "Password", Before: "a", After: "b"},
		{Field: "Settings.recoveryCode", After: "123456"},
		{Field: "Role", Before: "user", After: "admin"},
		{Field: "Bio", After: strings.Repeat("я", 500)},
	})
	if !got[0].Redacted || got[0].Before != nil || got[0].After != nil || !got[1].Redacted || got[1].After != nil {
		t.Fatalf("secrets kept: %+v", got)
	}
	if got[2].Before != "user" || got[2].After != "admin" || got[2].Redacted {
		t.Fatalf("an ordinary field is kept: %+v", got[2])
	}
	if text, _ := got[3].After.(string); len([]rune(text)) != maxValueRunes+1 {
		t.Fatalf("long text is cut: %d", len([]rune(text)))
	}
}

func TestDiffOfRawJSONValuesFlattensAndRedactsInside(t *testing.T) {
	got := Diff(json.RawMessage(`{"smtp":{"host":"a","password":"x"},"tags":[1,2]}`), json.RawMessage(`{"smtp":{"host":"b","password":"y"},"tags":[1,2]}`))
	if len(got) != 2 || got[0].Field != "smtp.host" || !got[1].Redacted || got[1].Field != "smtp.password" {
		t.Fatalf("diff = %+v", got)
	}
}
