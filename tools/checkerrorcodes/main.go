// Command checkerrorcodes enforces the error-code convention: within one
// object code, every error variable must hold a unique DetailCode — the lib's
// err.As compares only DetailCode when the target has one, so a duplicate
// makes errors.Is conflate two unrelated errors (e.g. a 401 with a 404).
//
// Usage: go run ./tools/checkerrorcodes [dir]   (default dir: internal)
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// declRe matches one builder chain: WithObjectCode(model.XObjectCode) followed
// (possibly across lines) by WithDetailCode(N). Declarations always order the
// calls this way, and (?U) keeps the match inside a single chain.
var declRe = regexp.MustCompile(`(?sU)WithObjectCode\(model\.(\w+)\).*WithDetailCode\((\d+)\)`)

func scan(src string) map[string][]string {
	found := map[string][]string{} // "Object:Code" → occurrences (for dup detection)
	for _, m := range declRe.FindAllStringSubmatch(src, -1) {
		key := m[1] + ":" + m[2]
		found[key] = append(found[key], m[0])
	}
	return found
}

func main() {
	roots := []string{"internal"}
	if len(os.Args) > 1 {
		roots = os.Args[1:]
	}

	seen := map[string]string{} // "Object:Code" → file of first declaration
	var violations []string

	walk := func(root string) error {
		return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for key, occurrences := range scan(string(data)) {
				if len(occurrences) > 1 {
					violations = append(violations,
						fmt.Sprintf("%s: DetailCode %s declared %d times in one file", path, key, len(occurrences)))
				}
				if prev, dup := seen[key]; dup {
					violations = append(violations,
						fmt.Sprintf("%s: DetailCode %s already declared in %s", path, key, prev))
				} else {
					seen[key] = path
				}
			}
			return nil
		})
	}
	for _, root := range roots {
		if err := walk(root); err != nil {
			fmt.Fprintln(os.Stderr, "checkerrorcodes:", err)
			os.Exit(2)
		}
	}

	if len(violations) > 0 {
		fmt.Fprintln(os.Stderr, "duplicate error DetailCodes (errors.Is will conflate these):")
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, "  "+v)
		}
		os.Exit(1)
	}
}
