package imageRegistryModel

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

var (
	hex1 = strings.Repeat("1", 64)
	hex2 = strings.Repeat("2", 64)
	hex3 = strings.Repeat("3", 64)
)

func ociImage() (Descriptor, []Descriptor) {
	return Descriptor{MediaType: "application/vnd.oci.image.config.v1+json", Digest: "sha256:" + hex1, Size: 100},
		[]Descriptor{
			{MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Digest: "sha256:" + hex2, Size: 200},
			{MediaType: "application/vnd.oci.image.layer.v1.tar", Digest: "sha256:" + hex3, Size: 300},
		}
}

func dockerImage() (Descriptor, []Descriptor) {
	return Descriptor{MediaType: "application/vnd.docker.container.image.v1+json", Digest: "sha256:" + hex1, Size: 100},
		[]Descriptor{
			{MediaType: "application/vnd.docker.image.rootfs.diff.tar.gzip", Digest: "sha256:" + hex2, Size: 200},
			{MediaType: "application/vnd.docker.image.rootfs.diff.tar", Digest: "sha256:" + hex3, Size: 300},
		}
}

const ociBody = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
	`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","size":100},` +
	`"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222","size":200},` +
	`{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:3333333333333333333333333333333333333333333333333333333333333333","size":300}]}`

func TestBuildManifestOCIIsDeterministic(t *testing.T) {
	config, layers := ociImage()
	for i := 0; i < 3; i++ {
		m, err := BuildManifest(config, layers)
		if err != nil {
			t.Fatal(err)
		}
		if m.MediaType != MediaTypeOCIManifest || string(m.Body) != ociBody {
			t.Fatalf("manifest %s %s", m.MediaType, m.Body)
		}
		if m.Digest != "sha256:08d1e5408d0d316bdd156b3390034046381049643ed76feab72c7424087a1763" {
			t.Fatalf("digest %s", m.Digest)
		}
		if m.BlobBytes != 600 {
			t.Fatalf("blob bytes %d", m.BlobBytes)
		}
	}
}

func TestBuildManifestAllDockerKeepsDockerSchema2(t *testing.T) {
	config, layers := dockerImage()
	m, err := BuildManifest(config, layers)
	if err != nil {
		t.Fatal(err)
	}
	if m.MediaType != MediaTypeDockerManifest || m.Digest != "sha256:8a38a90d524ef4ac7f7213877dddd08f88bb2eafd1ffe33cda68a7706a0bb3c2" {
		t.Fatalf("manifest %s %s %s", m.MediaType, m.Digest, m.Body)
	}
	if !strings.Contains(string(m.Body), `"mediaType":"application/vnd.docker.image.rootfs.diff.tar.gzip"`) {
		t.Fatalf("body %s", m.Body)
	}
}

// Mixed Docker and OCI types: the Docker ones are mapped to their OCI equivalents, so the result is exactly the
// all-OCI manifest.
func TestBuildManifestMixedMapsDockerToOCI(t *testing.T) {
	config, _ := dockerImage()
	_, layers := ociImage()
	m, err := BuildManifest(config, layers)
	if err != nil || m.MediaType != MediaTypeOCIManifest || string(m.Body) != ociBody {
		t.Fatalf("manifest %v %s %s", err, m.MediaType, m.Body)
	}
	ociConfig, _ := ociImage()
	_, dockerLayers := dockerImage()
	m, err = BuildManifest(ociConfig, dockerLayers)
	if err != nil || string(m.Body) != ociBody {
		t.Fatalf("manifest %v %s", err, m.Body)
	}
}

func TestBuildManifestAcceptsZstdInBothSpellings(t *testing.T) {
	config, layers := ociImage()
	layers[0].MediaType = "application/vnd.oci.image.layer.v1.tar+zstd"
	if _, err := BuildManifest(config, layers); err != nil {
		t.Fatal(err)
	}
	dConfig, dLayers := dockerImage()
	dLayers[0].MediaType = "application/vnd.docker.image.rootfs.diff.tar.zstd"
	if _, err := BuildManifest(dConfig, dLayers); err != nil {
		t.Fatal(err)
	}
}

func TestBuildManifestRefusesBadDescriptors(t *testing.T) {
	cases := map[string]func(c *Descriptor, l *[]Descriptor){
		"unknown layer type": func(_ *Descriptor, l *[]Descriptor) {
			(*l)[0].MediaType = "application/vnd.oci.image.layer.nondistributable.v1.tar+gzip"
		},
		"empty layer type":     func(_ *Descriptor, l *[]Descriptor) { (*l)[1].MediaType = "" },
		"config as a layer":    func(_ *Descriptor, l *[]Descriptor) { (*l)[0].MediaType = "application/vnd.oci.image.config.v1+json" },
		"layer as the config":  func(c *Descriptor, _ *[]Descriptor) { c.MediaType = "application/vnd.oci.image.layer.v1.tar" },
		"unknown config type":  func(c *Descriptor, _ *[]Descriptor) { c.MediaType = "application/json" },
		"no layers":            func(_ *Descriptor, l *[]Descriptor) { *l = nil },
		"uppercase digest":     func(_ *Descriptor, l *[]Descriptor) { (*l)[0].Digest = "sha256:" + strings.Repeat("A", 64) },
		"sha512 digest":        func(c *Descriptor, _ *[]Descriptor) { c.Digest = "sha512:" + hex1 },
		"short digest":         func(_ *Descriptor, l *[]Descriptor) { (*l)[0].Digest = "sha256:" + hex1[:63] },
		"zero size":            func(_ *Descriptor, l *[]Descriptor) { (*l)[1].Size = 0 },
		"negative config size": func(c *Descriptor, _ *[]Descriptor) { c.Size = -1 },
	}
	for name, mutate := range cases {
		config, layers := ociImage()
		mutate(&config, &layers)
		if _, err := BuildManifest(config, layers); !errors.Is(err, ErrInvalidImageArchive.Err()) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestBuildManifestTooManyLayers(t *testing.T) {
	config, _ := ociImage()
	layers := make([]Descriptor, MaxImageLayers+1)
	for i := range layers {
		layers[i] = Descriptor{MediaType: "application/vnd.oci.image.layer.v1.tar", Digest: "sha256:" + hex2, Size: 1}
	}
	if _, err := BuildManifest(config, layers); !errors.Is(err, ErrInvalidImageArchive.Err()) {
		t.Fatal(err)
	}
	if _, err := BuildManifest(config, layers[:MaxImageLayers]); err != nil {
		t.Fatal(err)
	}
}

func TestBuildManifestSizeLimit(t *testing.T) {
	config, layers := ociImage()
	layers[1].Size = MaxImageArchiveBytes - 300 // config 100 + 200 + this = the limit exactly
	if _, err := BuildManifest(config, layers); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	layers[1].Size++
	if _, err := BuildManifest(config, layers); !errors.Is(err, ErrImageTooLarge.Err()) {
		t.Fatalf("over the limit: %v", err)
	}
}

func TestNewPlatformImage(t *testing.T) {
	ref, err := NewPlatformRef("web", "sha256:"+hex1)
	if err != nil {
		t.Fatal(err)
	}
	ex, by := uuid.FromStringOrNil("01900000-0000-7000-8000-000000000001"), uuid.FromStringOrNil("01900000-0000-7000-8000-000000000002")
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	img := NewPlatformImage(ref, ex, by, now)
	if img.Digest != "sha256:"+hex1 || img.Tag != "sha256-"+hex1 || img.Name != "web" ||
		img.ExerciseID != ex || img.UploadedBy != by || !img.UploadedAt.Equal(now) {
		t.Fatalf("%+v", img)
	}
}

func TestCheckImageConfig(t *testing.T) {
	good := `{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":["sha256:` + hex2 + `","sha256:` + hex3 + `"]}}`
	if err := CheckImageConfig([]byte(good), 2); err != nil {
		t.Fatalf("good: %v", err)
	}
	bad := map[string]struct {
		body   string
		layers int
	}{
		"arm64":        {strings.Replace(good, "amd64", "arm64", 1), 2},
		"windows":      {strings.Replace(good, `"linux"`, `"windows"`, 1), 2},
		"no os":        {`{"architecture":"amd64","rootfs":{"diff_ids":["a","b"]}}`, 2},
		"layer count":  {good, 3},
		"not json":     {`{"architecture":`, 2},
		"empty object": {`{}`, 0},
	}
	for name, c := range bad {
		if err := CheckImageConfig([]byte(c.body), c.layers); !errors.Is(err, ErrInvalidImageConfig.Err()) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// A push is stale only after the job itself would have timed out.
func TestBlobPushStaleAfterTheJobTimeout(t *testing.T) {
	if BlobPushStaleAfter <= BlobPushTimeout {
		t.Fatalf("stale %s, timeout %s", BlobPushStaleAfter, BlobPushTimeout)
	}
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	p := NewBlobPush("sha256:"+hex1, now)
	if s, _ := p.Current(now.Add(BlobPushTimeout)); s != BlobPushing {
		t.Fatalf("at the timeout %s", s)
	}
}
