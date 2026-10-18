package ociregistry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

// bearerFake is a registry behind a token server on the same host. Accounts: "writer" (push+pull),
// "reader" (pull only), "stranger" (a valid account without access to any repository: /v2/ answers,
// every repository request is denied); any other account or a wrong password gets 401 from the token
// endpoint.
func bearerFake(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var scopes []string
	inner := registry.New()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || p != fakePassword || (u != "writer" && u != "reader" && u != "stranger") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		scopes = append(scopes, r.URL.Query().Get("scope"))
		_, _ = w.Write([]byte(`{"token":"` + u + `-token"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("Authorization")
		if tok == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="fake"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if tok == "Bearer stranger-token" && r.URL.Path != "/v2/" {
			w.WriteHeader(http.StatusForbidden)
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

func TestPingProvesPushPermission(t *testing.T) {
	srv, scopes := bearerFake(t)
	ctx := context.Background()

	require.NoError(t, newClient(srv, "writer", fakePassword).Ping(ctx))
	require.Contains(t, *scopes, "repository:acme/platform:push,pull")

	err := newClient(srv, "reader", fakePassword).Ping(ctx)
	require.ErrorIs(t, err, ErrUnauthorized)
	require.NotContains(t, err.Error(), fakePassword)

	err = newClient(srv, "writer", fakePassword+"x").Ping(ctx)
	require.ErrorIs(t, err, ErrUnauthorized)
	require.NotContains(t, err.Error(), fakePassword)

	// a pull token passes the pull check
	require.NoError(t, newClient(srv, "reader", fakePassword).PingPull(ctx))
	err = newClient(srv, "reader", fakePassword+"x").PingPull(ctx)
	require.ErrorIs(t, err, ErrUnauthorized)
}

// PingPull reads the repository itself: an account the registry knows but that cannot read the
// repository is refused; an empty repository (404 with an accepted token) passes.
func TestPingPullProvesTheRepositoryCanBeRead(t *testing.T) {
	srv, scopes := bearerFake(t)
	ctx := context.Background()

	err := newClient(srv, "stranger", fakePassword).PingPull(ctx)
	require.ErrorIs(t, err, ErrUnauthorized)
	require.NotContains(t, err.Error(), fakePassword)

	// empty repository: the registry answers 404 to an accepted token
	require.NoError(t, newClient(srv, "reader", fakePassword).PingPull(ctx))
	require.Contains(t, *scopes, "repository:acme/platform:pull")

	// with content
	w := newClient(srv, "writer", fakePassword)
	cfg := []byte(`{"architecture":"amd64","os":"linux"}`)
	require.NoError(t, w.PushBlob(ctx, digestOf(cfg), int64(len(cfg)), bytes.NewReader(cfg)))
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + digestOf(cfg) + `","size":36},"layers":[]}`)
	_, err = w.PutManifest(ctx, "demo-0123456789ab", "application/vnd.oci.image.manifest.v1+json", manifest)
	require.NoError(t, err)
	require.NoError(t, newClient(srv, "reader", fakePassword).PingPull(ctx))
}

// A reference the platform's own parser accepts but go-containerregistry refuses (library/x:y from
// "x:y:z") is an invalid reference, not an unreachable registry.
func TestInvalidRepositoryIsAnInvalidReference(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	c := New(Options{Host: hostOf(srv), Repository: "library/x:y", Insecure: true})
	ctx := context.Background()
	_, err := c.ManifestExists(ctx, "z")
	require.ErrorIs(t, err, ErrInvalidReference)
	require.NotErrorIs(t, err, ErrUnreachable)
	require.ErrorIs(t, c.Ping(ctx), ErrInvalidReference)
	require.ErrorIs(t, c.PingPull(ctx), ErrInvalidReference)
}

func TestPingCancelledKeepsContextError(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := newClient(srv, "", "").Ping(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, ErrUnreachable)
}

func TestScrubOnlyPassword(t *testing.T) {
	c := New(Options{Username: "bot", Password: fakePassword})
	require.Equal(t, "user bot with ***", c.scrub("user bot with "+fakePassword))
}

func redirecting(target string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && strings.Contains(r.URL.Path, "/blobs/") {
			http.Redirect(w, r, target+r.URL.Path, http.StatusTemporaryRedirect)
			return
		}
		basicAuthed(registry.New()).ServeHTTP(w, r)
	})
}

func TestCrossHostRedirectCarriesNoCredentials(t *testing.T) {
	var seen []string
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer cdn.Close()
	reg := httptest.NewServer(redirecting(cdn.URL))
	defer reg.Close()

	c := New(Options{Host: hostOf(reg), Repository: "acme/platform", Username: "bot", Password: fakePassword,
		Insecure: true, AllowInternal: true})
	ok, err := c.BlobExists(context.Background(), digestOf([]byte("a")))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{""}, seen)
}

func TestCrossHostRedirectToLoopbackRefusedByDefault(t *testing.T) {
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer cdn.Close()
	reg := httptest.NewServer(redirecting(cdn.URL))
	defer reg.Close()

	c := New(Options{Host: hostOf(reg), Repository: "acme/platform", Username: "bot", Password: fakePassword, Insecure: true})
	_, err := c.BlobExists(context.Background(), digestOf([]byte("a")))
	require.Error(t, err)
	require.NotContains(t, err.Error(), fakePassword)
}

func redirectReq(t *testing.T, raw string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, raw, nil)
	require.NoError(t, err)
	return r
}

func TestCheckRedirectPolicy(t *testing.T) {
	c := New(Options{})
	first := redirectReq(t, "https://registry.example.com/v2/")

	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = first
	}
	require.Error(t, c.checkRedirect(redirectReq(t, "https://registry.example.com/x"), via))
	require.NoError(t, c.checkRedirect(redirectReq(t, "https://registry.example.com/x"), via[:9]))

	// https -> http downgrade
	require.Error(t, c.checkRedirect(redirectReq(t, "http://cdn.example.com/x"), []*http.Request{first}))
	// private / loopback / link-local / unspecified literals
	for _, u := range []string{"https://127.0.0.1/x", "https://10.1.2.3/x", "https://169.254.169.254/x", "https://[::1]/x", "https://0.0.0.0/x"} {
		require.Error(t, c.checkRedirect(redirectReq(t, u), []*http.Request{first}), u)
	}
	// public literal, other host: allowed, credentials stripped
	next := redirectReq(t, "https://93.184.216.34/x")
	next.Header.Set("Authorization", "Basic abc")
	require.NoError(t, c.checkRedirect(next, []*http.Request{first}))
	require.Empty(t, next.Header.Get("Authorization"))
}

func TestPlainHTTPHostIsRefusedWithCredentials(t *testing.T) {
	for _, host := range []string{"localhost:5000", "127.0.0.1:5000", "10.0.0.5"} {
		c := New(Options{Host: host, Repository: "a/b", Username: "u", Password: "p"})
		if err := c.Ping(context.Background()); !errors.Is(err, ErrInvalidReference) {
			t.Errorf("%s: %v", host, err)
		}
	}
}

// PingCredentials proves the account is accepted by the registry without naming a repository (an organizer
// secret may hold a host only): a bad password is ErrUnauthorized, an account without repository access passes.
func TestPingCredentialsChecksTheAccountOnly(t *testing.T) {
	srv, _ := bearerFake(t)
	ctx := context.Background()
	anyRepo := func(user, pass string) *Client {
		return New(Options{Host: hostOf(srv), Username: user, Password: pass, Insecure: true})
	}

	require.NoError(t, anyRepo("reader", fakePassword).PingCredentials(ctx))
	require.NoError(t, anyRepo("stranger", fakePassword).PingCredentials(ctx))

	err := anyRepo("reader", fakePassword+"x").PingCredentials(ctx)
	require.ErrorIs(t, err, ErrUnauthorized)
	require.NotContains(t, err.Error(), fakePassword)
	require.ErrorIs(t, anyRepo("nobody", fakePassword).PingCredentials(ctx), ErrUnauthorized)
}

func TestPingCredentialsClassifiesFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		want   error
	}{
		"revoked":      {http.StatusUnauthorized, ErrUnauthorized},
		"forbidden":    {http.StatusForbidden, ErrUnauthorized},
		"server error": {http.StatusBadGateway, ErrUnreachable},
		"throttled":    {http.StatusTooManyRequests, ErrRateLimited},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status) }))
			defer srv.Close()
			err := New(Options{Host: hostOf(srv), Username: "u", Password: fakePassword, Insecure: true}).PingCredentials(context.Background())
			require.ErrorIs(t, err, tc.want)
			require.NotContains(t, err.Error(), fakePassword)
		})
	}
}

func TestPingCredentialsPlainHTTPHostIsRefusedWithCredentials(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	err := New(Options{Host: hostOf(srv), Username: "u", Password: fakePassword}).PingCredentials(context.Background())
	require.ErrorIs(t, err, ErrInvalidReference)
}
