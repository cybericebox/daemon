package ociregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/require"
)

const (
	ociManifest = "application/vnd.oci.image.manifest.v1+json"
	ociIndex    = "application/vnd.oci.image.index.v1+json"
	dockerV2    = "application/vnd.docker.distribution.manifest.v2+json"
	dockerList  = "application/vnd.docker.distribution.manifest.list.v2+json"
)

// pushImage puts an image (a config and a manifest of the given media type) into the repository and returns the digest of its
// manifest and of its config.
func pushImage(t *testing.T, c *Client, config []byte, mediaType string) (manifestDigest, configDigest string) {
	t.Helper()
	ctx := context.Background()
	configDigest = digestOf(config)
	require.NoError(t, c.PushBlob(ctx, configDigest, int64(len(config)), bytes.NewReader(config)))
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":%d},"layers":[]}`,
		mediaType, configDigest, len(config)))
	got, err := c.PutManifest(ctx, "m"+configDigest[7:19], mediaType, manifest)
	require.NoError(t, err)
	return got, configDigest
}

func configOf(os, arch, user string) []byte {
	return []byte(fmt.Sprintf(`{"architecture":%q,"os":%q,"config":{"User":%q,"Entrypoint":["/app"]},"rootfs":{"type":"layers","diff_ids":[]}}`, arch, os, user))
}

func pushIndex(t *testing.T, c *Client, mediaType string, entries ...string) string {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"manifests":[%s]}`, mediaType, strings.Join(entries, ",")))
	digest, err := c.PutManifest(context.Background(), "latest", mediaType, body)
	require.NoError(t, err)
	return digest
}

func entry(mediaType, digest, os, arch string) string {
	return fmt.Sprintf(`{"mediaType":%q,"digest":%q,"size":10,"platform":{"os":%q,"architecture":%q}}`, mediaType, digest, os, arch)
}

func TestImageConfigsOfASingleManifest(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	c := newClient(srv, "", "")
	for name, mediaType := range map[string]string{"oci": ociManifest, "docker v2": dockerV2} {
		t.Run(name, func(t *testing.T) {
			digest, _ := pushImage(t, c, configOf("linux", "amd64", "app:web"), mediaType)
			got, err := c.ImageConfigs(context.Background(), digest)
			require.NoError(t, err)
			require.Equal(t, digest, got.Digest)
			require.Len(t, got.Platforms, 1)
			require.Equal(t, "amd64", got.Platforms[0].Arch)
			require.Contains(t, string(got.Platforms[0].Config), `"User":"app:web"`)
		})
	}
}

// An index gives the linux platforms the laboratories run, each with its own config; attestations and other platforms are not
// read.
func TestImageConfigsOfAnIndex(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	c := newClient(srv, "", "")
	amd, _ := pushImage(t, c, configOf("linux", "amd64", "app"), ociManifest)
	arm, _ := pushImage(t, c, configOf("linux", "arm64", "1000"), dockerV2)
	win, _ := pushImage(t, c, configOf("windows", "amd64", "x"), ociManifest)
	attestation, _ := pushImage(t, c, configOf("unknown", "unknown", "x"), ociManifest)
	for name, mediaType := range map[string]string{"oci index": ociIndex, "docker list": dockerList} {
		t.Run(name, func(t *testing.T) {
			indexDigest := pushIndex(t, c, mediaType,
				entry(ociManifest, amd, "linux", "amd64"), entry(dockerV2, arm, "linux", "arm64"),
				entry(ociManifest, win, "windows", "amd64"), entry(ociManifest, attestation, "unknown", "unknown"))
			got, err := c.ImageConfigs(context.Background(), "latest")
			require.NoError(t, err)
			require.Equal(t, indexDigest, got.Digest, "the digest the reference pointed at")
			require.Len(t, got.Platforms, 2)
			byArch := map[string]string{}
			for _, p := range got.Platforms {
				var cfg struct{ Config struct{ User string } }
				require.NoError(t, json.Unmarshal(p.Config, &cfg))
				byArch[p.Arch] = cfg.Config.User
			}
			require.Equal(t, map[string]string{"amd64": "app", "arm64": "1000"}, byArch)
		})
	}

	pushIndex(t, c, ociIndex, entry(ociManifest, win, "windows", "amd64"))
	_, err := c.ImageConfigs(context.Background(), "latest")
	require.ErrorIs(t, err, ErrNoLinuxPlatform)
}

func TestImageConfigsRefuseWhatIsNotAnImage(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	c := newClient(srv, "", "")
	_, err := c.PutManifest(context.Background(), "art", "application/vnd.oci.artifact.manifest.v1+json", []byte(`{"schemaVersion":2}`))
	require.NoError(t, err)
	_, err = c.ImageConfigs(context.Background(), "art")
	require.ErrorIs(t, err, ErrUnsupportedManifest)
	_, err = c.ImageConfigs(context.Background(), "missing")
	require.ErrorIs(t, err, ErrNotFound)
}

// The config is read within a limit, whatever the manifest declares, and a blob that does not match its digest is refused.
func TestImageConfigsLimits(t *testing.T) {
	inner := registry.New()
	big := bytes.Repeat([]byte("x"), MaxConfigBytes+10)
	bigDigest := digestOf(big)
	manifest := func(declared int) []byte {
		return []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"config":{"digest":%q,"size":%d},"layers":[]}`, ociManifest, bigDigest, declared))
	}
	var declared int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/big"):
			w.Header().Set("Content-Type", ociManifest)
			_, _ = w.Write(manifest(declared))
		case strings.HasSuffix(r.URL.Path, "/blobs/"+bigDigest):
			_, _ = w.Write(big)
		case strings.HasSuffix(r.URL.Path, "/manifests/huge"):
			w.Header().Set("Content-Type", ociManifest)
			_, _ = w.Write(bytes.Repeat([]byte(" "), MaxManifestBytes+1))
		default:
			inner.ServeHTTP(w, r)
		}
	}))
	defer srv.Close()
	c := newClient(srv, "", "")

	declared = len(big) // honest and too large: refused before the blob is asked for
	_, err := c.ImageConfigs(context.Background(), "big")
	require.ErrorIs(t, err, ErrBlobTooLarge)
	declared = 10 // a manifest that lies about the size: the read is still bounded
	_, err = c.ImageConfigs(context.Background(), "big")
	require.ErrorIs(t, err, ErrBlobTooLarge)
	_, err = c.ImageConfigs(context.Background(), "huge")
	require.ErrorIs(t, err, ErrManifestTooLarge)
}

// A registry that sends a bearer challenge (Docker Hub, GHCR, Quay) is followed anonymously: the token comes from the realm.
func TestImageConfigsFollowAnAnonymousTokenChallenge(t *testing.T) {
	inner := registry.New()
	var tokenAsked, withToken int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenAsked++
			require.Empty(t, r.Header.Get("Authorization"), "anonymous: no credential goes to the realm")
			_, _ = w.Write([]byte(`{"token":"anon-token"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer anon-token" {
			w.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="fake",scope="repository:acme/platform:pull"`, srv.URL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		withToken++
		inner.ServeHTTP(w, r)
	}))
	defer srv.Close()
	c := newClient(srv, "", "")
	digest, _ := pushImageAuthorized(t, srv, c)
	got, err := c.ImageConfigs(context.Background(), digest)
	require.NoError(t, err)
	require.Len(t, got.Platforms, 1)
	require.Positive(t, tokenAsked)
	require.Positive(t, withToken)
}

// pushImageAuthorized seeds the repository behind the token challenge: the pushes carry the token the test realm hands out.
func pushImageAuthorized(t *testing.T, srv *httptest.Server, c *Client) (string, string) {
	t.Helper()
	config := configOf("linux", "amd64", "app")
	configDigest := digestOf(config)
	do := func(method, path string, body []byte, contentType string) {
		req, err := http.NewRequest(method, srv.URL+path, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer anon-token")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Less(t, resp.StatusCode, 300, path)
	}
	do(http.MethodPost, "/v2/acme/platform/blobs/uploads/?digest="+configDigest, config, "application/octet-stream")
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"config":{"digest":%q,"size":%d},"layers":[]}`, ociManifest, configDigest, len(config)))
	do(http.MethodPut, "/v2/acme/platform/manifests/v1", manifest, ociManifest)
	return digestOf(manifest), configDigest
}

// The reference is author-controlled: a registry at a loopback or private address is never dialled by the guarded transport,
// and a redirect to one is refused.
func TestImageConfigsDoNotReachInternalAddresses(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	guarded := New(Options{Host: hostOf(srv), Repository: "acme/platform", Insecure: true, Transport: GuardedTransport(nil)})
	_, err := guarded.ImageConfigs(context.Background(), "latest")
	require.ErrorIs(t, err, ErrUnreachable)
	require.NotErrorIs(t, err, ErrNotFound, "the registry was never asked")

	target := httptest.NewServer(registry.New())
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirector.Close()
	through := New(Options{Host: hostOf(redirector), Repository: "acme/platform", Insecure: true})
	_, err = through.ImageConfigs(context.Background(), "latest")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotFound)
}
