package ociregistry_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/ociregistry/archivetest"
)

// maxArchive is the platform cap of one image archive (10 GiB).
const maxArchive = 10 << 30

var (
	layerA = []byte("fixed bytes of layer a")
	layerB = []byte("fixed bytes of layer b, a little longer")
)

// source opens b anew on every call and counts the opens.
func source(b []byte, opens *int) ociregistry.ArchiveSource {
	return func() (io.ReadCloser, error) {
		*opens++
		return io.NopCloser(bytes.NewReader(b)), nil
	}
}

func read(t *testing.T, b []byte) (*ociregistry.ImageArchive, error) {
	t.Helper()
	opens := 0
	return ociregistry.ReadImageArchive(source(b, &opens), int64(len(b)), maxArchive)
}

func archiveCode(t *testing.T, err error) string {
	t.Helper()
	var ae *ociregistry.ArchiveError
	require.True(t, errors.As(err, &ae), "err %v", err)
	return ae.Code
}

// streamed collects every blob Stream hands over.
func streamed(t *testing.T, a *ociregistry.ImageArchive, want map[string]bool) map[string][]byte {
	t.Helper()
	got := map[string][]byte{}
	require.NoError(t, a.Stream(want, func(blob ociregistry.ArchiveBlob, r io.Reader) error {
		b, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		require.Equal(t, blob.Size, int64(len(b)))
		got[blob.Digest] = b
		return nil
	}))
	return got
}

func all(a *ociregistry.ImageArchive) map[string]bool {
	want := map[string]bool{a.Config.Digest: true}
	for _, l := range a.Layers {
		want[l.Digest] = true
	}
	return want
}

func TestReadImageArchiveLegacy(t *testing.T) {
	img := archivetest.Amd64(layerA, layerB)
	b := archivetest.Legacy(img)
	a, err := read(t, b)
	require.NoError(t, err)
	require.Equal(t, archivetest.Digest(img.Config()), a.Config.Digest)
	require.Equal(t, archivetest.OCIConfigType, a.Config.MediaType)
	require.Equal(t, int64(len(img.Config())), a.Config.Size)
	require.Len(t, a.Layers, 2)
	require.Equal(t, archivetest.Digest(layerA), a.Layers[0].Digest)
	require.Equal(t, archivetest.OCILayerType, a.Layers[0].MediaType)
	require.Equal(t, archivetest.Digest(layerB), a.Layers[1].Digest)
	require.Equal(t, archivetest.Digest(b)[len("sha256:"):], a.SHA256, "pass 1 hashes the whole file")

	got := streamed(t, a, all(a))
	require.Equal(t, layerA, got[archivetest.Digest(layerA)])
	require.Equal(t, layerB, got[archivetest.Digest(layerB)])
	require.Equal(t, img.Config(), got[a.Config.Digest])
}

func TestReadImageArchiveOCI(t *testing.T) {
	img := archivetest.Amd64(layerA)
	a, err := read(t, archivetest.OCI(img))
	require.NoError(t, err)
	require.Equal(t, archivetest.Digest(img.Config()), a.Config.Digest)
	require.Len(t, a.Layers, 1)
	require.Equal(t, archivetest.Digest(layerA), a.Layers[0].Digest)
	require.Equal(t, int64(len(layerA)), a.Layers[0].Size)

	// Only the blobs asked for are handed over.
	got := streamed(t, a, map[string]bool{archivetest.Digest(layerA): true})
	require.Len(t, got, 1)
	require.Equal(t, layerA, got[archivetest.Digest(layerA)])
}

func TestReadImageArchiveMultiArchPicksAmd64(t *testing.T) {
	arm := archivetest.Image{Layers: [][]byte{layerB}, OS: "linux", Arch: "arm64"}
	amd := archivetest.Amd64(layerA)
	a, err := read(t, archivetest.MultiArch(arm, amd))
	require.NoError(t, err)
	require.Equal(t, archivetest.Digest(amd.Config()), a.Config.Digest)
	require.Len(t, a.Layers, 1)
	require.Equal(t, archivetest.Digest(layerA), a.Layers[0].Digest)
}

func TestReadImageArchiveRefusals(t *testing.T) {
	arm := archivetest.Image{Layers: [][]byte{layerA}, OS: "linux", Arch: "arm64"}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(archivetest.Legacy(archivetest.Amd64(layerA)))
	require.NoError(t, zw.Close())
	two := archivetest.Tar(
		archivetest.Entry{Name: "manifest.json", Body: []byte(`[{"Config":"a.json","Layers":[]},{"Config":"b.json","Layers":[]}]`)},
	)
	for name, tc := range map[string]struct {
		archive []byte
		code    string
	}{
		"gzip":             {gz.Bytes(), ociregistry.ArchiveGzip},
		"not a tar":        {[]byte(strings.Repeat("not a tar archive ", 64)), ociregistry.ArchiveNotTar},
		"empty":            {nil, ociregistry.ArchiveNotTar},
		"arm64 legacy":     {archivetest.Legacy(arm), ociregistry.ArchiveNoAmd64},
		"arm64 oci":        {archivetest.OCI(arm), ociregistry.ArchiveNoAmd64},
		"arm64 only index": {archivetest.MultiArch(arm), ociregistry.ArchiveNoAmd64},
		"two images":       {two, ociregistry.ArchiveMultiImage},
		"no image":         {archivetest.Tar(archivetest.Entry{Name: "readme.txt", Body: []byte("hi")}), ociregistry.ArchiveNoImage},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := read(t, tc.archive)
			require.Equal(t, tc.code, archiveCode(t, err))
		})
	}
}

func TestReadImageArchiveTooLarge(t *testing.T) {
	b := archivetest.Legacy(archivetest.Amd64(layerA))
	opens := 0
	_, err := ociregistry.ReadImageArchive(source(b, &opens), int64(len(b)), int64(len(b))-1)
	require.Equal(t, ociregistry.ArchiveTooLarge, archiveCode(t, err))
	require.Zero(t, opens, "a too large archive is refused unread")
}

// Long names (PAX) and a hard link to a layer are followed, as the browser reader does.
func TestReadImageArchiveLongNamesAndLinks(t *testing.T) {
	img := archivetest.Amd64(layerA)
	config := img.Config()
	long := strings.Repeat("d", 120) + "/layer.tar"
	configName := archivetest.Digest(config)[len("sha256:"):] + ".json"
	b := archivetest.Tar(
		archivetest.Entry{Name: long, Body: layerA},
		archivetest.Entry{Name: "linked/layer.tar", Type: '1', Linkname: long},
		archivetest.Entry{Name: configName, Body: config},
		archivetest.Entry{Name: "manifest.json", Body: []byte(`[{"Config":"` + configName + `","Layers":["linked/layer.tar"]}]`)},
	)
	a, err := read(t, b)
	require.NoError(t, err)
	require.Equal(t, archivetest.Digest(layerA), a.Layers[0].Digest)
	got := streamed(t, a, all(a))
	require.Equal(t, layerA, got[archivetest.Digest(layerA)])
}

// A failure of the source itself is not a broken archive.
func TestReadImageArchiveSourceFailure(t *testing.T) {
	boom := errors.New("chunk object gone")
	b := archivetest.Legacy(archivetest.Amd64(layerA))
	open := func() (io.ReadCloser, error) {
		return io.NopCloser(io.MultiReader(bytes.NewReader(b[:600]), errReader{boom})), nil
	}
	_, err := ociregistry.ReadImageArchive(open, int64(len(b)), maxArchive)
	require.ErrorIs(t, err, boom)
	var ae *ociregistry.ArchiveError
	require.False(t, errors.As(err, &ae))
}

// Stream stops at the first push failure and returns it.
func TestStreamReturnsThePushFailure(t *testing.T) {
	a, err := read(t, archivetest.Legacy(archivetest.Amd64(layerA, layerB)))
	require.NoError(t, err)
	boom := errors.New("registry down")
	calls := 0
	err = a.Stream(all(a), func(ociregistry.ArchiveBlob, io.Reader) error { calls++; return boom })
	require.ErrorIs(t, err, boom)
	require.Equal(t, 1, calls)
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
