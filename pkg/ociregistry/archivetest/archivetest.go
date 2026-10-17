// Package archivetest builds small image archives for tests: the layouts `docker save` writes (legacy
// manifest.json, Docker ≥25 OCI layout, a multi-platform index). Everything is fixed bytes and a fixed time, so
// the archives and their digests are the same on every run.
package archivetest

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const (
	OCIConfigType   = "application/vnd.oci.image.config.v1+json"
	OCILayerType    = "application/vnd.oci.image.layer.v1.tar"
	OCIManifestType = "application/vnd.oci.image.manifest.v1+json"
	OCIIndexType    = "application/vnd.oci.image.index.v1+json"
)

// fixedTime is the modification time of every entry.
var fixedTime = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)

// Digest is the sha256 digest ("sha256:<hex>") of b.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func hexOf(b []byte) string { return Digest(b)[len("sha256:"):] }

// Image is one test image: its layers (uncompressed tars in practice; any fixed bytes here) and a config naming
// one diff id per layer for the platform os/arch.
type Image struct {
	Layers [][]byte
	OS     string
	Arch   string
}

// Amd64 is a linux/amd64 image of the layers.
func Amd64(layers ...[]byte) Image { return Image{Layers: layers, OS: "linux", Arch: "amd64"} }

// Config is the image config: os, architecture and the diff ids (the sha256 of each layer).
func (i Image) Config() []byte {
	ids := make([]string, len(i.Layers))
	for n, l := range i.Layers {
		ids[n] = Digest(l)
	}
	b, err := json.Marshal(map[string]any{
		"architecture": i.Arch, "os": i.OS,
		"rootfs": map[string]any{"type": "layers", "diff_ids": ids},
	})
	if err != nil {
		panic(err)
	}
	return b
}

// Entry is one tar entry; Type defaults to a regular file.
type Entry struct {
	Name     string
	Body     []byte
	Type     byte
	Linkname string
}

// Tar writes the entries in order (PAX headers where a name needs them).
func Tar(entries ...Entry) []byte {
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for _, e := range entries {
		h := &tar.Header{Name: e.Name, Mode: 0o644, Size: int64(len(e.Body)), ModTime: fixedTime, Typeflag: e.Type, Linkname: e.Linkname}
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			panic(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write(e.Body); err != nil {
				panic(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		panic(err)
	}
	return out.Bytes()
}

// Legacy is the pre-25 `docker save` layout: manifest.json, <config hex>.json and <n>/layer.tar per layer.
func Legacy(img Image) []byte {
	config := img.Config()
	configName := hexOf(config) + ".json"
	layerNames := make([]string, len(img.Layers))
	entries := []Entry{}
	for n, l := range img.Layers {
		layerNames[n] = fmt.Sprintf("layer%d/layer.tar", n+1)
		entries = append(entries, Entry{Name: layerNames[n], Body: l})
	}
	manifest := mustJSON([]map[string]any{{"Config": configName, "RepoTags": []string{"web:latest"}, "Layers": layerNames}})
	entries = append(entries, Entry{Name: configName, Body: config}, Entry{Name: "manifest.json", Body: manifest})
	return Tar(entries...)
}

// Manifest is the OCI image manifest of img (config and layers as OCI descriptors).
func (i Image) Manifest() []byte {
	config := i.Config()
	layers := make([]map[string]any, len(i.Layers))
	for n, l := range i.Layers {
		layers[n] = descriptor(OCILayerType, l, nil)
	}
	return mustJSON(map[string]any{
		"schemaVersion": 2, "mediaType": OCIManifestType,
		"config": descriptor(OCIConfigType, config, nil), "layers": layers,
	})
}

// OCI is the Docker ≥25 layout: oci-layout, index.json → manifest → config and layers under blobs/sha256/, plus
// the legacy manifest.json Docker also writes.
func OCI(img Image) []byte {
	manifest := img.Manifest()
	index := mustJSON(map[string]any{"schemaVersion": 2, "mediaType": OCIIndexType,
		"manifests": []any{descriptor(OCIManifestType, manifest, nil)}})
	return Tar(append(blobEntries(img, manifest), Entry{Name: "index.json", Body: index}, Entry{Name: "oci-layout", Body: []byte(`{"imageLayoutVersion":"1.0.0"}`)})...)
}

// MultiArch is an OCI layout whose index.json names a nested index of several platforms (one image each), as
// `docker save` of a multi-platform image writes it; an attestation manifest is listed as well.
func MultiArch(images ...Image) []byte {
	var entries []Entry
	var manifests []any
	for _, img := range images {
		manifest := img.Manifest()
		entries = append(entries, blobEntries(img, manifest)...)
		manifests = append(manifests, descriptor(OCIManifestType, manifest, map[string]any{"platform": map[string]string{"os": img.OS, "architecture": img.Arch}}))
	}
	attestation := mustJSON(map[string]any{"schemaVersion": 2, "mediaType": OCIManifestType})
	entries = append(entries, Entry{Name: "blobs/sha256/" + hexOf(attestation), Body: attestation})
	manifests = append(manifests, descriptor(OCIManifestType, attestation, map[string]any{
		"platform":    map[string]string{"os": "unknown", "architecture": "unknown"},
		"annotations": map[string]string{"vnd.docker.reference.type": "attestation-manifest"},
	}))
	nested := mustJSON(map[string]any{"schemaVersion": 2, "mediaType": OCIIndexType, "manifests": manifests})
	index := mustJSON(map[string]any{"schemaVersion": 2, "mediaType": OCIIndexType,
		"manifests": []any{descriptor(OCIIndexType, nested, nil)}})
	entries = append(entries, Entry{Name: "blobs/sha256/" + hexOf(nested), Body: nested}, Entry{Name: "index.json", Body: index})
	return Tar(entries...)
}

// blobEntries are the blobs of one image under blobs/sha256/: layers, config, manifest.
func blobEntries(img Image, manifest []byte) []Entry {
	var entries []Entry
	for _, l := range img.Layers {
		entries = append(entries, Entry{Name: "blobs/sha256/" + hexOf(l), Body: l})
	}
	config := img.Config()
	return append(entries, Entry{Name: "blobs/sha256/" + hexOf(config), Body: config}, Entry{Name: "blobs/sha256/" + hexOf(manifest), Body: manifest})
}

func descriptor(mediaType string, body []byte, extra map[string]any) map[string]any {
	d := map[string]any{"mediaType": mediaType, "digest": Digest(body), "size": len(body)}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
