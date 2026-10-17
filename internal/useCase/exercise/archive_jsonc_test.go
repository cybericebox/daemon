package exercise

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestStripJSONComments_LineAndBlock(t *testing.T) {
	out, err := stripJSONComments([]byte("{ // a\n \"x\": 1 /* b\n c */ }"))
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	var got map[string]int
	if err = json.Unmarshal(out, &got); err != nil || got["x"] != 1 {
		t.Fatalf("decoded %v, %v from %q", got, err, out)
	}
}

func TestStripJSONComments_KeepsStrings(t *testing.T) {
	in := []byte(`{"f":"ICE{//x}","g":"a/*b*/c","h":"q\"//"}`)
	out, err := stripJSONComments(in)
	if err != nil || !bytes.Equal(out, in) {
		t.Fatalf("out = %q, %v; want unchanged", out, err)
	}
}

func TestStripJSONComments_EscapedBackslashBeforeQuote(t *testing.T) {
	in := []byte("{\"a\":\"x\\\\\" // c\n}")
	out, err := stripJSONComments(in)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	var got map[string]string
	if err = json.Unmarshal(out, &got); err != nil || got["a"] != `x\` {
		t.Fatalf("decoded %v, %v from %q", got, err, out)
	}
}

func TestStripJSONComments_Unterminated(t *testing.T) {
	_, err := stripJSONComments([]byte(`{"x":1 /*`))
	if !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		t.Fatalf("err = %v, want ErrArchiveInvalid", err)
	}
	if got := publicContext(t, err)["reason"]; got != "unterminated comment" {
		t.Fatalf("reason = %v", got)
	}
}

func TestStripJSONComments_TrailingCommaStillFails(t *testing.T) {
	in := []byte("{\"schema\":\"cib-exercise/1\", // c\n}")
	out, err := stripJSONComments(in)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	if json.Valid(out) {
		t.Fatalf("a trailing comma must stay an error: %q", out)
	}
	if _, err = decodeArchiveV2(out); !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		t.Fatalf("err = %v, want ErrArchiveInvalid", err)
	}
}

func TestStripJSONComments_KeepsLineNumbers(t *testing.T) {
	in := []byte("/* a\n b\n c */\n{\"x\": ,}")
	out, err := stripJSONComments(in)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	if len(out) != len(in) || bytes.Count(out, []byte("\n")) != bytes.Count(in, []byte("\n")) {
		t.Fatalf("length or newlines changed: %q", out)
	}
	var anything any
	if json.Unmarshal(out, &anything) == nil {
		t.Fatal("expected a syntax error")
	}
	_, decodeErr := decodeArchiveV2(out)
	if got := publicContext(t, decodeErr)["reason"]; got != "invalid JSON at line 4" {
		t.Fatalf("reason = %v, want line 4", got)
	}
}

// exercise.json has the same 4 MiB cap as exercise.yaml, checked before comments are stripped. The filler is
// hex (no comment, valid JSON), so the zip's expansion ratio stays far below its own limit.
func TestParseExerciseArchive_JSONDocumentTooLarge(t *testing.T) {
	var filler bytes.Buffer
	seed := sha256.Sum256([]byte("filler"))
	for filler.Len() <= maxExerciseDocBytes {
		seed = sha256.Sum256(seed[:])
		filler.WriteString(hex.EncodeToString(seed[:]))
	}
	doc := `{"schema":"cib-exercise/1","exercise":{"name":"Завелике","description":"` + filler.String() + `"},"versions":[]}`
	archive, err := BuildArchiveNoManifest(map[string][]byte{"exercise.json": []byte(doc)})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	_, err = ParseExerciseArchive(archive, "")
	if !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) || !strings.Contains(err.Error(), "the document is too large") {
		t.Fatalf("got %v, want the document-too-large error", err)
	}
}
