package imageRegistryModel

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

var (
	t0      = time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	eventID = uuid.Must(uuid.FromString("0199a000-0000-7000-8000-000000000001"))
)

func TestNewEventSecret(t *testing.T) {
	cases := []struct {
		name               string
		host, prefix, user string
		wantHost, wantPref string
		wantErr            bool
	}{
		{"plain", "registry.local:5000", "Acme/", "bob", "registry.local:5000", "acme", false},
		{"hub alias", "index.docker.io", "acme", "bob", "docker.io", "acme", false},
		{"hub alias 2", "Registry-1.Docker.IO", "", "bob", "docker.io", "", false},
		{"scheme", "https://ghcr.io", "", "bob", "", "", true},
		{"path", "ghcr.io/acme", "", "bob", "", "", true},
		{"empty host", "", "", "bob", "", "", true},
		{"bad port", "ghcr.io:abc", "", "bob", "", "", true},
		{"empty user", "ghcr.io", "", "", "", "", true},
		{"bad prefix", "ghcr.io", "a b", "bob", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := NewEventSecret(eventID.String(), c.host, c.prefix, c.user, t0)
			if c.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.Host != c.wantHost || s.RepositoryPrefix != c.wantPref || s.EventID != eventID {
				t.Fatalf("got %+v", s)
			}
			if s.ID == uuid.Nil || !s.CreatedAt.Equal(t0) || !s.UpdatedAt.Equal(t0) {
				t.Fatalf("defaults not set: %+v", s)
			}
		})
	}
	if _, err := NewEventSecret("not-a-uuid", "ghcr.io", "", "bob", t0); err == nil {
		t.Fatal("bad event id must fail")
	}
}

func TestEventSecretMatches(t *testing.T) {
	mk := func(host, prefix string) EventSecret {
		return EventSecret{Host: host, RepositoryPrefix: prefix}
	}
	cases := []struct {
		name   string
		secret EventSecret
		image  string
		ok     bool
	}{
		{"prefix match", mk("docker.io", "acme"), "docker.io/acme/x", true},
		{"prefix evil", mk("docker.io", "acme"), "docker.io/acme-evil/x", false},
		{"prefix exact repo", mk("docker.io", "acme/x"), "acme/x:1", true},
		{"host only", mk("registry.local:5000", ""), "registry.local:5000/a/b", true},
		{"other host", mk("registry.local:5000", ""), "registry.local:5001/a/b", false},
		{"hub alias image", mk("docker.io", ""), "index.docker.io/library/nginx:1", true},
		{"nested prefix", mk("ghcr.io", "acme/team"), "ghcr.io/acme/team/app", true},
		{"partial segment", mk("ghcr.io", "acme/te"), "ghcr.io/acme/team/app", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ref, err := ParseImageRef(c.image)
			if err != nil {
				t.Fatal(err)
			}
			_, ok := c.secret.Matches(ref)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
		})
	}
	short, _ := mk("docker.io", "").Matches(ImageRef{Host: "docker.io", Repository: "acme/x"})
	long, _ := mk("docker.io", "acme").Matches(ImageRef{Host: "docker.io", Repository: "acme/x"})
	if long <= short {
		t.Fatalf("prefix must be more specific than host only: %d <= %d", long, short)
	}
}

func TestClassify(t *testing.T) {
	hostOnly := EventSecret{Host: "docker.io", Username: "host"}
	acme := EventSecret{Host: "docker.io", RepositoryPrefix: "acme", Username: "acme"}
	acmeTeam := EventSecret{Host: "docker.io", RepositoryPrefix: "acme/team", Username: "team"}
	secrets := []EventSecret{hostOnly, acme, acmeTeam}

	cases := []struct {
		name     string
		image    string
		secrets  []EventSecret
		kind     Kind
		username string
	}{
		{"platform", "platform:web@sha256:" + hexA, secrets, KindPlatform, ""},
		{"public no secrets", "nginx", nil, KindPublic, ""},
		{"public other host", "ghcr.io/acme/x", secrets, KindPublic, ""},
		{"host only wins when alone", "nginx", secrets, KindOrganizer, "host"},
		{"longer prefix", "acme/team/app", secrets, KindOrganizer, "team"},
		{"mid prefix", "docker.io/acme/app", secrets, KindOrganizer, "acme"},
		{"evil falls to host", "acme-evil/x", secrets, KindOrganizer, "host"},
		{"evil public", "acme-evil/x", []EventSecret{acme}, KindPublic, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, s := Classify(c.image, c.secrets)
			if kind != c.kind {
				t.Fatalf("kind = %v, want %v", kind, c.kind)
			}
			if c.username == "" {
				if s != nil {
					t.Fatalf("unexpected secret %+v", s)
				}
				return
			}
			if s == nil || s.Username != c.username {
				t.Fatalf("secret = %+v, want %s", s, c.username)
			}
		})
	}
}
