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
