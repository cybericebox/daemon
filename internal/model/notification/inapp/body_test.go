package inAppModel_test

import (
	"strings"
	"testing"

	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
)

// The reported gap: an in-app body was trusted HTML, defended only by the browser's sanitizer.
func TestSanitizeBodyKeepsTheEditorFormatAndNothingElse(t *testing.T) {
	kept := `<strong>Hi</strong> <em>there</em><br><span style="font-family:Georgia">x</span>`
	if got := inAppModel.SanitizeBody(kept); got != kept {
		t.Fatalf("the editor's own format must survive: %q", got)
	}
	for name, evil := range map[string]string{
		"script":        `<script>alert(1)</script>ok`,
		"link":          `<a href="https://evil.example/">click</a>`,
		"tracking img":  `<img src="https://evil.example/p.gif">`,
		"form":          `<form action="https://evil.example"><input name="pw"></form>`,
		"event handler": `<strong onclick="x()">b</strong>`,
		"iframe":        `<iframe src="https://evil.example"></iframe>`,
		"other style":   `<span style="background:url(https://evil.example/x)">x</span>`,
		"bad font":      `<span style="font-family:evil">x</span>`,
		"svg":           `<svg onload="x()"></svg>`,
		"js url":        `<a href="javascript:alert(1)">x</a>`,
	} {
		got := inAppModel.SanitizeBody(evil)
		for _, bad := range []string{"<script", "<a ", "<img", "<form", "<input", "onclick", "<iframe", "evil", "<svg", "javascript:", "url("} {
			if strings.Contains(got, bad) {
				t.Errorf("%s: %q survived in %q", name, bad, got)
			}
		}
	}
}
