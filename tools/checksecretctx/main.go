// Command checksecretctx keeps the associated data of stored secrets in one place.
//
// Every secret is sealed with a context that binds it to its row or purpose; the strings are built in internal/secretctx and
// nowhere else, and a seal or an open without a context is refused at run time. This check fails the build when
//   - a one-argument Encrypt( or Decrypt( is called (the sealing calls without a context that pkg/secret no longer has);
//   - EncryptWithContext or DecryptWithContext gets a nil, empty or hand-built literal context ([]byte("..."), []byte(fmt.Sprintf(...)));
//   - a string literal that starts like a known context appears outside internal/secretctx.
//
// Usage: go run ./tools/checksecretctx [dir ...]   (default: internal pkg cmd)
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// contextPrefixes are the starts of the contexts the stored data was sealed with.
var contextPrefixes = []string{
	"mail.smtp:", "infra.agent:", "image-registry:", "event-registry:", "vpn-config:", "exercise-env:", "notify-payload:",
	"auth-delivery:", "exercise-import:password:", "exercise-export:password:", "cybericebox/session-ticket/",
}

// ignoreMarker, in a comment on the same line, says a string that looks like a context is something else (a lookup code).
const ignoreMarker = "secretctx:ignore"

// exempt are the directories where contexts are built (secretctx) or the cipher itself lives.
func exempt(path string) bool {
	p := filepath.ToSlash(path)
	return strings.HasPrefix(p, "internal/secretctx/") || strings.HasPrefix(p, "pkg/secret/")
}

// violations parses one file and returns what it breaks, as "file:line: text".
func violations(filename, src string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return []string{fmt.Sprintf("%s: cannot be parsed: %v", filename, err)}
	}
	lines := strings.Split(src, "\n")
	var out []string
	report := func(pos token.Pos, format string, args ...any) {
		line := fset.Position(pos).Line
		if line >= 1 && line <= len(lines) && strings.Contains(lines[line-1], ignoreMarker) {
			return // a line that says, in a comment, that the string is not a context
		}
		out = append(out, fmt.Sprintf("%s:%d: %s", filename, line, fmt.Sprintf(format, args...)))
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "Encrypt", "Decrypt":
				if len(node.Args) == 1 {
					report(node.Pos(), "%s( without a context: seal and open with EncryptWithContext/DecryptWithContext and a secretctx builder", sel.Sel.Name)
				}
			case "EncryptWithContext", "DecryptWithContext":
				if len(node.Args) == 2 {
					if why := badContext(node.Args[1]); why != "" {
						report(node.Args[1].Pos(), "%s: %s; use a builder of internal/secretctx", sel.Sel.Name, why)
					}
				}
			}
		case *ast.BasicLit:
			if node.Kind == token.STRING {
				if text, err := strconv.Unquote(node.Value); err == nil {
					for _, prefix := range contextPrefixes {
						if strings.HasPrefix(text, prefix) {
							report(node.Pos(), "the string %q is a secret context and belongs in internal/secretctx", text)
						}
					}
				}
			}
		}
		return true
	})
	return out
}

// badContext says why an argument cannot be a context: nil, an empty literal, or built from a literal on the spot.
func badContext(arg ast.Expr) string {
	switch a := arg.(type) {
	case *ast.Ident:
		if a.Name == "nil" {
			return "a nil context"
		}
	case *ast.CompositeLit:
		return "a literal context"
	case *ast.CallExpr:
		// []byte(<literal or Sprintf or concatenation>)
		if arr, ok := a.Fun.(*ast.ArrayType); ok && arr.Len == nil && len(a.Args) == 1 {
			switch inner := a.Args[0].(type) {
			case *ast.BasicLit:
				return "a context written as a literal"
			case *ast.BinaryExpr:
				return "a context concatenated on the spot"
			case *ast.CallExpr:
				if s, ok := inner.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Sprintf" {
					return "a context formatted on the spot"
				}
			case *ast.Ident:
				if inner.Name == "nil" {
					return "a nil context"
				}
			}
		}
	}
	return ""
}

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		roots = []string{"internal", "pkg", "cmd"}
	}
	var found []string
	for _, root := range roots {
		walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || exempt(path) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			found = append(found, violations(path, string(data))...)
			return nil
		})
		if walkErr != nil {
			fmt.Fprintln(os.Stderr, "checksecretctx:", walkErr)
			os.Exit(2)
		}
	}
	if len(found) > 0 {
		fmt.Fprintln(os.Stderr, "secret contexts must come from internal/secretctx, and every seal needs one:")
		for _, v := range found {
			fmt.Fprintln(os.Stderr, "  "+v)
		}
		os.Exit(1)
	}
}
