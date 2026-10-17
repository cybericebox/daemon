package ociregistry

import (
	"archive/tar"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Server-side reader of a `docker save` archive, the twin of the browser reader (exercises-frontend
// src/lib/imageArchive.ts): the same layouts, the same choices, the same refusals. The archive is a stream that can
// be opened again (a zip entry), never held whole: pass 1 (ReadImageArchive) walks the tar headers, keeps the small
// files and resolves the image; pass 2 (Stream) reads it once more and hands over the bytes of the blobs asked for.
//
//   - Docker ≥25 OCI layout: index.json → (nested index → linux/amd64) → manifest blob → config + layers.
//   - legacy manifest.json: Config `<hex>.json`, Layers `<id>/layer.tar`, layer digests from the config's
//     rootfs.diff_ids (a layer.tar is the uncompressed layer, so its sha256 is the diff id).
//
// When both are present the OCI layout wins. Only an uncompressed tar is accepted.

// Codes of ArchiveError, what the uploader is told.
const (
	ArchiveGzip       = "gzip"
	ArchiveNotTar     = "not_tar"
	ArchiveNoImage    = "no_image"
	ArchiveMultiImage = "multi_image"
	ArchiveNoAmd64    = "no_amd64"
	ArchiveTooLarge   = "too_large"
	// ArchiveDigestMismatch: a blob's bytes are not its digest (one tar entry named by two different digests).
	ArchiveDigestMismatch = "digest_mismatch"
)

// ArchiveError is an archive that is not one linux/amd64 image in an uncompressed docker save tar.
type ArchiveError struct{ Code string }

func (e *ArchiveError) Error() string { return "image archive: " + e.Code }

func archiveErr(code string) error { return &ArchiveError{Code: code} }

// ArchiveSource opens the archive from its first byte; each pass opens it once.
type ArchiveSource func() (io.ReadCloser, error)

// ArchiveBlob is one blob of the image: its digest, media type and size, and the tar entry holding its bytes.
type ArchiveBlob struct {
	Digest    string
	MediaType string
	Size      int64
	entry     int
}

// ImageArchive is the image found by pass 1.
type ImageArchive struct {
	Config ArchiveBlob
	Layers []ArchiveBlob
	// SHA256 is the hex sha256 of the whole archive file (pass 1 reads every byte of it).
	SHA256 string

	open ArchiveSource
}

const (
	// maxJSONBytes: a JSON file larger than this is not an image description.
	maxJSONBytes = 16 << 20
	// Pass 1 keeps the small files it meets (an index, a manifest, a config) so they need no second read: each
	// at most cacheEntryBytes, at most cacheTotalBytes together. A needed file not kept is read in a pass of its own.
	cacheEntryBytes = 4 << 20
	cacheTotalBytes = 64 << 20
	layerTarType    = "application/vnd.oci.image.layer.v1.tar"
	ociConfigType   = "application/vnd.oci.image.config.v1+json"
)

// The index of pass 1 is bounded: at most maxIndexEntries tar entries and maxIndexNameBytes of names and link
// targets kept (a docker save has a few hundred entries); past either the archive is too large. Variables for tests.
var (
	maxIndexEntries         = 100_000
	maxIndexNameBytes int64 = 64 << 20
)

var (
	indexTypes = map[string]bool{
		"application/vnd.oci.image.index.v1+json":                   true,
		"application/vnd.docker.distribution.manifest.list.v2+json": true,
	}
	hexDigest    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	legacyConfig = regexp.MustCompile(`([0-9a-f]{64})(\.json)?$`)
)

// tarEntry is where an entry sits: its position among the tar entries, size, type and link target.
type tarEntry struct {
	ordinal  int
	size     int64
	typeflag byte
	linkname string
}

// ReadImageArchive is pass 1: it reads the whole archive once (size bytes, at most max) and returns the image.
// A failure of the source is returned as it is (wrapped); anything wrong with the archive is an *ArchiveError.
func ReadImageArchive(open ArchiveSource, size, max int64) (*ImageArchive, error) {
	if size > max {
		return nil, archiveErr(ArchiveTooLarge)
	}
	idx := &archiveIndex{open: open, entries: map[string]tarEntry{}, cache: map[int][]byte{}}
	sum, err := idx.scan()
	if err != nil {
		return nil, err
	}
	var image *ImageArchive
	switch {
	case idx.has("index.json"):
		image, err = idx.readOCI()
	case idx.has("manifest.json"):
		image, err = idx.readLegacy()
	default:
		err = archiveErr(ArchiveNoImage)
	}
	if err != nil {
		return nil, err
	}
	// One entry holds one content: two different digests on it mean one of them does not match.
	digestOf := map[int]string{}
	for _, b := range append([]ArchiveBlob{image.Config}, image.Layers...) {
		if d, seen := digestOf[b.entry]; seen && d != b.Digest {
			return nil, archiveErr(ArchiveDigestMismatch)
		}
		digestOf[b.entry] = b.Digest
	}
	image.SHA256, image.open = sum, open
	return image, nil
}

// Stream is pass 2: it reads the archive once more and calls push, in archive order, for every blob whose digest
// is in want, with a reader of exactly its bytes. A blob several descriptors share is handed over once. The first
// push error ends the pass and is returned.
func (a *ImageArchive) Stream(want map[string]bool, push func(blob ArchiveBlob, r io.Reader) error) error {
	byEntry := map[int]ArchiveBlob{}
	for _, b := range append([]ArchiveBlob{a.Config}, a.Layers...) {
		if want[b.Digest] {
			if _, taken := byEntry[b.entry]; !taken {
				byEntry[b.entry] = b
			}
		}
	}
	if len(byEntry) == 0 {
		return nil
	}
	rc, err := a.open()
	if err != nil {
		return fmt.Errorf("open image archive: %w", err)
	}
	defer func() { _ = rc.Close() }()
	src := &sourceReader{r: rc}
	tr := tar.NewReader(src)
	for ordinal := 0; len(byEntry) > 0; ordinal++ {
		if _, err = tr.Next(); err != nil {
			if src.err != nil {
				return fmt.Errorf("read image archive: %w", src.err)
			}
			return errors.New("image archive: a blob was not found again in the archive")
		}
		blob, ok := byEntry[ordinal]
		if !ok {
			continue
		}
		delete(byEntry, ordinal)
		r := &sourceErrReader{r: io.LimitReader(tr, blob.Size), src: src}
		if err = push(blob, r); err != nil {
			return err
		}
	}
	return nil
}

// sourceReader remembers a read failure of the source, so it is not taken for a broken tar.
type sourceReader struct {
	r   io.Reader
	err error
}

func (s *sourceReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		s.err = err
	}
	return n, err
}

// sourceErrReader is a blob's reader in pass 2: a failure of the source is returned as such.
type sourceErrReader struct {
	r   io.Reader
	src *sourceReader
}

func (s *sourceErrReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && s.src.err != nil {
		return n, fmt.Errorf("read image archive: %w", s.src.err)
	}
	return n, err
}

// archiveIndex is what pass 1 learned: every entry by name (a later one of the same name wins) and the small
// files it kept.
type archiveIndex struct {
	open    ArchiveSource
	entries map[string]tarEntry
	cache   map[int][]byte
}

// scan walks the whole archive once: headers, the small files, and the sha256 of every byte.
func (x *archiveIndex) scan() (string, error) {
	rc, err := x.open()
	if err != nil {
		return "", fmt.Errorf("open image archive: %w", err)
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	src := &sourceReader{r: io.TeeReader(rc, h)}
	br := bufio.NewReader(src)
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		return "", archiveErr(ArchiveGzip)
	}
	if src.err != nil {
		return "", fmt.Errorf("read image archive: %w", src.err)
	}
	tr := tar.NewReader(br)
	cached, nameBytes := int64(0), int64(0)
	for ordinal := 0; ; ordinal++ {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) && src.err == nil {
			if ordinal == 0 {
				return "", archiveErr(ArchiveNotTar)
			}
			break
		}
		if nextErr != nil {
			if src.err != nil {
				return "", fmt.Errorf("read image archive: %w", src.err)
			}
			return "", archiveErr(ArchiveNotTar)
		}
		if ordinal >= maxIndexEntries {
			return "", archiveErr(ArchiveTooLarge)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeDir {
			continue // nothing in them matters
		}
		name := normalizeName(hdr.Name)
		if nameBytes += int64(len(name) + len(hdr.Linkname)); nameBytes > maxIndexNameBytes {
			return "", archiveErr(ArchiveTooLarge)
		}
		entry := tarEntry{ordinal: ordinal, size: hdr.Size, typeflag: hdr.Typeflag, linkname: hdr.Linkname}
		x.entries[name] = entry
		if isRegular(entry.typeflag) && hdr.Size <= cacheEntryBytes && cached+hdr.Size <= cacheTotalBytes {
			body, readErr := io.ReadAll(tr)
			if readErr != nil {
				if src.err != nil {
					return "", fmt.Errorf("read image archive: %w", src.err)
				}
				return "", archiveErr(ArchiveNotTar)
			}
			x.cache[ordinal] = body
			cached += hdr.Size
		}
	}
	// The end-of-archive blocks and anything after them count for the file's hash.
	if _, err = io.Copy(io.Discard, br); err != nil {
		return "", fmt.Errorf("read image archive: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isRegular(t byte) bool { return t == tar.TypeReg || t == '\x00' || t == tar.TypeCont }

// normalizeName drops empty and "." parts and applies "..", like the browser reader.
func normalizeName(name string) string {
	var parts []string
	for _, part := range strings.Split(name, "/") {
		switch part {
		case "", ".":
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/")
}

func (x *archiveIndex) has(name string) bool {
	_, ok := x.entries[name]
	return ok
}

// find is the regular file behind a name, following symbolic and hard links.
func (x *archiveIndex) find(name string) (tarEntry, bool) {
	current := normalizeName(name)
	for hops := 0; hops < 16; hops++ {
		entry, ok := x.entries[current]
		if !ok {
			return tarEntry{}, false
		}
		switch entry.typeflag {
		case tar.TypeSymlink:
			dir := ""
			if i := strings.LastIndex(current, "/"); i >= 0 {
				dir = current[:i]
			}
			if strings.HasPrefix(entry.linkname, "/") {
				current = normalizeName(entry.linkname)
			} else {
				current = normalizeName(dir + "/" + entry.linkname)
			}
		case tar.TypeLink:
			current = normalizeName(entry.linkname)
		default:
			return entry, isRegular(entry.typeflag)
		}
	}
	return tarEntry{}, false
}

// body returns the bytes of a small regular file: kept by pass 1, else read in a pass of its own.
func (x *archiveIndex) body(entry tarEntry) ([]byte, error) {
	if b, ok := x.cache[entry.ordinal]; ok {
		return b, nil
	}
	rc, err := x.open()
	if err != nil {
		return nil, fmt.Errorf("open image archive: %w", err)
	}
	defer func() { _ = rc.Close() }()
	src := &sourceReader{r: rc}
	tr := tar.NewReader(src)
	for ordinal := 0; ; ordinal++ {
		if _, err = tr.Next(); err != nil {
			if src.err != nil {
				return nil, fmt.Errorf("read image archive: %w", src.err)
			}
			return nil, archiveErr(ArchiveNoImage)
		}
		if ordinal == entry.ordinal {
			b, readErr := io.ReadAll(tr)
			if readErr != nil {
				if src.err != nil {
					return nil, fmt.Errorf("read image archive: %w", src.err)
				}
				return nil, archiveErr(ArchiveNoImage)
			}
			x.cache[ordinal] = b
			return b, nil
		}
	}
}

// json reads a small JSON file of the archive into a generic value.
func (x *archiveIndex) json(name string) (any, error) {
	entry, ok := x.find(name)
	if !ok || entry.size > maxJSONBytes {
		return nil, archiveErr(ArchiveNoImage)
	}
	b, err := x.body(entry)
	if err != nil {
		return nil, err
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return nil, archiveErr(ArchiveNoImage)
	}
	return v, nil
}

func (x *archiveIndex) object(name string) (map[string]any, error) {
	v, err := x.json(name)
	if err != nil {
		return nil, err
	}
	return asObject(v)
}

func asObject(v any) (map[string]any, error) {
	o, ok := v.(map[string]any)
	if !ok {
		return nil, archiveErr(ArchiveNoImage)
	}
	return o, nil
}

func blobPath(digest string) string { return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:") }

// descriptor is the part of an OCI descriptor the reader uses.
type descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        *float64          `json:"size"`
	Platform    *platform         `json:"platform"`
	Annotations map[string]string `json:"annotations"`
}

type platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}

// descriptors decodes a list of descriptors; anything else is an empty list.
func descriptors(v any) []descriptor {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []descriptor
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

func toDescriptor(v any) (descriptor, bool) {
	if v == nil {
		return descriptor{}, false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return descriptor{}, false
	}
	var d descriptor
	return d, json.Unmarshal(b, &d) == nil
}

// blob locates the blob a descriptor names; a stated size must be the entry's.
func (x *archiveIndex) blob(d descriptor, fallbackType string) (ArchiveBlob, error) {
	if !hexDigest.MatchString(d.Digest) {
		return ArchiveBlob{}, archiveErr(ArchiveNoImage)
	}
	entry, ok := x.find(blobPath(d.Digest))
	if !ok || (d.Size != nil && int64(*d.Size) != entry.size) {
		return ArchiveBlob{}, archiveErr(ArchiveNoImage)
	}
	mediaType := d.MediaType
	if mediaType == "" {
		mediaType = fallbackType
	}
	return ArchiveBlob{Digest: d.Digest, MediaType: mediaType, Size: entry.size, entry: entry.ordinal}, nil
}

// checkPlatform refuses an image built for anything but linux/amd64, when the config says what it was built for.
func checkPlatform(config map[string]any) error {
	if arch, ok := config["architecture"].(string); ok && arch != "amd64" {
		return archiveErr(ArchiveNoAmd64)
	}
	if os, ok := config["os"].(string); ok && os != "linux" {
		return archiveErr(ArchiveNoAmd64)
	}
	return nil
}

func isAttestation(d descriptor) bool {
	return d.Annotations["vnd.docker.reference.type"] == "attestation-manifest"
}

// pickOne picks the one image of an index: linux/amd64 among platform entries, and never two different images.
func pickOne(manifests []descriptor) (descriptor, error) {
	var picked []descriptor
	withPlatform := 0
	for _, d := range manifests {
		if isAttestation(d) {
			continue
		}
		if d.Platform != nil {
			withPlatform++
			if d.Platform.OS == "linux" && d.Platform.Architecture == "amd64" {
				picked = append(picked, d)
			}
		}
	}
	for _, d := range manifests {
		if !isAttestation(d) && d.Platform == nil {
			picked = append(picked, d)
		}
	}
	digests := map[string]bool{}
	for _, d := range picked {
		digests[d.Digest] = true
	}
	switch {
	case len(digests) == 0 && withPlatform > 0:
		return descriptor{}, archiveErr(ArchiveNoAmd64)
	case len(digests) == 0:
		return descriptor{}, archiveErr(ArchiveNoImage)
	case len(digests) > 1:
		return descriptor{}, archiveErr(ArchiveMultiImage)
	}
	return picked[0], nil
}

func (x *archiveIndex) readOCI() (*ImageArchive, error) {
	index, err := x.object("index.json")
	if err != nil {
		return nil, err
	}
	for depth := 0; depth < 8; depth++ {
		chosen, err := pickOne(descriptors(index["manifests"]))
		if err != nil {
			return nil, err
		}
		if !hexDigest.MatchString(chosen.Digest) {
			return nil, archiveErr(ArchiveNoImage)
		}
		path := blobPath(chosen.Digest)
		if _, ok := x.find(path); !ok {
			if chosen.Platform != nil {
				return nil, archiveErr(ArchiveNoAmd64)
			}
			return nil, archiveErr(ArchiveNoImage)
		}
		doc, err := x.object(path)
		if err != nil {
			return nil, err
		}
		docType, _ := doc["mediaType"].(string)
		_, hasManifests := doc["manifests"].([]any)
		if indexTypes[chosen.MediaType] || indexTypes[docType] || (hasManifests && doc["layers"] == nil) {
			index = doc
			continue
		}
		configDescriptor, _ := toDescriptor(doc["config"])
		config, err := x.blob(configDescriptor, ociConfigType)
		if err != nil {
			return nil, err
		}
		configDoc, err := x.object(blobPath(config.Digest))
		if err != nil {
			return nil, err
		}
		if err = checkPlatform(configDoc); err != nil {
			return nil, err
		}
		rawLayers, ok := doc["layers"].([]any)
		if !ok {
			return nil, archiveErr(ArchiveNoImage)
		}
		image := &ImageArchive{Config: config}
		for _, d := range descriptors(rawLayers) {
			layer, err := x.blob(d, layerTarType)
			if err != nil {
				return nil, err
			}
			image.Layers = append(image.Layers, layer)
		}
		if len(image.Layers) != len(rawLayers) {
			return nil, archiveErr(ArchiveNoImage)
		}
		return image, nil
	}
	return nil, archiveErr(ArchiveNoImage)
}

func (x *archiveIndex) readLegacy() (*ImageArchive, error) {
	v, err := x.json("manifest.json")
	if err != nil {
		return nil, err
	}
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return nil, archiveErr(ArchiveNoImage)
	}
	images := map[string]bool{}
	for _, item := range items {
		o, err := asObject(item)
		if err != nil {
			return nil, err
		}
		key, _ := json.Marshal(o["Config"])
		images[string(key)] = true
	}
	if len(images) > 1 {
		return nil, archiveErr(ArchiveMultiImage)
	}
	item, _ := asObject(items[0])
	configName, _ := item["Config"].(string)
	configName = normalizeName(configName)
	match := legacyConfig.FindStringSubmatch(configName)
	if match == nil {
		return nil, archiveErr(ArchiveNoImage)
	}
	configEntry, ok := x.find(configName)
	if !ok {
		return nil, archiveErr(ArchiveNoImage)
	}
	config, err := x.object(configName)
	if err != nil {
		return nil, err
	}
	if err = checkPlatform(config); err != nil {
		return nil, err
	}
	rootfs, _ := config["rootfs"].(map[string]any)
	diffIDs, okIDs := rootfs["diff_ids"].([]any)
	layerNames, okNames := item["Layers"].([]any)
	if !okIDs || !okNames || len(diffIDs) != len(layerNames) {
		return nil, archiveErr(ArchiveNoImage)
	}
	image := &ImageArchive{Config: ArchiveBlob{Digest: "sha256:" + match[1], MediaType: ociConfigType, Size: configEntry.size, entry: configEntry.ordinal}}
	for i, rawName := range layerNames {
		name, _ := rawName.(string)
		digest, _ := diffIDs[i].(string)
		entry, found := x.find(name)
		if name == "" || !found || !hexDigest.MatchString(digest) {
			return nil, archiveErr(ArchiveNoImage)
		}
		image.Layers = append(image.Layers, ArchiveBlob{Digest: digest, MediaType: layerTarType, Size: entry.size, entry: entry.ordinal})
	}
	return image, nil
}
