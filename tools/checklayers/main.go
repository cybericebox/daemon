// Command checklayers enforces the layering rule from CLAUDE.md: the domain
// (internal/model/...) must not import the application or delivery layers.
//
// Usage: go run ./tools/checklayers [modelDir]   (default: internal/model)
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var forbiddenRe = regexp.MustCompile(`"github\.com/cybericebox/daemon/internal/(delivery|useCase)(/[^"]*)?"`)

func violations(src string) []string {
	var out []string
	for _, m := range forbiddenRe.FindAllString(src, -1) {
		out = append(out, m)
	}
	return out
}

func main() {
	root := "internal/model"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	var found []string
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
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
		for _, v := range violations(string(data)) {
			found = append(found, fmt.Sprintf("%s imports %s", path, v))
		}
		return nil
	})
	if walkErr != nil {
		fmt.Fprintln(os.Stderr, "checklayers:", walkErr)
		os.Exit(2)
	}

	if len(found) > 0 {
		fmt.Fprintln(os.Stderr, "domain layer must not import application/delivery:")
		for _, v := range found {
			fmt.Fprintln(os.Stderr, "  "+v)
		}
		os.Exit(1)
	}
}
