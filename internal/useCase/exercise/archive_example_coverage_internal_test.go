package exercise

import (
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// documentKeys collects every JSON key of the portable document: the tags of archiveDocV2 and of every struct it
// reaches (domain types included). A field without a tag has no key of its own.
func documentKeys(root reflect.Type) map[string]bool {
	keys := map[string]bool{}
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || seen[t] {
			return
		}
		seen[t] = true
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name != "" {
				keys[name] = true
			}
			walk(field.Type)
		}
	}
	walk(root)
	return keys
}

// Every key the importer reads appears in the example (as a key, or in a comment line that shows the other form),
// so a new key cannot be added to the format without being documented here.
func TestExampleDocument_CoversEveryKey(t *testing.T) {
	body, err := exampleFS.ReadFile("example/exercise.yaml")
	require.NoError(t, err)
	text := string(body)
	keys := documentKeys(reflect.TypeOf(archiveDocV2{}))
	// the image reference is a string or {file: ...}: that object has no tags of its own
	keys["file"] = true
	require.NotEmpty(t, keys)
	names := make([]string, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		pattern := regexp.MustCompile(`(^|[\s#{,-])` + regexp.QuoteMeta(name) + `:`)
		require.Truef(t, pattern.MatchString(text), "the key %q is read by the importer but the example does not show it", name)
	}
}
