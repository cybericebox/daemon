package imageRegistryUseCase

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/ociregistry/archivetest"
)

// archiveSource serves b and counts how often it was opened.
type archiveSource struct {
	b     []byte
	opens int
}

func (s *archiveSource) open() (io.ReadCloser, error) {
	s.opens++
	return io.NopCloser(bytes.NewReader(s.b)), nil
}

func (f *uploadFixture) index(t *testing.T, src *archiveSource) *ociregistry.ImageArchive {
	t.Helper()
	a, err := f.uc.IndexImageArchive(src.open, int64(len(src.b)))
	require.NoError(t, err)
	return a
}

// expectImageRow expects the platform_images row of an archive image and returns where it is captured.
func (f *uploadFixture) expectImageRow(t *testing.T) *[]postgres.UpsertPlatformImageParams {
	t.Helper()
	var written []postgres.UpsertPlatformImageParams
	f.q.EXPECT().PlatformImageTagTakenByOtherDigest(gomock.Any(), gomock.Any()).Return(false, nil).AnyTimes()
	f.q.EXPECT().UpsertPlatformImage(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.UpsertPlatformImageParams) error {
			written = append(written, p)
			return nil
		}).AnyTimes()
	return &written
}

func archiveImage() archivetest.Image {
	return archivetest.Amd64(layerBytes, []byte("a second fixed layer"))
}

// A legacy docker save archive: every blob is pushed from the archive, the manifest is pushed under the platform
// tag, the image is recorded (no exercise yet: the import creates it afterwards) and the reference returned.
func TestPushImageArchivePushesBlobsAndManifest(t *testing.T) {
	f := newUploadFixture(t, true)
	img := archiveImage()
	src := &archiveSource{b: archivetest.Legacy(img)}
	a := f.index(t, src)
	written := f.expectImageRow(t)

	res, err := f.uc.PushImageArchive(context.Background(), PushArchiveInput{Name: "Web Server", UploadedBy: uploader, Archive: a})
	require.NoError(t, err)

	descs := []Descriptor{}
	for _, l := range a.Layers {
		descs = append(descs, Descriptor{MediaType: l.MediaType, Digest: l.Digest, Size: l.Size})
	}
	m, err := imageRegistryModel.BuildManifest(Descriptor{MediaType: a.Config.MediaType, Digest: a.Config.Digest, Size: a.Config.Size}, descs)
	require.NoError(t, err)
	ref, err := imageRegistryModel.NewPlatformRef("web-server", m.Digest)
	require.NoError(t, err)
	require.Equal(t, ref.String(), res.Reference)
	for _, b := range append([][]byte{img.Config()}, img.Layers...) {
		require.True(t, f.has(t, archivetest.Digest(b)), "blob pushed")
	}
	ok, err := f.registry().ManifestExists(context.Background(), ref.Tag())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []postgres.UpsertPlatformImageParams{{
		Digest: m.Digest, Tag: ref.Tag(), Name: "web-server",
		UploadedBy: uuid.NullUUID{UUID: uploader, Valid: true}, UploadedAt: t0,
	}}, *written)
	require.Equal(t, 2, src.opens, "pass 1 and pass 2")
}

// The multi-platform OCI layout pushes the linux/amd64 image.
func TestPushImageArchiveMultiArch(t *testing.T) {
	f := newUploadFixture(t, true)
	arm := archivetest.Image{Layers: [][]byte{[]byte("arm layer")}, OS: "linux", Arch: "arm64"}
	src := &archiveSource{b: archivetest.MultiArch(arm, archiveImage())}
	a := f.index(t, src)
	f.expectImageRow(t)
	_, err := f.uc.PushImageArchive(context.Background(), PushArchiveInput{Name: "web", UploadedBy: uploader, Archive: a})
	require.NoError(t, err)
	require.True(t, f.has(t, digestOf(layerBytes)))
	require.False(t, f.has(t, digestOf([]byte("arm layer"))), "the other platform stays out")
}

// Blobs the registry already has are not sent again: with all of them present the archive is not read twice.
func TestPushImageArchiveSkipsPresentBlobs(t *testing.T) {
	f := newUploadFixture(t, true)
	img := archiveImage()
	f.preload(t, append([][]byte{img.Config()}, img.Layers...)...)
	src := &archiveSource{b: archivetest.OCI(img)}
	a := f.index(t, src)
	f.expectImageRow(t)
	_, err := f.uc.PushImageArchive(context.Background(), PushArchiveInput{Name: "web", UploadedBy: uploader, Archive: a})
	require.NoError(t, err)
	require.Equal(t, 1, src.opens)
}

func unreadableReason(t *testing.T, err error) any {
	t.Helper()
	require.ErrorIs(t, err, imageRegistryModel.ErrImageArchiveUnreadable.Err())
	return publicReason(t, err)[imageRegistryModel.ContextReason]
}

func TestIndexImageArchiveRefusals(t *testing.T) {
	f := newUploadFixture(t, true)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(archivetest.Legacy(archiveImage()))
	require.NoError(t, zw.Close())
	arm := archivetest.Image{Layers: [][]byte{layerBytes}, OS: "linux", Arch: "arm64"}
	for name, tc := range map[string]struct {
		b      []byte
		reason string
	}{
		"gzip":  {gz.Bytes(), "gzip"},
		"arm64": {archivetest.Legacy(arm), "no_amd64"},
		"text":  {bytes.Repeat([]byte("plain text "), 100), "not_tar"},
	} {
		t.Run(name, func(t *testing.T) {
			src := &archiveSource{b: tc.b}
			_, err := f.uc.IndexImageArchive(src.open, int64(len(tc.b)))
			require.Equal(t, tc.reason, unreadableReason(t, err))
		})
	}
	src := &archiveSource{b: archivetest.Legacy(archiveImage())}
	_, err := f.uc.IndexImageArchive(src.open, imageRegistryModel.MaxImageArchiveBytes+1)
	require.Equal(t, "too_large", unreadableReason(t, err))
	require.Zero(t, src.opens)
}

// A source that cannot be read is a platform failure, not the author's archive.
func TestIndexImageArchiveSourceFailure(t *testing.T) {
	f := newUploadFixture(t, true)
	_, err := f.uc.IndexImageArchive(func() (io.ReadCloser, error) { return nil, errors.New("storage down") }, 10)
	require.Error(t, err)
	require.False(t, errors.Is(err, imageRegistryModel.ErrImageArchiveUnreadable.Err()))
}

// A layer whose bytes are not its diff id is refused while it streams: the registry never gets it, nothing is
// recorded or tagged.
func TestPushImageArchiveDigestMismatch(t *testing.T) {
	f := newUploadFixture(t, true)
	img := archiveImage()
	config := img.Config()
	configName := digestOf(config)[len("sha256:"):] + ".json"
	b := archivetest.Tar(
		archivetest.Entry{Name: "l1/layer.tar", Body: []byte("not the first layer")},
		archivetest.Entry{Name: "l2/layer.tar", Body: img.Layers[1]},
		archivetest.Entry{Name: configName, Body: config},
		archivetest.Entry{Name: "manifest.json", Body: []byte(`[{"Config":"` + configName + `","Layers":["l1/layer.tar","l2/layer.tar"]}]`)},
	)
	a := f.index(t, &archiveSource{b: b})
	_, err := f.uc.PushImageArchive(context.Background(), PushArchiveInput{Name: "web", UploadedBy: uploader, Archive: a})
	require.Equal(t, "digest_mismatch", unreadableReason(t, err))
	require.False(t, f.has(t, digestOf(layerBytes)))
}

// The registry not configured: refused before the archive is read again.
func TestPushImageArchiveUnconfigured(t *testing.T) {
	configured := newUploadFixture(t, true)
	src := &archiveSource{b: archivetest.Legacy(archiveImage())}
	a := configured.index(t, src)

	f := newUploadFixture(t, false)
	_, err := f.uc.PushImageArchive(context.Background(), PushArchiveInput{Name: "web", UploadedBy: uploader, Archive: a})
	require.ErrorIs(t, err, imageRegistryModel.ErrRegistryNotConfigured.Err())
	require.Equal(t, 1, src.opens)
}

// A registry failing mid-push is the upload refusal with the reason; nothing is recorded or tagged.
func TestPushImageArchiveRegistryFailure(t *testing.T) {
	f := newUploadFixture(t, true)
	src := &archiveSource{b: archivetest.Legacy(archiveImage())}
	a := f.index(t, src)
	f.reg.mu.Lock()
	f.reg.allow = map[string]bool{} // the push token was revoked
	f.reg.mu.Unlock()
	_, err := f.uc.PushImageArchive(context.Background(), PushArchiveInput{Name: "web", UploadedBy: uploader, Archive: a})
	require.ErrorIs(t, err, imageRegistryModel.ErrRegistryUnavailable.Err())
	require.Equal(t, "unauthorized", publicReason(t, err)[imageRegistryModel.ContextReason])
}

// Inspect uses the canonical manifest tag without any image row/blob/manifest mutation.
func TestInspectImageArchiveIsReadOnlyAndUsesCanonicalManifest(t *testing.T) {
	f := newUploadFixture(t, true)
	src := &archiveSource{b: archivetest.Legacy(archiveImage())}
	indexed := f.index(t, src)
	inspected, err := f.uc.InspectImageArchive(context.Background(), indexed, "web")
	require.NoError(t, err)
	require.False(t, inspected.Reused)
	require.NotEmpty(t, inspected.Digest)
	require.Zero(t, f.reg.putCount())
	require.Empty(t, f.pushes)
	require.Empty(t, f.queued)
	for _, blob := range append([]ociregistry.ArchiveBlob{indexed.Config}, indexed.Layers...) {
		require.False(t, f.has(t, blob.Digest), "inspection must not push a blob")
	}

	manifest, err := imageRegistryModel.BuildManifest(descriptorOf(indexed.Config), []Descriptor{descriptorOf(indexed.Layers[0]), descriptorOf(indexed.Layers[1])})
	require.NoError(t, err)
	ref, err := imageRegistryModel.NewPlatformRef("web", manifest.Digest)
	require.NoError(t, err)
	_, err = f.registry().PutManifest(context.Background(), ref.Tag(), manifest.MediaType, manifest.Body)
	require.NoError(t, err)
	inspected, err = f.uc.InspectImageArchive(context.Background(), indexed, "other-name")
	require.NoError(t, err)
	require.True(t, inspected.Reused)
	require.Equal(t, manifest.Digest, inspected.Digest)
	require.Equal(t, 1, src.opens, "inspection never streams/pushes the tar a second time")
}
