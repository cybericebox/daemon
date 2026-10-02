// Command checkroutes enforces the route-gating rule from CLAUDE.md: since
// use cases no longer duplicate permission checks, EVERY HTTP route
// registration in the handler packages must be gated by RequirePermission
// (inline, via a local middleware variable, or on its group). Registrations
// without a gate must be explicitly allowlisted as public, per method,
// receiver and path.
//
// This is the fast static pre-check. What it cannot see (PermSelf standing
// alone, the real middleware chain of the mounted tree) is covered by
// TestRoutePolicy / TestRouteInvariants in the handler package, which build the
// actual router and compare it with testdata/routes.golden.
//
// Recognised registrations: GET POST PUT PATCH DELETE HEAD OPTIONS Any
// (path, handlers...), Handle (method, path, handlers...) and Match (methods,
// path, handlers...). A registration whose path is not a string literal is a
// violation: the allowlist cannot vouch for a path it cannot read.
//
// Usage: go run ./tools/checkroutes [handlerDir]
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// pathArg is the index of the path argument per registration method.
var pathArg = map[string]int{
	"GET": 0, "POST": 0, "PUT": 0, "PATCH": 0, "DELETE": 0, "HEAD": 0, "OPTIONS": 0, "Any": 0,
	"Handle": 1, "Match": 1,
}

// publicRoutes are the registrations deliberately reachable without a
// permission gate, keyed "METHOD receiver/path" so a literal ("reset",
// "callback", "") on another group or with another method is NOT whitelisted.
// RequireRecaptcha does NOT count as a gate — it is bot protection, not
// authorization — so recaptcha-only routes are listed here too.
var publicRoutes = map[string]bool{
	"POST pub/sign-in":                   true, // recaptcha-gated credential entry
	"POST pub/sign-up":                   true, // recaptcha-gated registration entry
	"GET pub/setup":                      true, // setup-token flow (context)
	"POST pub/setup":                     true, // setup-token flow (complete)
	"POST password/reset-request":        true, // recaptcha-gated password reset request
	"POST password/reset":                true, // password reset with emailed code
	"GET password/policy":                true, // static password complexity policy (config, no user data)
	"GET google/":                        true, // google oauth root redirect
	"GET google/register":                true, // google oauth registration redirect
	"GET google/setup":                   true, // google oauth setup-link redirect (email must match)
	"GET google/callback":                true, // google oauth callback (state cookie double-submit)
	"POST pub/account/email/confirm":     true, // authorized by the single-use emailed code itself
	"GET pub/avatar/:id":                 true, // public avatar proxy (bucket stays private)
	"GET ev/upcoming":                    true, // landing-page card of the nearest published event (public data only)
	"GET ev/:id/logo/:fileID":            true, // public image proxy verifies the file is this event's current logo
	"GET ev/:id/favicon/:fileID":         true, // public image proxy verifies the file is this event's current favicon
	"GET ev/:id/preview-picture/:fileID": true, // public image proxy verifies the file is this event's current preview
	"GET ev/:id/content-images/:fileID":  true, // public image proxy verifies the file belongs to this event's page content
}

// fileViolations returns route registrations that are neither gated nor
// allowlisted, for one parsed file.
func fileViolations(f *ast.File) []string {
	var out []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		out = append(out, funcViolations(fn.Body)...)
	}
	// Registrations outside any function body (package-level vars) have no
	// scope to hide in: check them with an empty one.
	return out
}

// funcViolations walks one function body in source order. Gate variables and
// gated groups are tracked per function (the same name in another function is
// another variable) and re-assigned names lose or gain their gate where the
// assignment is, not file-wide.
func funcViolations(body *ast.BlockStmt) []string {
	var out []string
	gateVars := map[string]bool{}
	gatedGroups := map[string]bool{}

	isGate := func(e ast.Expr) bool {
		if callTo(e, "RequirePermission") {
			return true
		}
		id, ok := e.(*ast.Ident)
		return ok && gateVars[id.Name]
	}

	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				if i >= len(n.Lhs) {
					break
				}
				id, ok := n.Lhs[i].(*ast.Ident)
				if !ok {
					continue
				}
				gateVars[id.Name] = callTo(rhs, "RequirePermission")
				call, ok := rhs.(*ast.CallExpr)
				if !ok {
					gatedGroups[id.Name] = false
					continue
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Group" {
					gatedGroups[id.Name] = false
					continue
				}
				gated := false
				if recv, ok := sel.X.(*ast.Ident); ok && gatedGroups[recv.Name] {
					gated = true
				}
				for _, arg := range call.Args[1:] {
					if isGate(arg) {
						gated = true
					}
				}
				gatedGroups[id.Name] = gated
			}
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			// group.Use(gate) gates every registration after it on that group.
			if sel.Sel.Name == "Use" {
				if recv, ok := sel.X.(*ast.Ident); ok {
					for _, arg := range n.Args {
						if isGate(arg) {
							gatedGroups[recv.Name] = true
						}
					}
				}
				return true
			}
			idx, isRoute := pathArg[sel.Sel.Name]
			if !isRoute || len(n.Args) <= idx {
				return true
			}
			method := strings.ToUpper(sel.Sel.Name)
			if sel.Sel.Name == "Handle" {
				if m, ok := stringLit(n.Args[0]); ok {
					method = strings.ToUpper(m)
				} else {
					method = "?"
				}
			}
			path, ok := stringLit(n.Args[idx])
			if !ok {
				// A non-gin call that happens to be named GET with a variable
				// would be a false alarm, but no such call exists in the handler
				// packages; failing here is the point.
				out = append(out, fmt.Sprintf("%s registration with a non-literal path: the allowlist cannot vouch for it", sel.Sel.Name))
				return true
			}
			if len(n.Args) <= idx+1 {
				return true // not a route registration shape (no handlers)
			}
			gated := false
			recvName := ""
			if recv, ok := sel.X.(*ast.Ident); ok {
				recvName = recv.Name
				gated = gatedGroups[recv.Name]
			}
			for _, arg := range n.Args[idx+1:] {
				if isGate(arg) {
					gated = true
					break
				}
			}
			key := method + " " + recvName + "/" + path
			if !gated && !publicRoutes[key] {
				out = append(out, fmt.Sprintf("%s %q has no RequirePermission gate and is not allowlisted as public (%s)", sel.Sel.Name, path, key))
			}
		}
		return true
	})
	return out
}

func callTo(e ast.Expr, name string) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == name
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(lit.Value, `"`), true
}

func main() {
	root := "internal/delivery/controller/http/handler"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	fset := token.NewFileSet()
	var violations []string
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, v := range fileViolations(f) {
			violations = append(violations, path+": "+v)
		}
		return nil
	})
	if walkErr != nil {
		fmt.Fprintln(os.Stderr, "checkroutes:", walkErr)
		os.Exit(2)
	}

	if len(violations) > 0 {
		fmt.Fprintln(os.Stderr, "ungated routes (use cases no longer re-check permissions — the route is the only gate):")
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, "  "+v)
		}
		os.Exit(1)
	}
}
