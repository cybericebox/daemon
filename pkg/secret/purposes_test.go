package secret

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"testing"
)

// Every constant of type Purpose in master.go is in AllPurposes, and the other way round: a purpose cannot be added
// without `keys check` being told about it.
func TestAllPurposesListsEveryPurposeConstant(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "master.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != "Purpose" {
				continue
			}
			for _, v := range vs.Values {
				if lit, ok := v.(*ast.BasicLit); ok {
					value, _ := strconv.Unquote(lit.Value)
					declared = append(declared, value)
				}
			}
		}
	}
	var listed []string
	for _, p := range AllPurposes {
		listed = append(listed, string(p))
	}
	sort.Strings(declared)
	sort.Strings(listed)
	if len(declared) == 0 || len(declared) != len(listed) {
		t.Fatalf("declared %v, listed %v", declared, listed)
	}
	for i := range declared {
		if declared[i] != listed[i] {
			t.Fatalf("declared %v, listed %v", declared, listed)
		}
	}
}
