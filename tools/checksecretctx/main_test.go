package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func check(t *testing.T, src string) []string {
	t.Helper()
	return violations("x.go", "package x\n"+src)
}

func TestFlagsASealWithoutContext(t *testing.T) {
	got := check(t, `func f() { c.Encrypt("v"); c.Decrypt(v) }`)
	if len(got) != 2 {
		t.Fatalf("want 2, got %v", got)
	}
}

func TestFlagsNilEmptyAndHandBuiltContexts(t *testing.T) {
	for name, src := range map[string]string{
		"nil":          `func f() { c.EncryptWithContext(p, nil) }`,
		"nil bytes":    `func f() { c.DecryptWithContext(s, []byte(nil)) }`,
		"literal":      `func f() { c.EncryptWithContext(p, []byte("some:ctx")) }`,
		"empty":        `func f() { c.EncryptWithContext(p, []byte{}) }`,
		"concatenated": `func f() { c.EncryptWithContext(p, []byte("a:"+id)) }`,
		"sprintf":      `func f() { c.EncryptWithContext(p, []byte(fmt.Sprintf("a:%s", id))) }`,
	} {
		if got := check(t, src); len(got) != 1 {
			t.Errorf("%s: want 1 violation, got %v", name, got)
		}
	}
}

func TestAllowsBuilderCallsAndVariables(t *testing.T) {
	for name, src := range map[string]string{
		"builder":  `func f() { c.EncryptWithContext(p, secretctx.SMTPPassword(id)) }`,
		"variable": `func f() { c.DecryptWithContext(s, ctx) }`,
		"field":    `func f() { c.DecryptWithContext(s, ticketContext) }`,
		"no args":  `func f() { zw.Encrypt(name, "pw", mode) }`,
	} {
		if got := check(t, src); len(got) != 0 {
			t.Errorf("%s: want none, got %v", name, got)
		}
	}
}

func TestFlagsAContextStringOutsideTheBuilders(t *testing.T) {
	got := check(t, `var s = "mail.smtp:" + id
var t = "vpn-config:%s"
var ok = "unrelated: text"`)
	if len(got) != 2 || !strings.Contains(got[0], "belongs in internal/secretctx") {
		t.Fatalf("want 2, got %v", got)
	}
}

func TestExemptDirectories(t *testing.T) {
	if !exempt("internal/secretctx/secretctx.go") || !exempt("pkg/secret/secret.go") || exempt("internal/useCase/mail/useCase.go") {
		t.Fatal("exempt directories wrong")
	}
}

func TestAMarkedLineIsNotReported(t *testing.T) {
	got := check(t, `var code = "notify-payload:" + id // secretctx:ignore: a lookup code, not a context
var bad = "vpn-config:" + id`)
	if len(got) != 1 || !strings.Contains(got[0], "x.go:3") {
		t.Fatalf("want only the unmarked line, got %v", got)
	}
}

// The repository itself obeys the rule (make vet runs the same check, this keeps it in go test too).
func TestTheRepositoryIsClean(t *testing.T) {
	var found []string
	for _, root := range []string{"../../internal", "../../pkg", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(filepath.ToSlash(path), "../../")
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || exempt(rel) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			found = append(found, violations(rel, string(data))...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(found) > 0 {
		t.Fatalf("secret contexts outside internal/secretctx:\n%s", strings.Join(found, "\n"))
	}
}
