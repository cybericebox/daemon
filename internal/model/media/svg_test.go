package mediaModel

import (
	"strings"
	"testing"
)

func TestSanitizeSVGKeepsDrawingAndDropsActiveContent(t *testing.T) {
	dirty := `<?xml version="1.0"?>
<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 10 10" onload="alert(1)">
  <script>alert(1)</script>
  <style>@import url(https://evil.example/x.css);</style>
  <foreignObject><div>html</div></foreignObject>
  <defs><linearGradient id="g"><stop offset="0" stop-color="#fff"/></linearGradient></defs>
  <rect width="10" height="10" fill="url(#g)" onclick="x()"/>
  <path d="M0 0L10 10" style="fill:url(https://evil.example/p)"/>
  <use href="#g"/><use xlink:href="https://evil.example/s.svg#a"/>
  <a href="javascript:alert(1)"><circle r="1"/></a>
  <image href="https://evil.example/i.png"/>
</svg>`
	clean, err := SanitizeSVG([]byte(dirty))
	if err != nil {
		t.Fatalf("SanitizeSVG: %v", err)
	}
	out := string(clean)
	for _, banned := range []string{"script", "alert", "onload", "onclick", "@import", "foreignObject", "evil.example", "<image", "<a", "ENTITY", "passwd"} {
		if strings.Contains(out, banned) {
			t.Fatalf("%q survived: %s", banned, out)
		}
	}
	for _, kept := range []string{`viewBox="0 0 10 10"`, `<rect`, `fill="url(#g)"`, `<path d="M0 0L10 10"`, `<use href="#g"`, `stop-color="#fff"`, `xmlns="http://www.w3.org/2000/svg"`} {
		if !strings.Contains(out, kept) {
			t.Fatalf("%q was dropped: %s", kept, out)
		}
	}
}

func TestSanitizeSVGRejectsNonSVG(t *testing.T) {
	for _, bad := range []string{"<html><body/></html>", "not xml", "<svg><g></svg>", ""} {
		if _, err := SanitizeSVG([]byte(bad)); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if !LooksLikeSVG([]byte("  <svg viewBox='0 0 1 1'/>")) || LooksLikeSVG([]byte{0x89, 'P', 'N', 'G'}) {
		t.Fatal("svg sniff")
	}
}

// The reported PoC: a CSS escape spells url( so the text checks miss it, and the viewer's browser fetches it.
func TestSanitizeSVGDropsCSSEscapedURLs(t *testing.T) {
	for name, style := range map[string]string{
		"escaped url":        `fill:u\72l(https://evil.example/x)`,
		"escaped paren":      `fill:url\28 https://evil.example/x)`,
		"hex escape":         `fill:\75 rl(https://evil.example/x)`,
		"escaped everywhere": `background:\000075rl(//evil.example/x)`,
	} {
		out, err := SanitizeSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1" style="` + style + `"/></svg>`))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(string(out), "style") || strings.Contains(string(out), "evil") {
			t.Errorf("%s: the attribute survived: %s", name, out)
		}
	}
	// A plain local reference still works.
	out, err := SanitizeSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><defs><linearGradient id="g"/></defs><rect fill="url(#g)" width="1" height="1"/></svg>`))
	if err != nil || !strings.Contains(string(out), `fill="url(#g)"`) {
		t.Fatalf("local gradient: %s %v", out, err)
	}
}
