package ociregistry

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/require"
)

func TestBlobSizeReadsTheRegistrySize(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	c := newClient(srv, "", "")
	ctx := context.Background()
	layer := []byte("fixed layer bytes for the size check")

	size, ok, err := c.BlobSize(ctx, digestOf(layer))
	require.NoError(t, err)
	require.False(t, ok)
	require.Zero(t, size)

	require.NoError(t, c.PushBlob(ctx, digestOf(layer), int64(len(layer)), bytes.NewReader(layer)))
	size, ok, err = c.BlobSize(ctx, digestOf(layer))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(len(layer)), size)
}

func TestGetBlobReadsVerifiesAndCaps(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	c := newClient(srv, "", "")
	ctx := context.Background()
	config := []byte(`{"architecture":"amd64","os":"linux"}`)
	require.NoError(t, c.PushBlob(ctx, digestOf(config), int64(len(config)), bytes.NewReader(config)))

	got, err := c.GetBlob(ctx, digestOf(config), 1024)
	require.NoError(t, err)
	require.Equal(t, config, got)

	_, err = c.GetBlob(ctx, digestOf(config), int64(len(config))-1)
	require.True(t, errors.Is(err, ErrBlobTooLarge), "err %v", err)

	_, err = c.GetBlob(ctx, "sha256:"+strings.Repeat("0", 64), 1024)
	require.True(t, errors.Is(err, ErrNotFound), "err %v", err)
}
