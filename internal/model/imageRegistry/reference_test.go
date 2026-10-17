package imageRegistryModel

import (
	"strings"
	"testing"
)

var hexA = strings.Repeat("a", 64)

func TestPlatformRefRoundTrip(t *testing.T) {
	s := "platform:web@sha256:" + hexA
	ref, ok := ParsePlatformRef(s)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if ref.Name != "web" || ref.Digest != "sha256:"+hexA {
		t.Fatalf("unexpected ref %+v", ref)
	}
	if ref.String() != s {
		t.Fatalf("String = %q", ref.String())
	}
	if ref.Tag() != "web-aaaaaaaaaaaa" {
		t.Fatalf("Tag = %q", ref.Tag())
	}
}

func TestParsePlatformRefInvalid(t *testing.T) {
	cases := map[string]string{
		"empty":         "",
		"no prefix":     "web@sha256:" + hexA,
		"no digest":     "platform:web",
		"short digest":  "platform:web@sha256:" + hexA[:63],
		"upper digest":  "platform:web@sha256:" + strings.Repeat("A", 64),
		"non hex":       "platform:web@sha256:" + strings.Repeat("g", 64),
		"wrong algo":    "platform:web@sha512:" + hexA,
		"upper name":    "platform:Web@sha256:" + hexA,
		"double sep":    "platform:a..b@sha256:" + hexA,
		"leading sep":   "platform:-a@sha256:" + hexA,
		"empty name":    "platform:@sha256:" + hexA,
		"name too long": "platform:" + strings.Repeat("a", 65) + "@sha256:" + hexA,
		"slash name":    "platform:a/b@sha256:" + hexA,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok := ParsePlatformRef(in); ok {
				t.Fatalf("%q must be rejected", in)
			}
		})
	}
}

func TestNewPlatformRef(t *testing.T) {
	if _, err := NewPlatformRef("web", "sha256:"+hexA); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPlatformRef("Web", "sha256:"+hexA); err == nil {
		t.Fatal("bad name must fail")
	}
	if _, err := NewPlatformRef("web", hexA); err == nil {
		t.Fatal("bad digest must fail")
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Web", "web"},
		{"my app", "my-app"},
		{"my  app!!v2", "my-app-v2"},
		{"my.app_v2", "my.app_v2"},
		{"a..b", "a-b"},
		{"--a--", "a"},
		{"", "image"},
		{"???", "image"},
		{"Кіт", "image"},
		{strings.Repeat("a", 70), strings.Repeat("a", 64)},
		{strings.Repeat("a", 63) + "-b", strings.Repeat("a", 63)},
	}
	for _, c := range cases {
		if got := SanitizeName(c.in); got != c.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", c.in, got, c.want)
		}
		if _, err := NewPlatformRef(SanitizeName(c.in), "sha256:"+hexA); err != nil {
			t.Errorf("sanitized %q is not a valid name: %v", c.in, err)
		}
	}
}

func TestParseImageRef(t *testing.T) {
	cases := []struct {
		in   string
		want ImageRef
	}{
		{"nginx", ImageRef{"docker.io", "library/nginx", "latest", ""}},
		{"library/nginx", ImageRef{"docker.io", "library/nginx", "latest", ""}},
		{"docker.io/library/nginx", ImageRef{"docker.io", "library/nginx", "latest", ""}},
		{"index.docker.io/library/nginx:1", ImageRef{"docker.io", "library/nginx", "1", ""}},
		{"registry-1.docker.io/acme/x:2", ImageRef{"docker.io", "acme/x", "2", ""}},
		{"docker.io/nginx", ImageRef{"docker.io", "library/nginx", "latest", ""}},
		{"acme/x", ImageRef{"docker.io", "acme/x", "latest", ""}},
		{"registry.local:5000/a/b", ImageRef{"registry.local:5000", "a/b", "latest", ""}},
		{"ghcr.io/Acme/x:v1", ImageRef{"ghcr.io", "Acme/x", "v1", ""}},
		{"localhost/x", ImageRef{"localhost", "x", "latest", ""}},
		{"nginx@sha256:" + hexA, ImageRef{"docker.io", "library/nginx", "", "sha256:" + hexA}},
		{"ghcr.io/a/b:t@sha256:" + hexA, ImageRef{"ghcr.io", "a/b", "t", "sha256:" + hexA}},
	}
	for _, c := range cases {
		got, err := ParseImageRef(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseImageRefInvalid(t *testing.T) {
	for _, in := range []string{"", " ", "a b", "ghcr.io/", "nginx:", "x@sha256:abc", "https://ghcr.io/a/b", "a//b"} {
		if _, err := ParseImageRef(in); err == nil {
			t.Errorf("%q must be rejected", in)
		}
	}
}
