package laboratorysdk_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sdkCheck(t *testing.T, bundle string) error {
	t.Helper()
	return exec.Command("python3", "../../.github/scripts/check-laboratory-sdk.py", "--bundle", bundle).Run()
}
func TestLaboratorySDKMatchesProvenance(t *testing.T) {
	if err := sdkCheck(t, "../../third_party/laboratory-sdk"); err != nil {
		t.Fatal("SDK bundle provenance mismatch", err)
	}
}
func TestLaboratorySDKRejectsMissingBundle(t *testing.T) {
	if err := sdkCheck(t, filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("missing bundle accepted")
	}
}
func TestLaboratorySDKRejectsTamper(t *testing.T) {
	for _, name := range []string{"pkg/agent/client/client.go", "PROVENANCE.json"} {
		t.Run(name, func(t *testing.T) {
			dest := t.TempDir()
			err := filepath.WalkDir("../../third_party/laboratory-sdk", func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				relative, err := filepath.Rel("../../third_party/laboratory-sdk", path)
				if err != nil {
					return err
				}
				target := filepath.Join(dest, relative)
				if entry.IsDir() {
					return os.MkdirAll(target, 0755)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				return os.WriteFile(target, data, 0644)
			})
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dest, name)
			data, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if name == "PROVENANCE.json" {
				// A syntactically valid different SHA must fail, not just malformed text.
				text := string(data)
				start := strings.Index(text, `"SourceCommit": "`) + len(`"SourceCommit": "`)
				data = []byte(text[:start] + strings.Repeat("a", 40) + text[start+40:])
			} else {
				data = append(data, []byte("\n// altered snapshot\n")...)
			}
			if err = os.WriteFile(target, data, 0644); err != nil {
				t.Fatal(err)
			}
			if err = sdkCheck(t, dest); err == nil {
				t.Fatal("tampered bundle accepted")
			}
		})
	}
}
