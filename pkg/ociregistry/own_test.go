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

func repoClient(s *httptest.Server, repository string, o Options) *Client {
	o.Host, o.Repository = hostOf(s), repository
	return New(o)
}

// PlainHTTP is the one option that lets credentials travel over plain http: for the platform's own registry.
func TestPlainHTTPAllowsCredentials(t *testing.T) {
	srv := httptest.NewServer(basicAuthed(registry.New()))
	defer srv.Close()
	ctx := context.Background()

	plain := repoClient(srv, "platform/demo", Options{Username: "bot", Password: fakePassword, PlainHTTP: true})
	require.NoError(t, plain.Ping(ctx))
	require.NoError(t, plain.PingCredentials(ctx))
	require.NoError(t, plain.PushBlob(ctx, digestOf([]byte("a")), 1, strings.NewReader("a")))

	// without the option the same address is refused before any request: credentials never go over http
	refused := repoClient(srv, "platform/demo", Options{Username: "bot", Password: fakePassword})
	require.ErrorIs(t, refused.Ping(ctx), ErrInvalidReference)
	require.ErrorIs(t, refused.PingCredentials(ctx), ErrInvalidReference)
	_, err := refused.BlobExists(ctx, digestOf([]byte("a")))
	require.ErrorIs(t, err, ErrInvalidReference)
	require.NotContains(t, err.Error(), fakePassword)

	// RequireHTTPS still wins over PlainHTTP
	both := repoClient(srv, "platform/demo", Options{Username: "bot", Password: fakePassword, PlainHTTP: true, RequireHTTPS: true})
	require.ErrorIs(t, both.Ping(ctx), ErrInvalidReference)
}

// mountingRegistry is the in-memory test registry plus the cross-repository mount it does not implement: a mount
// request for a blob answers 201 when mounts is true, and the registry's own 202 session otherwise. It records
// what was asked.
func mountingRegistry(mounts bool, asked *[]string) http.Handler {
	inner := registry.New()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Query().Get("mount") != "" && mounts {
			*asked = append(*asked, r.URL.Path+"?mount="+r.URL.Query().Get("mount")+"&from="+r.URL.Query().Get("from"))
			w.WriteHeader(http.StatusCreated)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

func TestMountBlobBetweenRepositories(t *testing.T) {
	ctx := context.Background()
	digest := digestOf([]byte("layer bytes for the mount test"))

	var asked []string
	srv := httptest.NewServer(mountingRegistry(true, &asked))
	mounted, err := repoClient(srv, "platform/demo", Options{PlainHTTP: true}).MountBlob(ctx, digest, "platform/blob--staging")
	srv.Close()
	require.NoError(t, err)
	require.True(t, mounted)
	require.Equal(t, []string{"/v2/platform/demo/blobs/uploads/?mount=" + digest + "&from=platform/blob--staging"}, asked)

	// a registry that does not mount opens an upload session (202): not mounted, no error
	srv = httptest.NewServer(mountingRegistry(false, &asked))
	defer srv.Close()
	mounted, err = repoClient(srv, "platform/demo", Options{PlainHTTP: true}).MountBlob(ctx, digest, "platform/blob--staging")
	require.NoError(t, err)
	require.False(t, mounted)
}

func TestMountBlobFailureIsClassified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer srv.Close()
	_, err := repoClient(srv, "platform/demo", Options{PlainHTTP: true}).MountBlob(context.Background(), digestOf([]byte("x")), "platform/other")
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestDeleteManifest(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	ctx := context.Background()
	c := repoClient(srv, "platform/demo", Options{PlainHTTP: true})

	config := []byte(`{"architecture":"amd64","os":"linux"}`)
	cd := digestOf(config)
	require.NoError(t, c.PushBlob(ctx, cd, int64(len(config)), bytes.NewReader(config)))
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + cd + `","size":36},"layers":[]}`)
	got, err := c.PutManifest(ctx, "sha256-tag", "application/vnd.oci.image.manifest.v1+json", manifest)
	require.NoError(t, err)

	require.NoError(t, c.DeleteManifest(ctx, got))
	ok, err := c.ManifestExists(ctx, got)
	require.NoError(t, err)
	require.False(t, ok)
	// deleting again: the manifest is not there
	require.ErrorIs(t, c.DeleteManifest(ctx, got), ErrNotFound)
}
