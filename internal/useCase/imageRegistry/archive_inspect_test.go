package imageRegistryUseCase

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/require"

	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/ociregistry/archivetest"
)

// hubRegistry behaves like Docker Hub with access tokens: a token server on the same host hands the account
// "acme" a bearer token per scope. The push token gets any scope; the pull token (a read-only access token) is
// refused a token for push with 401 "insufficient scopes" and may only GET/HEAD.
type hubRegistry struct {
	srv *httptest.Server
	mu  sync.Mutex
	// scopes are the "<token> <scope>" pairs asked of the token server.
	scopes []string
	// refuse are tokens the token server refuses for any scope (a revoked token).
	refuse map[string]bool
	// denyRepo answers 403 DENIED to every repository request (a token without access to the repository).
	denyRepo bool
	// down answers 503 to every repository request (an outage).
	down bool
	puts int
}

func newHubRegistry(t *testing.T) *hubRegistry {
	t.Helper()
	h := &hubRegistry{refuse: map[string]bool{}}
	inner := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		scope := r.URL.Query().Get("scope")
		h.mu.Lock()
		defer h.mu.Unlock()
		name := map[string]string{pushToken: "push", pullToken: "pull"}[p]
		h.scopes = append(h.scopes, name+" "+scope)
		switch {
		case !ok || u != "acme" || name == "" || h.refuse[p]:
			w.WriteHeader(http.StatusUnauthorized)
		case name == "pull" && strings.Contains(scope, "push"):
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"details":"access token has insufficient scopes"}`))
		default:
			_, _ = w.Write([]byte(`{"token":"` + name + `-bearer"}`))
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("Authorization")
		if tok == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+h.srv.URL+`/token",service="hub"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h.mu.Lock()
		down, deny := h.down, h.denyRepo
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/manifests/") {
			h.puts++
		}
		h.mu.Unlock()
		repoRequest := r.URL.Path != "/v2/"
		switch {
		case down && repoRequest:
			w.WriteHeader(http.StatusServiceUnavailable)
		case deny && repoRequest:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":[{"code":"DENIED","message":"requested access to the resource is denied"}]}`))
		case tok == "Bearer pull-bearer" && r.Method != http.MethodGet && r.Method != http.MethodHead:
			w.WriteHeader(http.StatusForbidden)
		default:
			inner.ServeHTTP(w, r)
		}
	})
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hubRegistry) newClient(_ imageRegistryModel.Provider, repository, username, password string) *ociregistry.Client {
	return ociregistry.New(ociregistry.Options{
		Host: strings.TrimPrefix(h.srv.URL, "http://"), Repository: repository,
		Username: username, Password: password, Insecure: true, AllowInternal: true,
	})
}

func (h *hubRegistry) set(fn func(h *hubRegistry)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn(h)
}

func (h *hubRegistry) asked() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.scopes...)
}

func (h *hubRegistry) manifestPuts() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.puts
}

// hubFixture is the platform registry on hubRegistry (account acme, repository acme/tasks, push and pull tokens),
// with the image of archiveImage() indexed and its canonical tag and manifest digest.
type hubFixture struct {
	*accessFixture
	hub     *hubRegistry
	indexed *ociregistry.ImageArchive
	tag     string
	digest  string
}

func newHubFixture(t *testing.T) *hubFixture {
	t.Helper()
	hub := newHubRegistry(t)
	f := newAccessFixtureOn(t, true, "dockerhub", func(d *Dependencies) { d.NewClient = hub.newClient })
	src := &archiveSource{b: archivetest.Legacy(archiveImage())}
	indexed, err := f.uc.IndexImageArchive(src.open, int64(len(src.b)))
	require.NoError(t, err)
	layers := make([]Descriptor, 0, len(indexed.Layers))
	for _, l := range indexed.Layers {
		layers = append(layers, descriptorOf(l))
	}
	manifest, err := imageRegistryModel.BuildManifest(descriptorOf(indexed.Config), layers)
	require.NoError(t, err)
	ref, err := imageRegistryModel.NewPlatformRef("library", manifest.Digest)
	require.NoError(t, err)
	return &hubFixture{accessFixture: f, hub: hub, indexed: indexed, tag: ref.Tag(), digest: manifest.Digest}
}

// publish puts the image's manifest under its canonical tag with the push token: an earlier import.
func (f *hubFixture) publish(t *testing.T) {
	t.Helper()
	m, err := imageRegistryModel.BuildManifest(descriptorOf(f.indexed.Config), func() []Descriptor {
		out := []Descriptor{}
		for _, l := range f.indexed.Layers {
			out = append(out, descriptorOf(l))
		}
		return out
	}())
	require.NoError(t, err)
	c := f.hub.newClient("", "acme/tasks", "acme", pushToken)
	_, err = c.PutManifest(context.Background(), f.tag, m.MediaType, m.Body)
	require.NoError(t, err)
}

// The production failure of 2.19.0: re-importing a task whose image is already in a Docker Hub repository, with a
// read-only pull token set. The preview must see the image as present (reused, nothing to push), asking the token
// server for pull only, and never write to the registry.
func TestInspectImageArchiveReusesExistingImageWithPullOnlyToken(t *testing.T) {
	f := newHubFixture(t)
	f.publish(t)
	puts := f.hub.manifestPuts()

	inspected, err := f.uc.InspectImageArchive(context.Background(), f.indexed, "library")
	require.NoError(t, err)
	require.True(t, inspected.Reused, "the same digest under the tag is the same image")
	require.Equal(t, f.digest, inspected.Digest)
	require.Equal(t, puts, f.hub.manifestPuts(), "inspection never writes")
	for _, s := range f.hub.asked() {
		if strings.HasPrefix(s, "pull ") {
			require.NotContains(t, s, "push", "the pull token is never asked for push")
		}
	}
}

// A new image (no manifest under its tag) is reported new, with the pull-only token as well.
func TestInspectImageArchiveNewImageWithPullOnlyToken(t *testing.T) {
	f := newHubFixture(t)
	inspected, err := f.uc.InspectImageArchive(context.Background(), f.indexed, "library")
	require.NoError(t, err)
	require.False(t, inspected.Reused)
	require.Equal(t, f.digest, inspected.Digest)
}

// A pull token the registry refuses falls back to the push token the import will push with.
func TestInspectImageArchiveFallsBackToPushToken(t *testing.T) {
	f := newHubFixture(t)
	f.publish(t)
	f.hub.set(func(h *hubRegistry) { h.refuse[pullToken] = true })

	inspected, err := f.uc.InspectImageArchive(context.Background(), f.indexed, "library")
	require.NoError(t, err)
	require.True(t, inspected.Reused)
}

// Both tokens refused: a precise access refusal naming the token, not "registry unavailable".
func TestInspectImageArchiveTokensRefusedIsAccessDenied(t *testing.T) {
	f := newHubFixture(t)
	f.hub.set(func(h *hubRegistry) { h.refuse[pullToken], h.refuse[pushToken] = true, true })

	_, err := f.uc.InspectImageArchive(context.Background(), f.indexed, "library")
	require.ErrorIs(t, err, imageRegistryModel.ErrRegistryAccessDenied.Err())
	require.NotErrorIs(t, err, imageRegistryModel.ErrRegistryUnavailable.Err())
	ctx := publicReason(t, err)
	require.Equal(t, "push", ctx[imageRegistryModel.ContextToken])
	require.Equal(t, string(imageRegistryModel.ReasonUnauthorized), ctx[imageRegistryModel.ContextReason])
	require.NotContains(t, err.Error(), pushToken)
	require.NotContains(t, err.Error(), pullToken)
}

// A 403 on the repository itself (the token has no access to it) is the same access refusal.
func TestInspectImageArchiveRepository403IsAccessDenied(t *testing.T) {
	f := newHubFixture(t)
	f.hub.set(func(h *hubRegistry) { h.denyRepo = true })

	_, err := f.uc.InspectImageArchive(context.Background(), f.indexed, "library")
	require.ErrorIs(t, err, imageRegistryModel.ErrRegistryAccessDenied.Err())
}

// A real outage stays "registry unavailable" with its reason, and is not retried with the other token.
func TestInspectImageArchiveOutageStaysUnavailable(t *testing.T) {
	f := newHubFixture(t)
	f.hub.set(func(h *hubRegistry) { h.down = true })

	_, err := f.uc.InspectImageArchive(context.Background(), f.indexed, "library")
	require.ErrorIs(t, err, imageRegistryModel.ErrRegistryUnavailable.Err())
	require.Equal(t, string(imageRegistryModel.ReasonUnreachable), publicReason(t, err)[imageRegistryModel.ContextReason])
	require.Equal(t, "pull", publicReason(t, err)[imageRegistryModel.ContextToken])
}

// The import push of an image that already exists: present blobs are not pushed, the tag already serves the
// manifest, so no manifest PUT happens (immutable tags on Docker Hub would refuse it with 403).
func TestPushImageArchiveExistingImageOnHubPushesNothing(t *testing.T) {
	f := newHubFixture(t)
	written := (&uploadFixture{accessFixture: f.accessFixture}).expectImageRow(t)
	ctx := context.Background()
	c := f.hub.newClient("", "acme/tasks", "acme", pushToken)
	// first import: pushes blobs and the manifest
	res, err := f.uc.PushImageArchive(ctx, PushArchiveInput{Name: "library", UploadedBy: uploader, Archive: f.indexed})
	require.NoError(t, err)
	puts := f.hub.manifestPuts()
	require.Equal(t, 1, puts)
	for _, b := range append([]ociregistry.ArchiveBlob{f.indexed.Config}, f.indexed.Layers...) {
		ok, err := c.BlobExists(ctx, b.Digest)
		require.NoError(t, err)
		require.True(t, ok)
	}

	// re-import of the same image: reused in the preview, nothing written on the push
	inspected, err := f.uc.InspectImageArchive(ctx, f.indexed, "library")
	require.NoError(t, err)
	require.True(t, inspected.Reused)
	again, err := f.uc.PushImageArchive(ctx, PushArchiveInput{Name: "library", UploadedBy: uploader, Archive: f.indexed})
	require.NoError(t, err)
	require.Equal(t, res.Reference, again.Reference)
	require.Equal(t, puts, f.hub.manifestPuts(), "the tag already serves the manifest: no second PUT")
	require.NotEmpty(t, *written)
}

// A push token the registry refuses during the import push is the precise access refusal too.
func TestPushImageArchiveRefusedTokenIsAccessDenied(t *testing.T) {
	f := newHubFixture(t)
	f.hub.set(func(h *hubRegistry) { h.refuse[pushToken] = true })
	_, err := f.uc.PushImageArchive(context.Background(), PushArchiveInput{Name: "library", UploadedBy: uploader, Archive: f.indexed})
	require.ErrorIs(t, err, imageRegistryModel.ErrRegistryAccessDenied.Err())
	require.Equal(t, "push", publicReason(t, err)[imageRegistryModel.ContextToken])
}
