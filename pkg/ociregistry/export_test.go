package ociregistry

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/require"
)

// exportImage is a fixed image held in memory: config, two layers (one of them twice) and its manifest.
type exportImage struct {
	config, layer1, layer2 []byte
	manifest               []byte
	mediaType              string
}

func newExportImage(mediaType string) exportImage {
	img := exportImage{
		config:    []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`),
		layer1:    []byte("fixed export layer one"),
		layer2:    []byte("fixed export layer two, a little longer"),
		mediaType: mediaType,
	}
	configType, layerType := "application/vnd.oci.image.config.v1+json", "application/vnd.oci.image.layer.v1.tar"
	if mediaType == "application/vnd.docker.distribution.manifest.v2+json" {
		configType, layerType = "application/vnd.docker.container.image.v1+json", "application/vnd.docker.image.rootfs.diff.tar.gzip"
	}
	img.manifest = []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"config":{"mediaType":%q,"digest":%q,"size":%d},"layers":[`+
		`{"mediaType":%q,"digest":%q,"size":%d},{"mediaType":%q,"digest":%q,"size":%d},{"mediaType":%q,"digest":%q,"size":%d}]}`,
		mediaType, configType, digestOf(img.config), len(img.config),
		layerType, digestOf(img.layer1), len(img.layer1),
		layerType, digestOf(img.layer2), len(img.layer2),
		layerType, digestOf(img.layer1), len(img.layer1)))
	return img
}

func (i exportImage) blobs() map[string][]byte {
	return map[string][]byte{digestOf(i.config): i.config, digestOf(i.layer1): i.layer1, digestOf(i.layer2): i.layer2}
}

// opener serves blobs from memory and counts the opens per digest.
func (i exportImage) opener(opens map[string]int) BlobOpener {
	blobs := i.blobs()
	return func(digest string) (io.ReadCloser, error) {
		opens[digest]++
		b, ok := blobs[digest]
		if !ok {
			return nil, fmt.Errorf("get blob: %w (status 404)", ErrNotFound)
		}
		return io.NopCloser(bytes.NewReader(b)), nil
	}
}

func (i exportImage) layout(t *testing.T) ImageLayout {
	t.Helper()
	l, err := ParseImageLayout(i.mediaType, i.manifest, "web:0123456789ab")
	require.NoError(t, err)
	return l
}

// tarEntries reads a tar into name → body, keeping the order and the headers.
func tarEntries(t *testing.T, b []byte) ([]string, map[string][]byte, map[string]*tar.Header) {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(b))
	var names []string
	bodies, headers := map[string][]byte{}, map[string]*tar.Header{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		names = append(names, h.Name)
		bodies[h.Name], headers[h.Name] = body, h
	}
	return names, bodies, headers
}

func TestParseImageLayout(t *testing.T) {
	img := newExportImage("application/vnd.oci.image.manifest.v1+json")
	l := img.layout(t)
	require.Equal(t, digestOf(img.manifest), l.ManifestDigest)
	require.Equal(t, ExportBlob{Digest: digestOf(img.config), Size: int64(len(img.config))}, l.Config)
	require.Len(t, l.Layers, 3)
	require.Equal(t, digestOf(img.layer2), l.Layers[1].Digest)

	// The media type may come from the body alone.
	l2, err := ParseImageLayout("", img.manifest, "web:x")
	require.NoError(t, err)
	require.Equal(t, img.mediaType, l2.ManifestType)

	for name, body := range map[string]string{
		"index":        `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`,
		"no config":    `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[]}`,
		"bad digest":   `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"sha256:xyz","size":2},"layers":[]}`,
		"bad size":     `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"` + digestOf(img.config) + `","size":-1},"layers":[]}`,
		"not json":     `nope`,
		"schema v1":    `{"schemaVersion":1,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`,
		"layer digest": `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"` + digestOf(img.config) + `","size":2},"layers":[{"digest":"md5:00","size":1}]}`,
	} {
		_, err := ParseImageLayout("", []byte(body), "web:x")
		require.ErrorIs(t, err, ErrNotSingleImage, name)
	}
}

// The tar is an OCI image layout plus a docker load manifest.json: oci-layout, index.json naming the manifest with
// the ref name, manifest.json (Config, RepoTags, Layers as blob paths), every blob once; fixed headers; exactly
// Size() bytes; the same bytes every time.
func TestWriteImageLayoutTar(t *testing.T) {
	for _, mediaType := range []string{"application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json"} {
		t.Run(mediaType, func(t *testing.T) {
			img := newExportImage(mediaType)
			l := img.layout(t)
			opens := map[string]int{}
			var out bytes.Buffer
			require.NoError(t, l.WriteTo(&out, img.opener(opens)))
			require.Equal(t, l.Size(), int64(out.Len()), "Size is the exact tar length")
			require.Equal(t, map[string]int{digestOf(img.config): 1, digestOf(img.layer1): 1, digestOf(img.layer2): 1}, opens, "each blob read once")

			names, bodies, headers := tarEntries(t, out.Bytes())
			blob := func(b []byte) string { return "blobs/sha256/" + strings.TrimPrefix(digestOf(b), "sha256:") }
			require.Equal(t, []string{
				"oci-layout", "index.json", "manifest.json", "blobs/", "blobs/sha256/",
				blob(img.manifest), blob(img.config), blob(img.layer1), blob(img.layer2),
			}, names)
			require.JSONEq(t, `{"imageLayoutVersion":"1.0.0"}`, string(bodies["oci-layout"]))
			require.JSONEq(t, fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[`+
				`{"mediaType":%q,"digest":%q,"size":%d,"annotations":{"io.containerd.image.name":"docker.io/library/web:0123456789ab","org.opencontainers.image.ref.name":"0123456789ab"}}]}`,
				mediaType, digestOf(img.manifest), len(img.manifest)), string(bodies["index.json"]))
			require.JSONEq(t, fmt.Sprintf(`[{"Config":%q,"RepoTags":["web:0123456789ab"],"Layers":[%q,%q,%q]}]`,
				blob(img.config), blob(img.layer1), blob(img.layer2), blob(img.layer1)), string(bodies["manifest.json"]))
			require.Equal(t, img.manifest, bodies[blob(img.manifest)])
			require.Equal(t, img.layer2, bodies[blob(img.layer2)])
			for name, h := range headers {
				require.Zero(t, h.ModTime.Unix(), name)
				require.Zero(t, h.Uid, name)
				if strings.HasSuffix(name, "/") {
					require.Equal(t, byte(tar.TypeDir), h.Typeflag, name)
					require.Equal(t, int64(0o755), h.Mode, name)
				} else {
					require.Equal(t, byte(tar.TypeReg), h.Typeflag, name)
					require.Equal(t, int64(0o644), h.Mode, name)
				}
			}

			var again bytes.Buffer
			require.NoError(t, l.WriteTo(&again, img.opener(map[string]int{})))
			require.Equal(t, out.Bytes(), again.Bytes(), "deterministic")
		})
	}
}

// The written tar is read back by the archive import reader: the same config and layers.
func TestImageLayoutTarReadsBack(t *testing.T) {
	img := newExportImage("application/vnd.oci.image.manifest.v1+json")
	l := img.layout(t)
	var out bytes.Buffer
	require.NoError(t, l.WriteTo(&out, img.opener(map[string]int{})))
	b := out.Bytes()
	a, err := ReadImageArchive(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }, int64(len(b)), int64(len(b)))
	require.NoError(t, err)
	require.Equal(t, digestOf(img.config), a.Config.Digest)
	require.Equal(t, "application/vnd.oci.image.config.v1+json", a.Config.MediaType)
	var layers []string
	for _, layer := range a.Layers {
		layers = append(layers, layer.Digest)
	}
	require.Equal(t, []string{digestOf(img.layer1), digestOf(img.layer2), digestOf(img.layer1)}, layers)
}

// failingWriter fails every write.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestWriteImageLayoutTarFailures(t *testing.T) {
	img := newExportImage("application/vnd.oci.image.manifest.v1+json")
	l := img.layout(t)

	// A blob whose bytes are not its digest is never written whole as that blob.
	blobs := img.blobs()
	blobs[digestOf(img.layer2)] = []byte("fixed export layer two, a little LONGER")
	tampered := func(digest string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(blobs[digest])), nil }
	err := l.WriteTo(io.Discard, tampered)
	require.ErrorIs(t, err, ErrUnreachable)
	require.Contains(t, err.Error(), "digest")

	// Shorter than the manifest says.
	blobs[digestOf(img.layer2)] = img.layer2[:5]
	err = l.WriteTo(io.Discard, tampered)
	require.ErrorIs(t, err, ErrUnreachable)

	// A missing blob: the opener's error as it is.
	delete(blobs, digestOf(img.layer2))
	missing := func(digest string) (io.ReadCloser, error) {
		if b, ok := blobs[digest]; ok {
			return io.NopCloser(bytes.NewReader(b)), nil
		}
		return nil, fmt.Errorf("get blob: %w (status 404)", ErrNotFound)
	}
	require.ErrorIs(t, l.WriteTo(io.Discard, missing), ErrNotFound)

	// A failing destination: its error as it is, never a registry error.
	sink := errors.New("sink closed")
	err = l.WriteTo(failingWriter{err: sink}, img.opener(map[string]int{}))
	require.ErrorIs(t, err, sink)
	require.NotErrorIs(t, err, ErrUnreachable)

	// A read that fails mid-blob is a registry failure; a cancelled one stays a cancellation.
	broken := func(err error) BlobOpener {
		return func(string) (io.ReadCloser, error) {
			return io.NopCloser(io.MultiReader(strings.NewReader("{"), errReader{err})), nil
		}
	}
	require.ErrorIs(t, l.WriteTo(io.Discard, broken(errors.New("connection reset"))), ErrUnreachable)
	require.ErrorIs(t, l.WriteTo(io.Discard, broken(context.Canceled)), context.Canceled)
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// GetManifest reads a manifest by digest and checks it; OpenBlob streams a blob.
func TestGetManifestAndOpenBlob(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	c := newClient(srv, "", "")
	ctx := context.Background()
	img := newExportImage("application/vnd.oci.image.manifest.v1+json")
	for d, b := range img.blobs() {
		require.NoError(t, c.PushBlob(ctx, d, int64(len(b)), bytes.NewReader(b)))
	}
	digest, err := c.PutManifest(ctx, "web-x", img.mediaType, img.manifest)
	require.NoError(t, err)

	mediaType, body, err := c.GetManifest(ctx, digest, 1<<20)
	require.NoError(t, err)
	require.Equal(t, img.mediaType, mediaType)
	require.Equal(t, img.manifest, body)

	_, _, err = c.GetManifest(ctx, digest, 10)
	require.ErrorIs(t, err, ErrBlobTooLarge)
	_, _, err = c.GetManifest(ctx, "sha256:"+strings.Repeat("0", 64), 1<<20)
	require.ErrorIs(t, err, ErrNotFound)

	rc, size, err := c.OpenBlob(ctx, digestOf(img.layer2))
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.NoError(t, err)
	require.Equal(t, img.layer2, got)
	require.Equal(t, int64(len(img.layer2)), size)

	_, _, err = c.OpenBlob(ctx, "sha256:"+strings.Repeat("0", 64))
	require.ErrorIs(t, err, ErrNotFound)
}

// The manifest JSON a test decodes from index.json keeps the descriptor shape.
func TestImageLayoutIndexIsValidJSON(t *testing.T) {
	img := newExportImage("application/vnd.oci.image.manifest.v1+json")
	var out bytes.Buffer
	require.NoError(t, img.layout(t).WriteTo(&out, img.opener(map[string]int{})))
	_, bodies, _ := tarEntries(t, out.Bytes())
	var index struct {
		Manifests []descriptor `json:"manifests"`
	}
	require.NoError(t, json.Unmarshal(bodies["index.json"], &index))
	require.Len(t, index.Manifests, 1)
}
