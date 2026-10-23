package ociregistry

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/require"
)

// scopedTokenFake is a registry behind a token server that refuses (401) a token for a scope its account may not
// have, as Docker Hub does for a read-only access token asked for push ("access token has insufficient scopes").
// Accounts: "writer" (push+pull) and "reader" (pull only).
func scopedTokenFake(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var scopes []string
	inner := registry.New()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		scope := r.URL.Query().Get("scope")
		if !ok || p != fakePassword || (u != "writer" && u != "reader") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scopes = append(scopes, u+" "+scope)
		if u == "reader" && strings.Contains(scope, "push") {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"details":"access token has insufficient scopes"}`))
			return
		}
		_, _ = w.Write([]byte(`{"token":"` + u + `-token"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("Authorization")
		if tok == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="fake"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if tok == "Bearer reader-token" && r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		inner.ServeHTTP(w, r)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &scopes
}

// ManifestDigest is a read: a pull-only credential sees an existing manifest (and a missing one as not found)
// instead of being refused a push token. The 2.19.0 import preview called it with the pull token and push scope,
// and every import with images failed as "registry unavailable".
func TestManifestDigestNeedsOnlyPull(t *testing.T) {
	srv, scopes := scopedTokenFake(t)
	ctx := context.Background()

	cfg := []byte(`{"architecture":"amd64","os":"linux"}`)
	w := newClient(srv, "writer", fakePassword)
	require.NoError(t, w.PushBlob(ctx, digestOf(cfg), int64(len(cfg)), bytes.NewReader(cfg)))
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + digestOf(cfg) + `","size":36},"layers":[]}`)
	_, err := w.PutManifest(ctx, "sha256-0123456789ab", "application/vnd.oci.image.manifest.v1+json", manifest)
	require.NoError(t, err)

	r := newClient(srv, "reader", fakePassword)
	have, found, err := r.ManifestDigest(ctx, "sha256-0123456789ab")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, digestOf(manifest), have)

	_, found, err = r.ManifestDigest(ctx, "sha256-ffffffffffff")
	require.NoError(t, err)
	require.False(t, found)

	for _, s := range *scopes {
		if strings.HasPrefix(s, "reader ") {
			require.Equal(t, "reader repository:acme/platform:pull", s, "a manifest read asks for pull only")
		}
	}

	// the push credential still reads it (the image push checks the tag with it)
	have, found, err = w.ManifestDigest(ctx, "sha256-0123456789ab")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, digestOf(manifest), have)

	// a credential the registry refuses is still ErrUnauthorized, never a found/missing answer
	_, _, err = newClient(srv, "reader", fakePassword+"x").ManifestDigest(ctx, "sha256-0123456789ab")
	require.ErrorIs(t, err, ErrUnauthorized)
	require.NotContains(t, err.Error(), fakePassword)
}
