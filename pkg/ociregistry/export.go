package ociregistry

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// Writing one image of the registry as an image archive (a task archive exported «з образами»): an OCI image
// layout tar (oci-layout, index.json, blobs/sha256/*) with a docker load manifest.json next to it, so both
// `docker load` and the archive import (ReadImageArchive) accept it. The blobs are streamed from the registry
// straight into the tar (their sizes come from the manifest, so every header is written before its bytes) and
// checked against their digests on the way. The output is fixed by the manifest alone: entry order, headers
// (mode, zero time and owner) and JSON are the same every time.

// ErrNotSingleImage: the manifest is not one image (an index, another schema, or invalid descriptors).
var ErrNotSingleImage = errors.New("registry manifest is not a single image")

// ExportBlob is one blob of an exported image.
type ExportBlob struct {
	Digest string
	Size   int64
}

// ImageLayout is one image as its archive is written: the manifest (as the registry holds it), its config and
// layers in manifest order, and the tag docker load gives it ("<name>:<tag>").
type ImageLayout struct {
	ManifestType   string
	Manifest       []byte
	ManifestDigest string
	Config         ExportBlob
	Layers         []ExportBlob
	RepoTag        string
}

// BlobOpener opens a blob by digest; the caller closes it.
type BlobOpener func(digest string) (io.ReadCloser, error)

const (
	ociManifestType    = "application/vnd.oci.image.manifest.v1+json"
	dockerManifestType = "application/vnd.docker.distribution.manifest.v2+json"
	ociIndexType       = "application/vnd.oci.image.index.v1+json"
)

// ParseImageLayout reads a single-image manifest (OCI or Docker v2; mediaType "" takes the body's). Anything else
// is ErrNotSingleImage.
func ParseImageLayout(mediaType string, manifest []byte, repoTag string) (ImageLayout, error) {
	var doc struct {
		SchemaVersion int        `json:"schemaVersion"`
		MediaType     string     `json:"mediaType"`
		Config        *rawBlob   `json:"config"`
		Layers        []*rawBlob `json:"layers"`
	}
	if err := json.Unmarshal(manifest, &doc); err != nil {
		return ImageLayout{}, fmt.Errorf("%w: %v", ErrNotSingleImage, err)
	}
	if mediaType == "" {
		mediaType = doc.MediaType
	}
	if (mediaType != ociManifestType && mediaType != dockerManifestType) || doc.SchemaVersion != 2 || doc.Config == nil {
		return ImageLayout{}, ErrNotSingleImage
	}
	config, ok := doc.Config.blob()
	if !ok {
		return ImageLayout{}, ErrNotSingleImage
	}
	l := ImageLayout{ManifestType: mediaType, Manifest: manifest, ManifestDigest: digestHex(manifest), Config: config, RepoTag: repoTag}
	for _, raw := range doc.Layers {
		layer, ok := raw.blob()
		if !ok {
			return ImageLayout{}, ErrNotSingleImage
		}
		l.Layers = append(l.Layers, layer)
	}
	return l, nil
}

type rawBlob struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

func (r *rawBlob) blob() (ExportBlob, bool) {
	if r == nil || !hexDigest.MatchString(r.Digest) || r.Size < 0 {
		return ExportBlob{}, false
	}
	return ExportBlob{Digest: r.Digest, Size: r.Size}, true
}

func digestHex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// tarFile is one entry of the archive: a small file held in memory, a directory, or a blob read from the registry.
type tarFile struct {
	name string
	dir  bool
	body []byte
	blob *ExportBlob
}

// entries is the archive in order: oci-layout, index.json, manifest.json, the blob folders, then the manifest,
// the config and the layers (each blob once, in manifest order).
func (l ImageLayout) entries() ([]tarFile, error) {
	ociLayout, err := json.Marshal(map[string]string{"imageLayoutVersion": "1.0.0"})
	if err != nil {
		return nil, err
	}
	_, tag, _ := strings.Cut(l.RepoTag, ":")
	type indexEntry struct {
		MediaType   string            `json:"mediaType"`
		Digest      string            `json:"digest"`
		Size        int               `json:"size"`
		Annotations map[string]string `json:"annotations,omitempty"`
	}
	index, err := json.Marshal(struct {
		SchemaVersion int          `json:"schemaVersion"`
		MediaType     string       `json:"mediaType"`
		Manifests     []indexEntry `json:"manifests"`
	}{2, ociIndexType, []indexEntry{{
		MediaType: l.ManifestType, Digest: l.ManifestDigest, Size: len(l.Manifest),
		Annotations: map[string]string{
			"io.containerd.image.name":          "docker.io/library/" + l.RepoTag,
			"org.opencontainers.image.ref.name": tag,
		},
	}}})
	if err != nil {
		return nil, err
	}
	layers := make([]string, 0, len(l.Layers))
	for _, layer := range l.Layers {
		layers = append(layers, blobPath(layer.Digest))
	}
	dockerManifest, err := json.Marshal([]struct {
		Config   string   `json:"Config"`
		RepoTags []string `json:"RepoTags"`
		Layers   []string `json:"Layers"`
	}{{Config: blobPath(l.Config.Digest), RepoTags: []string{l.RepoTag}, Layers: layers}})
	if err != nil {
		return nil, err
	}
	files := []tarFile{
		{name: "oci-layout", body: ociLayout},
		{name: "index.json", body: index},
		{name: "manifest.json", body: dockerManifest},
		{name: "blobs/", dir: true},
		{name: "blobs/sha256/", dir: true},
		{name: blobPath(l.ManifestDigest), body: l.Manifest},
	}
	seen := map[string]bool{l.ManifestDigest: true}
	for _, b := range append([]ExportBlob{l.Config}, l.Layers...) {
		if seen[b.Digest] {
			continue
		}
		seen[b.Digest] = true
		blob := b
		files = append(files, tarFile{name: blobPath(b.Digest), blob: &blob})
	}
	return files, nil
}

// header is the fixed tar header of an entry: no owner, the zero Unix time, 0644 files and 0755 folders.
func (f tarFile) header() *tar.Header {
	h := &tar.Header{Name: f.name, Mode: 0o644, Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0)}
	switch {
	case f.dir:
		h.Mode, h.Typeflag = 0o755, tar.TypeDir
	case f.blob != nil:
		h.Size = f.blob.Size
	default:
		h.Size = int64(len(f.body))
	}
	return h
}

// Size is the exact length of the tar WriteTo writes.
func (l ImageLayout) Size() int64 {
	files, err := l.entries()
	if err != nil {
		return 0
	}
	var total int64
	for _, f := range files {
		// The header bytes exactly as the tar writer emits them (with a PAX record for a size past 8 GiB).
		var counter countingWriter
		if err = tar.NewWriter(&counter).WriteHeader(f.header()); err != nil {
			return 0
		}
		total += counter.n + (f.header().Size+511)/512*512
	}
	return total + 1024 // two zero blocks end the archive
}

type countingWriter struct{ n int64 }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// WriteTo writes the archive to w, reading each blob once through open and checking it against its digest and
// size. A failure of w is returned as it is; a failure of reading a blob is the opener's error, or ErrUnreachable
// (a read cut off, wrong bytes, too few or too many), or the context's error when the read was cancelled.
func (l ImageLayout) WriteTo(w io.Writer, open BlobOpener) error {
	files, err := l.entries()
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	for _, f := range files {
		if err = tw.WriteHeader(f.header()); err != nil {
			return err
		}
		switch {
		case f.blob != nil:
			if err = copyBlob(tw, *f.blob, open); err != nil {
				return err
			}
		case !f.dir:
			if _, err = tw.Write(f.body); err != nil {
				return err
			}
		}
	}
	return tw.Close()
}

// copyBlob copies exactly the blob's bytes into w and checks them; a read failure is a registry failure, a write
// failure stays w's.
func copyBlob(w io.Writer, blob ExportBlob, open BlobOpener) error {
	rc, err := open(blob.Digest)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	src := &blobReader{r: rc, hash: sha256.New()}
	n, err := io.CopyN(w, src, blob.Size)
	switch {
	case src.err != nil:
		return src.err
	case err != nil && errors.Is(err, io.EOF) && n < blob.Size:
		return fmt.Errorf("read blob %s: %w: shorter than its size", blob.Digest, ErrUnreachable)
	case err != nil:
		return err // the writer's
	}
	var extra [1]byte
	if more, _ := rc.Read(extra[:]); more > 0 {
		return fmt.Errorf("read blob %s: %w: longer than its size", blob.Digest, ErrUnreachable)
	}
	if "sha256:"+hex.EncodeToString(src.hash.Sum(nil)) != blob.Digest {
		return fmt.Errorf("read blob %s: %w: content does not match its digest", blob.Digest, ErrUnreachable)
	}
	return nil
}

// blobReader hashes what it reads and keeps a read failure (other than EOF) as a registry failure.
type blobReader struct {
	r    io.Reader
	hash hash.Hash
	err  error
}

func (b *blobReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.hash.Write(p[:n])
	if err != nil && !errors.Is(err, io.EOF) && b.err == nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			b.err = err
		} else {
			b.err = fmt.Errorf("read blob: %w: %v", ErrUnreachable, err)
		}
	}
	return n, err
}

// GetManifest reads a manifest by digest (at most max bytes, else ErrBlobTooLarge) and checks it against the
// digest; it returns the media type the registry states and the body.
func (c *Client) GetManifest(ctx context.Context, digest string, max int64) (string, []byte, error) {
	repo, err := c.repo()
	if err != nil {
		return "", nil, err
	}
	hc, err := c.http(ctx, repo, transport.PullScope)
	if err != nil {
		return "", nil, c.wrap("get manifest", err)
	}
	req, err := newGet(ctx, c.base(repo)+"/manifests/"+digest)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", manifestAccept)
	resp, err := c.do(hc, req, "get manifest")
	if err != nil {
		return "", nil, err
	}
	defer drain(resp)
	if err = statusError("get manifest", resp.StatusCode); err != nil {
		return "", nil, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return "", nil, c.wrap("get manifest", err)
	}
	if int64(len(body)) > max {
		return "", nil, fmt.Errorf("get manifest: %w", ErrBlobTooLarge)
	}
	if digestHex(body) != digest {
		return "", nil, fmt.Errorf("get manifest: %w: content does not match its digest", ErrUnreachable)
	}
	mediaType, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";")
	return strings.TrimSpace(mediaType), bytes.Clone(body), nil
}

// OpenBlob opens a blob for reading and reports the size the registry states (-1 when none); the caller closes it.
// The bytes are not checked here: the reader does that (ImageLayout.WriteTo).
func (c *Client) OpenBlob(ctx context.Context, digest string) (io.ReadCloser, int64, error) {
	repo, err := c.repo()
	if err != nil {
		return nil, 0, err
	}
	hc, err := c.http(ctx, repo, transport.PullScope)
	if err != nil {
		return nil, 0, c.wrap("open blob", err)
	}
	req, err := newGet(ctx, c.base(repo)+"/blobs/"+digest)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.do(hc, req, "open blob")
	if err != nil {
		return nil, 0, err
	}
	if err = statusError("open blob", resp.StatusCode); err != nil {
		drain(resp)
		return nil, 0, err
	}
	return resp.Body, resp.ContentLength, nil
}

func newGet(ctx context.Context, url string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
}

// statusError is nil for 200 and the sentinel of any other status.
func statusError(op string, status int) error {
	if status == http.StatusOK {
		return nil
	}
	if s := classifyStatus(status); s != nil {
		return fmt.Errorf("%s: %w (status %d)", op, s, status)
	}
	return fmt.Errorf("%s: %w (status %d)", op, ErrUnreachable, status)
}
