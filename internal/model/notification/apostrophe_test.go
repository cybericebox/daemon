package notificationModel_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Ukrainian apostrophe is U+02BC. A straight or typographic one inside a word is a different character: it breaks
// search and hyphenation and looks wrong next to the rest of the text.
var wrongApostrophe = regexp.MustCompile(`(?i)[а-яіїєґ]['’][а-яіїєґ]`)

// Every Ukrainian user-visible text of the daemon (mail and in-app templates, CSV headers, the error catalog) spells
// the apostrophe as U+02BC. Migrations are history and tests are fixtures, so neither is read.
func TestUkrainianTextsUseTheModifierApostrophe(t *testing.T) {
	root := "../../.."
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found: %v", err)
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == ".worktrees" || name == "migrations" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		isSource := strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
		if !isSource && name != "errors.uk.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if m := wrongApostrophe.FindString(line); m != "" {
				t.Errorf("%s:%d: %q: write the apostrophe as U+02BC", path, i+1, m)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
