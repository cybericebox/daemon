package imageRegistryUseCase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	appErr "github.com/cybericebox/daemon/pkg/err"
	"github.com/cybericebox/daemon/pkg/ociregistry/archivetest"
)

// importedImage pushes a legacy docker save archive through the archive import and returns its reference.
func (f *uploadFixture) importedImage(t *testing.T, name string, img archivetest.Image) string {
	t.Helper()
	src := &archiveSource{b: archivetest.Legacy(img)}
	a := f.index(t, src)
	f.expectImageRow(t)
	res, err := f.uc.PushImageArchive(context.Background(), PushArchiveInput{Name: name, UploadedBy: uploader, Archive: a})
	require.NoError(t, err)
	return res.Reference
}

func notExportableReason(t *testing.T, err error) any {
	t.Helper()
	require.ErrorIs(t, err, imageRegistryModel.ErrImageNotExportable.Err())
	return publicReason(t, err)[imageRegistryModel.ContextReason]
}

// Round trip: an image of the platform registry exported as an image archive and imported into another registry
// gets the same manifest digest, so the device reference is the same image. The export pulls with the pull token.
func TestImageExportRoundTrip(t *testing.T) {
	from := newUploadFixture(t, true)
	original := from.importedImage(t, "web", archiveImage())
	ref, ok := imageRegistryModel.ParsePlatformRef(original)
	require.True(t, ok)

	from.reg.mu.Lock()
	from.reg.seen = nil
	from.reg.mu.Unlock()
	export, err := from.uc.ResolveImageExport(context.Background(), original)
	require.NoError(t, err)
	var tarball bytes.Buffer
	require.NoError(t, export.WriteTo(context.Background(), &tarball))
	require.Equal(t, export.Size(), int64(tarball.Len()))
	require.Equal(t, ref.Digest, export.Digest())
	for _, password := range from.reg.passwords() {
		require.Equal(t, pullToken, password, "the export pulls with the pull token")
	}

	to := newUploadFixture(t, true)
	reimported := to.importedImageFromTar(t, "web", tarball.Bytes())
	again, ok := imageRegistryModel.ParsePlatformRef(reimported)
	require.True(t, ok)
	require.Equal(t, ref.Digest, again.Digest, "the same manifest digest after export and import")

	var second bytes.Buffer
	require.NoError(t, export.WriteTo(context.Background(), &second))
	require.Equal(t, tarball.Bytes(), second.Bytes(), "deterministic")
}

func (f *uploadFixture) importedImageFromTar(t *testing.T, name string, b []byte) string {
	t.Helper()
	src := &archiveSource{b: b}
	a := f.index(t, src)
	f.expectImageRow(t)
	res, err := f.uc.PushImageArchive(context.Background(), PushArchiveInput{Name: name, UploadedBy: uploader, Archive: a})
	require.NoError(t, err)
	return res.Reference
}

// The registry not configured: not exportable, reason not_configured.
func TestImageExportUnconfigured(t *testing.T) {
	f := newUploadFixture(t, false)
	_, err := f.uc.ResolveImageExport(context.Background(), "platform:web@sha256:"+strings.Repeat("a", 64))
	require.Equal(t, "not_configured", notExportableReason(t, err))
}

// An image the registry does not have, a reference that is not a platform one, a revoked token.
func TestImageExportUnavailable(t *testing.T) {
	f := newUploadFixture(t, true)
	_, err := f.uc.ResolveImageExport(context.Background(), "platform:web@sha256:"+strings.Repeat("a", 64))
	require.Equal(t, "not_found", notExportableReason(t, err))

	_, err = f.uc.ResolveImageExport(context.Background(), "nginx:1.27")
	require.Equal(t, "invalid_reference", notExportableReason(t, err))

	ref := f.importedImage(t, "web", archiveImage())
	f.reg.mu.Lock()
	f.reg.allow = map[string]bool{pushToken: true}
	f.reg.mu.Unlock()
	_, err = f.uc.ResolveImageExport(context.Background(), ref)
	require.Equal(t, "unauthorized", notExportableReason(t, err))
	require.NotContains(t, err.Error(), pullToken)
}

// A layer lost from the registry after the manifest was read: the write fails as not exportable (not_found); a
// failing destination is returned as it is (not a registry error).
func TestImageExportWriteFailures(t *testing.T) {
	f := newUploadFixture(t, true)
	ref := f.importedImage(t, "web", archiveImage())
	export, err := f.uc.ResolveImageExport(context.Background(), ref)
	require.NoError(t, err)

	sink := errors.New("storage pipe closed")
	err = export.WriteTo(context.Background(), failingWriter{err: sink})
	require.ErrorIs(t, err, sink)
	var typed appErr.Error
	require.False(t, errors.As(err, &typed), "a destination failure is not told as the registry's")

	f.reg.mu.Lock()
	f.reg.gone[strings.TrimPrefix(digestOf(layerBytes), "sha256:")] = true
	f.reg.mu.Unlock()
	err = export.WriteTo(context.Background(), io.Discard)
	require.Equal(t, "not_found", notExportableReason(t, err))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = export.WriteTo(ctx, io.Discard)
	require.ErrorIs(t, err, context.Canceled)
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }
