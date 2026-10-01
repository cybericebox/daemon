package event

import (
	"errors"
	"strings"
	"testing"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

func TestLiveLogoTypeSniffsContent(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if kind, _, err := liveLogoType(png); err != nil || kind != "image/png" {
		t.Fatalf("png = %q, %v", kind, err)
	}
	webp := []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")
	if kind, _, err := liveLogoType(webp); err != nil || kind != "image/webp" {
		t.Fatalf("webp = %q, %v", kind, err)
	}
	kind, clean, err := liveLogoType([]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="x()"><script>x()</script><rect width="1" height="1"/></svg>`))
	if err != nil || kind != "image/svg+xml" || strings.Contains(string(clean), "script") || strings.Contains(string(clean), "onload") {
		t.Fatalf("svg = %q %s, %v", kind, clean, err)
	}
	for _, bad := range [][]byte{[]byte("GIF89a...."), []byte("\xff\xd8\xff\xe0 jpeg"), []byte("<html></html>"), []byte("plain text")} {
		if _, _, err := liveLogoType(bad); !errors.Is(err, eventContentModel.ErrLiveLogoTypeInvalid.Err()) {
			t.Fatalf("%q accepted: %v", bad, err)
		}
	}
}
