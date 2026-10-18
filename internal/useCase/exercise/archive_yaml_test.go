package exercise

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func requireInvalid(t *testing.T, err error) map[string]any {
	t.Helper()
	if !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		t.Fatalf("err = %v, want ErrArchiveInvalid", err)
	}
	return publicContext(t, err)
}

func TestYAMLToJSON_DropsComments(t *testing.T) {
	out, err := yamlToJSON([]byte("a: 1 # end\n# whole\nb: ICE{x#y}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"a":1,"b":"ICE{x#y}"}` {
		t.Fatalf("out = %s", out)
	}
}

func TestYAMLToJSON_SpaceHashNeedsQuotes(t *testing.T) {
	out, err := yamlToJSON([]byte("flag: ICE{a #b}\n"))
	if err != nil || string(out) != `{"flag":"ICE{a"}` {
		t.Fatalf("out = %s, err = %v", out, err)
	}
	out, err = yamlToJSON([]byte("flag: \"ICE{a #b}\"\n"))
	if err != nil || string(out) != `{"flag":"ICE{a #b}"}` {
		t.Fatalf("quoted out = %s, err = %v", out, err)
	}
}

func TestParseExerciseArchive_YAMLUnquotedHashBreaksFlag(t *testing.T) {
	doc := "schema: cib-exercise/1\nexercise: {name: T}\nversions:\n  - status: draft\n    variants:\n      - tasks:\n          - name: A\n            difficulty: easy\n            flag: ICE{a #b}\n"
	_, err := ParseExerciseArchive(schemaArchive(t, map[string]string{"exercise.yaml": doc}), "", DefaultLimits())
	ctx := requireInvalid(t, err)
	if reason, _ := ctx["reason"].(string); !strings.Contains(reason, "flag") {
		t.Fatalf("reason = %q, want the flag validation", reason)
	}
}

func TestYAMLToJSON_RejectsMultiDocument(t *testing.T) {
	_, err := yamlToJSON([]byte("a: 1\n---\nb: 2\n"))
	requireInvalid(t, err)
}

func TestYAMLToJSON_RejectsAliasBomb(t *testing.T) {
	var b strings.Builder
	b.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i <= 9; i++ {
		prev := "*a" + string(rune('0'+i-1))
		b.WriteString("a" + string(rune('0'+i)) + ": &a" + string(rune('0'+i)) + " [" + strings.Repeat(prev+", ", 8) + prev + "]\n")
	}
	done := make(chan error, 1)
	go func() { _, err := yamlToJSON([]byte(b.String())); done <- err }()
	select {
	case err := <-done:
		requireInvalid(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("alias bomb not stopped")
	}
}

func TestYAMLToJSON_RejectsAnyAlias(t *testing.T) {
	_, err := yamlToJSON([]byte("a: &x hi\nb: *x\n"))
	requireInvalid(t, err)
}

func TestYAMLToJSON_RejectsNonStringKey(t *testing.T) {
	for _, doc := range []string{"1: a\n", "? [a]\n: b\n", "true: a\n", "a:\n  2: b\n"} {
		_, err := yamlToJSON([]byte(doc))
		requireInvalid(t, err)
	}
}

func TestYAMLToJSON_RejectsTimestamp(t *testing.T) {
	_, err := yamlToJSON([]byte("name: 2001-01-01\n"))
	ctx := requireInvalid(t, err)
	if !strings.Contains(ctx["reason"].(string), "name") {
		t.Fatalf("reason = %v", ctx["reason"])
	}
}

func TestYAMLToJSON_RejectsOversize(t *testing.T) {
	_, err := yamlToJSON([]byte("a: " + strings.Repeat("x", maxExerciseDocBytes)))
	requireInvalid(t, err)
}

func TestYAMLToJSON_ErrorHidesLibraryText(t *testing.T) {
	_, err := yamlToJSON([]byte("a: 1\n b: [\n"))
	reason := requireInvalid(t, err)["reason"].(string)
	if strings.Contains(reason, "yaml:") || !strings.Contains(reason, "line") {
		t.Fatalf("reason = %q", reason)
	}
}

const minimalYAML = `# the minimal task
schema: cib-exercise/1 # version
exercise:
  name: Тест-завдання # shown name
versions:
  - status: draft
    variants:
      - tasks:
          - name: Тест
            difficulty: easy
`

func TestParseExerciseArchive_YAMLSchema1(t *testing.T) {
	fromJSON, err := ParseExerciseArchive(schemaArchive(t, map[string]string{"exercise.json": minimalDoc}), "", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	fromYAML, err := ParseExerciseArchive(schemaArchive(t, map[string]string{"exercise.yaml": minimalYAML}), "", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	fromYAML.Versions[0].Variants[0].ID = fromJSON.Versions[0].Variants[0].ID
	fromJSON.Files, fromYAML.Files = nil, nil
	if !reflect.DeepEqual(fromJSON, fromYAML) {
		t.Fatalf("differs:\n%+v\n%+v", fromJSON, fromYAML)
	}
}

func TestParseExerciseArchive_YAMLWithoutSchemaRejected(t *testing.T) {
	_, err := ParseExerciseArchive(schemaArchive(t, map[string]string{"exercise.yaml": "exercise:\n  name: T\n"}), "", DefaultLimits())
	if !errors.Is(err, exerciseModel.ErrArchiveSchemaUnsupported.Err()) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseExerciseArchive_UnquotedNumberForStringField(t *testing.T) {
	doc := "schema: cib-exercise/1\nexercise: {name: T}\nversions:\n  - status: draft\n    variants:\n      - tasks:\n          - name: A\n            difficulty: easy\n            image: 1.10\n"
	_, err := ParseExerciseArchive(schemaArchive(t, map[string]string{"exercise.yaml": doc}), "", DefaultLimits())
	ctx := requireInvalid(t, err)
	if !strings.Contains(ctx["reason"].(string), "image") {
		t.Fatalf("reason = %v", ctx["reason"])
	}
}

func TestYAMLToJSON_YesNoStayStrings(t *testing.T) {
	out, err := yamlToJSON([]byte("a: no\nb: yes\nc: on\nd: true\n"))
	if err != nil || string(out) != `{"a":"no","b":"yes","c":"on","d":true}` {
		t.Fatalf("out = %s, err = %v", out, err)
	}
}

func TestYAMLToJSON_NumbersKeepTheirText(t *testing.T) {
	cases := map[string]string{
		"a: 1e3":                  `{"a":1e3}`,
		"a: 1.0":                  `{"a":1.0}`,
		"a: 99999999999999999999": `{"a":99999999999999999999}`,
		"a: 9007199254740993.0":   `{"a":9007199254740993.0}`,
		"a: -0":                   `{"a":-0}`,
	}
	for in, want := range cases {
		out, err := yamlToJSON([]byte(in + "\n"))
		if err != nil || string(out) != want {
			t.Fatalf("%q: out = %s, err = %v", in, out, err)
		}
	}
}

func TestYAMLToJSON_RejectsNumberForms(t *testing.T) {
	for _, in := range []string{"a: 0755", "a: 0x1F", "a: 0o17", "a: 1_000", "a: +1", "a: .5", "a: .inf", "a: 1."} {
		_, err := yamlToJSON([]byte(in + "\n"))
		reason, _ := requireInvalid(t, err)["reason"].(string)
		if !strings.Contains(reason, "a") || !strings.Contains(reason, "plain decimal number") {
			t.Fatalf("%q: reason = %q", in, reason)
		}
	}
}

func TestYAMLToJSON_RejectsExplicitTags(t *testing.T) {
	for _, in := range []string{`a: !!str 5`, `a: !!int "7"`, `a: !foo bar`} {
		_, err := yamlToJSON([]byte(in + "\n"))
		reason, _ := requireInvalid(t, err)["reason"].(string)
		if !strings.Contains(reason, "a at line 1") || !strings.Contains(reason, "tags") {
			t.Fatalf("%q: reason = %q", in, reason)
		}
	}
}

func TestYAMLToJSON_RejectsNullKey(t *testing.T) {
	_, err := yamlToJSON([]byte("~: a\n"))
	requireInvalid(t, err)
}

func TestYAMLToJSON_EmptyDocument(t *testing.T) {
	for _, in := range []string{"", "# only a comment\n"} {
		_, err := yamlToJSON([]byte(in))
		if reason, _ := requireInvalid(t, err)["reason"].(string); reason != "the document is empty" {
			t.Fatalf("%q: reason = %q", in, reason)
		}
	}
}

func TestParseExerciseArchive_NumberFormsFailIntFields(t *testing.T) {
	for _, value := range []string{"1e3", "99999999999999999999", "1.5"} {
		doc := "schema: cib-exercise/1\nexercise: {name: T}\nversions:\n  - status: draft\n    variants:\n      - index: " + value + "\n        tasks:\n          - name: A\n            difficulty: easy\n"
		_, err := ParseExerciseArchive(schemaArchive(t, map[string]string{"exercise.yaml": doc}), "", DefaultLimits())
		reason, _ := requireInvalid(t, err)["reason"].(string)
		if !strings.Contains(reason, "index") || strings.Contains(reason, "unknown") {
			t.Fatalf("%s: reason = %q", value, reason)
		}
	}
}
