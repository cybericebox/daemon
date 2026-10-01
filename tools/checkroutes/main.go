// Command checkroutes enforces the route-gating rule from CLAUDE.md: since
// use cases no longer duplicate permission checks, EVERY HTTP route
// registration in the handler packages must be gated by RequirePermission
// (inline or via a local middleware variable). Registrations without a gate
// must be explicitly allowlisted as public.
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

var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

// publicPaths are route literals that are deliberately reachable without a
// permission gate (registration/setup/oauth entry points). RequireRecaptcha
// does NOT count as a gate — it is bot protection, not authorization — so
// recaptcha-only routes are listed here too.
var publicPaths = map[string]bool{
	"sign-in":               true, // recaptcha-gated credential entry
	"sign-up":               true, // recaptcha-gated registration entry
	"setup":                 true, // setup-token flow (GET context + POST complete)
	"reset-request":         true, // recaptcha-gated password reset request
	"reset":                 true, // password reset with emailed code
	"register":              true, // google oauth registration redirect
	"callback":              true, // google oauth callback
	"account/email/confirm": true, // authorized by the single-use emailed code itself
	"avatar/:id":            true, // public avatar proxy (bucket stays private)
}

// publicReceiverPaths allowlists registrations per receiver ("<recv>/<path>"),
// so a generic literal ("" or "policy") cannot accidentally whitelist the same
// literal on another group, like dispatches.GET("").
var publicReceiverPaths = map[string]bool{
	"google/":                        true, // google oauth root redirect: google.GET("", ...)
	"password/policy":                true, // static password complexity policy (config, no user data)
	"ev/upcoming":                    true, // landing-page card of the nearest published event (public data only)
	"ev/:id/logo/:fileID":            true, // public image proxy verifies the file is this event's current logo
	"ev/:id/favicon/:fileID":         true, // public image proxy verifies the file is this event's current favicon
	"ev/:id/preview-picture/:fileID": true, // public image proxy verifies the file is this event's current preview
	"ev/:id/content-images/:fileID":  true, // public image proxy verifies the file belongs to this event's page content
}

// fileViolations returns route registrations that are neither gated nor
// allowlisted, for one parsed file.
func fileViolations(f *ast.File) []string {
	var out []string

	// Collect local identifiers assigned from a RequirePermission call
	// (e.g. `self := h.prot.RequirePermission(rbac.PermSelf)`) and router
	// groups whose Group(...) call carries a gate (the gate then covers every
	// registration on that group). Group gating also propagates from an
	// already-gated parent group.
	gateVars := map[string]bool{}
	gatedGroups := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range assign.Rhs {
			if i >= len(assign.Lhs) {
				break
			}
			id, ok := assign.Lhs[i].(*ast.Ident)
			if !ok {
				continue
			}
			if callTo(rhs, "RequirePermission") {
				gateVars[id.Name] = true
				continue
			}
			if call, ok := rhs.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Group" {
					gated := false
					if recv, ok := sel.X.(*ast.Ident); ok && gatedGroups[recv.Name] {
						gated = true
					}
					for _, arg := range call.Args[1:] {
						if callTo(arg, "RequirePermission") {
							gated = true
						}
						if aid, ok := arg.(*ast.Ident); ok && gateVars[aid.Name] {
							gated = true
						}
					}
					if gated {
						gatedGroups[id.Name] = true
					}
				}
			}
		}
		return true
	})

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !httpMethods[sel.Sel.Name] || len(call.Args) < 2 {
			return true
		}
		path, ok := stringLit(call.Args[0])
		if !ok {
			return true // not a route registration shape
		}

		gated := false
		if recv, ok := sel.X.(*ast.Ident); ok && gatedGroups[recv.Name] {
			gated = true
		}
		for _, arg := range call.Args[1:] {
			if callTo(arg, "RequirePermission") {
				gated = true
				break
			}
			if id, ok := arg.(*ast.Ident); ok && gateVars[id.Name] {
				gated = true
				break
			}
		}
		receiverPath := path
		if recv, ok := sel.X.(*ast.Ident); ok {
			receiverPath = recv.Name + "/" + path
		}
		if !gated && !publicPaths[path] && !publicReceiverPaths[receiverPath] {
			out = append(out, fmt.Sprintf("%s %q has no RequirePermission gate and is not allowlisted as public", sel.Sel.Name, path))
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
