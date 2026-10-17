package exercise

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/gofrs/uuid"
)

func textNode(s string) map[string]any {
	return map[string]any{"type": "text", "version": 1.0, "text": s, "detail": 0.0, "format": 0.0, "mode": "normal", "style": ""}
}

func varNode(key string) map[string]any {
	return map[string]any{"type": "variable", "version": 1.0, "varName": key, "formats": []any{}}
}

func breakNode() map[string]any { return map[string]any{"type": "linebreak", "version": 1.0} }

func paragraphNode(children ...any) map[string]any {
	return map[string]any{"type": "paragraph", "version": 1.0, "direction": nil, "format": "", "indent": 0.0, "textFormat": 0.0, "textStyle": "", "children": children}
}

func documentNode(paragraphs ...any) map[string]any {
	return map[string]any{"root": map[string]any{"type": "root", "version": 1.0, "direction": nil, "format": "", "indent": 0.0, "children": paragraphs}}
}

func convertPlain(t *testing.T, text string, keys ...string) (map[string]any, error) {
	t.Helper()
	var placeholders []exerciseModel.Placeholder
	for _, key := range keys {
		placeholders = append(placeholders, exerciseModel.Placeholder{Key: key, Kind: exerciseModel.PlaceholderVPNSubnet})
	}
	raw, err := plainTextToDocument(text, placeholders, "Q")
	if err != nil {
		return nil, err
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	return got, nil
}

func assertDocument(t *testing.T, got, want map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("document mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestPlainText_ParagraphsAndBreaks(t *testing.T) {
	got, err := convertPlain(t, "one\ntwo\n\n\nthree")
	if err != nil {
		t.Fatal(err)
	}
	assertDocument(t, got, documentNode(
		paragraphNode(textNode("one"), breakNode(), textNode("two")),
		paragraphNode(textNode("three")),
	))
}

func TestPlainText_VariableNode(t *testing.T) {
	got, err := convertPlain(t, "Open {{srv}} now", "srv")
	if err != nil {
		t.Fatal(err)
	}
	assertDocument(t, got, documentNode(paragraphNode(textNode("Open "), varNode("srv"), textNode(" now"))))
}

func TestPlainText_EscapedBraces(t *testing.T) {
	got, err := convertPlain(t, `use \{{srv}} literally`, "srv")
	if err != nil {
		t.Fatal(err)
	}
	assertDocument(t, got, documentNode(paragraphNode(textNode("use {{srv}} literally"))))
}

func TestPlainText_UnknownKey(t *testing.T) {
	_, err := convertPlain(t, "see {{nope}}", "srv")
	if !errors.Is(err, exerciseModel.ErrArchiveDescriptionKeyUnknown.Err()) {
		t.Fatalf("want ErrArchiveDescriptionKeyUnknown, got %v", err)
	}
	ctx := publicContext(t, err)
	if ctx["key"] != "nope" || ctx["task"] != "Q" {
		t.Fatalf("context = %v", ctx)
	}
}

func TestPlainText_SpacesTrimmedAndCRLF(t *testing.T) {
	got, err := convertPlain(t, "\r\n\r\na {{  srv  }}\r\nb\r\n\r\nc\r\n\r\n", "srv")
	if err != nil {
		t.Fatal(err)
	}
	assertDocument(t, got, documentNode(
		paragraphNode(textNode("a "), varNode("srv"), breakNode(), textNode("b")),
		paragraphNode(textNode("c")),
	))
}

func TestPlainText_UnclosedIsError(t *testing.T) {
	_, err := convertPlain(t, "broken {{srv", "srv")
	if !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		t.Fatalf("want ErrArchiveInvalid, got %v", err)
	}
}

func TestPlainText_EmptyIsNil(t *testing.T) {
	for _, text := range []string{"", "  \n\r\n\t "} {
		raw, err := plainTextToDocument(text, nil, "Q")
		if err != nil || raw != nil {
			t.Fatalf("%q: got %s, %v", text, raw, err)
		}
	}
}

func TestPlainText_PassesPublishValidation(t *testing.T) {
	placeholders := []exerciseModel.Placeholder{{Key: "srv", Kind: exerciseModel.PlaceholderVPNSubnet}}
	raw, err := plainTextToDocument("Scan {{srv}}", placeholders, "Q")
	if err != nil {
		t.Fatal(err)
	}
	version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{{
		Topology: exerciseModel.Topology{VPN: exerciseModel.NetworkSpec{Enabled: true}},
		Tasks: []exerciseModel.Task{{
			ID: uuid.FromStringOrNil("11111111-1111-1111-1111-111111111111"), Name: "Question", Difficulty: exerciseModel.DifficultyEasy,
			Description: raw, Flag: []string{"ICE{x}"}, Placeholders: placeholders,
		}},
	}}}
	if err := version.ValidateForPublish(); err != nil {
		t.Fatalf("ValidateForPublish: %v", err)
	}
}

func TestToParsed_EditorDocDescriptionStillAccepted(t *testing.T) {
	editor := `{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"hi"}]}]}}`
	got, err := descriptionToRaw(json.RawMessage(editor), nil, "Q")
	if err != nil {
		t.Fatal(err)
	}
	var want, have map[string]any
	_ = json.Unmarshal([]byte(editor), &want)
	_ = json.Unmarshal(got, &have)
	assertDocument(t, have, want)

	plain, err := descriptionToRaw(json.RawMessage(`"hello"`), nil, "Q")
	if err != nil || plain == nil {
		t.Fatalf("string description: %s, %v", plain, err)
	}
	if raw, err := descriptionToRaw(json.RawMessage(`null`), nil, "Q"); raw != nil || err != nil {
		t.Fatalf("null: %s, %v", raw, err)
	}
	if _, err := descriptionToRaw(json.RawMessage(`42`), nil, "Q"); !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		t.Fatalf("number: %v", err)
	}
}
