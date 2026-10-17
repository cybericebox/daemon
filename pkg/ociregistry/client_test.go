package ociregistry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/require"
)

const fakePassword = "s3cr3t-fake-password-0xDEADBEEF"

func digestOf(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

func hostOf(s *httptest.Server) string { return strings.TrimPrefix(s.URL, "http://") }

func newClient(s *httptest.Server, user, pass string) *Client {
	return New(Options{Host: hostOf(s), Repository: "acme/platform", Username: user, Password: pass, Insecure: true})
}

func TestPushAndExists(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	c := newClient(srv, "", "")
	ctx := context.Background()

	config := []byte(`{"architecture":"amd64","os":"linux"}`)
	layer := []byte("fixed layer bytes 0123456789")
	cd, ld := digestOf(config), digestOf(layer)

	ok, err := c.BlobExists(ctx, cd)
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, c.PushBlob(ctx, cd, int64(len(config)), bytes.NewReader(config)))
	require.NoError(t, c.PushBlob(ctx, ld, int64(len(layer)), bytes.NewReader(layer)))

	ok, err = c.BlobExists(ctx, cd)
	require.NoError(t, err)
	require.True(t, ok)

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + cd + `","size":` + "36" + `},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"` + ld + `","size":28}]}`)
	got, err := c.PutManifest(ctx, "demo-0123456789ab", "application/vnd.oci.image.manifest.v1+json", manifest)
	require.NoError(t, err)
	require.Equal(t, digestOf(manifest), got)

	ok, err = c.ManifestExists(ctx, got)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.ManifestExists(ctx, "sha256:"+strings.Repeat("0", 64))
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, c.Ping(ctx))
}

func basicAuthed(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "bot" || p != fakePassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="fake"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

func TestBasicAuth(t *testing.T) {
	srv := httptest.NewServer(basicAuthed(registry.New()))
	defer srv.Close()
	ctx := context.Background()

	require.NoError(t, newClient(srv, "bot", fakePassword).Ping(ctx))

	bad := newClient(srv, "bot", fakePassword+"x")
	for name, err := range map[string]error{
		"ping": bad.Ping(ctx),
		"push": bad.PushBlob(ctx, digestOf([]byte("a")), 1, strings.NewReader("a")),
	} {
		require.ErrorIs(t, err, ErrUnauthorized, name)
		require.NotContains(t, err.Error(), fakePassword, name)
	}
	_, err := bad.BlobExists(ctx, digestOf([]byte("a")))
	require.ErrorIs(t, err, ErrUnauthorized)
	require.NotContains(t, err.Error(), fakePassword)
}

func TestRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := newClient(srv, "bot", fakePassword)
	require.ErrorIs(t, c.Ping(context.Background()), ErrRateLimited)
	_, err := c.BlobExists(context.Background(), digestOf([]byte("a")))
	require.ErrorIs(t, err, ErrRateLimited)
	require.NotContains(t, err.Error(), fakePassword)
}

func TestUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	host := hostOf(srv)
	srv.Close()
	c := New(Options{Host: host, Repository: "a/b", Username: "bot", Password: fakePassword, Insecure: true})
	err := c.Ping(context.Background())
	require.ErrorIs(t, err, ErrUnreachable)
	require.NotContains(t, err.Error(), fakePassword)
}
