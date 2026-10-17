package imageRegistryUseCase

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/pkg/ociregistry"
)

var (
	uploader   = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000a1")
	stranger   = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000a2")
	exerciseID = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000e1")
	stagedID   = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000f1")

	configBytes = []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`)
	layerBytes  = []byte("fixed layer bytes of the test image")
)

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// fakeMedia is the staged media files: one file per id, its bytes by content hash.
type fakeMedia struct {
	mu       sync.Mutex
	files    map[uuid.UUID]mediaModel.File
	data     map[string][]byte
	streamed int
	asked    int
}

func newFakeMedia() *fakeMedia {
	return &fakeMedia{files: map[uuid.UUID]mediaModel.File{}, data: map[string][]byte{}}
}

func (m *fakeMedia) stage(id, owner uuid.UUID, b []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hash := strings.TrimPrefix(digestOf(b), "sha256:")
	m.files[id] = mediaModel.File{ID: id, Name: "sha256-" + hash, SizeBytes: int64(len(b)), ContentHash: hash, CreatedBy: uuid.NullUUID{UUID: owner, Valid: true}}
	m.data[hash] = b
}

func (m *fakeMedia) GetFile(_ context.Context, id uuid.UUID) (mediaModel.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.asked++
	f, ok := m.files[id]
	if !ok {
		return mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	return f, nil
}

func (m *fakeMedia) StreamFile(_ context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamed++
	f, ok := m.files[id]
	if !ok {
		return nil, mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	return io.NopCloser(bytes.NewReader(m.data[f.ContentHash])), f, nil
}

type uploadFixture struct {
	*accessFixture
	media *fakeMedia
}

func newUploadFixture(t *testing.T, configured bool) *uploadFixture {
	media := newFakeMedia()
	f := newAccessFixture(t, configured, func(d *Dependencies) { d.Media = media })
	return &uploadFixture{accessFixture: f, media: media}
}

// registry is a direct client of the fixture's platform repository (acme/tasks).
func (f *uploadFixture) registry() *ociregistry.Client {
	return ociregistry.New(ociregistry.Options{Host: f.reg.host(), Repository: "acme/tasks", Username: "acme", Password: pushToken, Insecure: true, AllowInternal: true})
}

func (f *uploadFixture) has(t *testing.T, digest string) bool {
	t.Helper()
	ok, err := f.registry().BlobExists(context.Background(), digest)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func (f *uploadFixture) preload(t *testing.T, blobs ...[]byte) {
	t.Helper()
	for _, b := range blobs {
		if err := f.registry().PushBlob(context.Background(), digestOf(b), int64(len(b)), bytes.NewReader(b)); err != nil {
			t.Fatal(err)
		}
	}
}

func as(user uuid.UUID) context.Context {
	return rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: user, Role: rbac.RoleUser})
}

func testImage() CreateImageInput {
	return CreateImageInput{
		Name:   "My Web!",
		Config: Descriptor{MediaType: "application/vnd.oci.image.config.v1+json", Digest: digestOf(configBytes), Size: int64(len(configBytes))},
		Layers: []Descriptor{{MediaType: "application/vnd.oci.image.layer.v1.tar", Digest: digestOf(layerBytes), Size: int64(len(layerBytes))}},
	}
}

func TestMissingBlobsListsWhatTheRegistryLacks(t *testing.T) {
	f := newUploadFixture(t, true)
	f.preload(t, configBytes)
	absent := digestOf(layerBytes)
	got, err := f.uc.MissingBlobs(as(uploader), exerciseID, []string{digestOf(configBytes), absent, absent})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != absent {
		t.Fatalf("missing %v", got)
	}
	got, err = f.uc.MissingBlobs(as(uploader), exerciseID, []string{digestOf(configBytes)})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("nothing missing: %v %v", got, err)
	}
}

func TestMissingBlobsRefusesABadDigest(t *testing.T) {
	f := newUploadFixture(t, true)
	for _, d := range []string{"", "sha256:abc", "md5:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("A", 64)} {
		if _, err := f.uc.MissingBlobs(as(uploader), exerciseID, []string{d}); !errors.Is(err, imageRegistryModel.ErrInvalidBlobDigest.Err()) {
			t.Fatalf("%q: %v", d, err)
		}
	}
}

func TestPushStagedBlobStreamsTheFileIntoTheRegistry(t *testing.T) {
	f := newUploadFixture(t, true)
	f.media.stage(stagedID, uploader, layerBytes)
	err := f.uc.PushStagedBlob(as(uploader), exerciseID, PushBlobInput{FileID: stagedID, Digest: digestOf(layerBytes), Size: int64(len(layerBytes))})
	if err != nil {
		t.Fatal(err)
	}
	if !f.has(t, digestOf(layerBytes)) || f.media.streamed != 1 {
		t.Fatalf("pushed=%v streamed=%d", f.has(t, digestOf(layerBytes)), f.media.streamed)
	}
	// Pushing the same blob again (a retried request) does not stream it again.
	if err = f.uc.PushStagedBlob(as(uploader), exerciseID, PushBlobInput{FileID: stagedID, Digest: digestOf(layerBytes), Size: int64(len(layerBytes))}); err != nil || f.media.streamed != 1 {
		t.Fatalf("again: %v streamed=%d", err, f.media.streamed)
	}
}

func TestPushStagedBlobDigestMismatchPushesNothing(t *testing.T) {
	f := newUploadFixture(t, true)
	f.media.stage(stagedID, uploader, layerBytes)
	claimed := digestOf([]byte("other bytes"))
	err := f.uc.PushStagedBlob(as(uploader), exerciseID, PushBlobInput{FileID: stagedID, Digest: claimed, Size: int64(len(layerBytes))})
	if !errors.Is(err, imageRegistryModel.ErrBlobDigestMismatch.Err()) {
		t.Fatalf("hash: %v", err)
	}
	// The right hash with a wrong size is a mismatch too.
	err = f.uc.PushStagedBlob(as(uploader), exerciseID, PushBlobInput{FileID: stagedID, Digest: digestOf(layerBytes), Size: int64(len(layerBytes)) + 1})
	if !errors.Is(err, imageRegistryModel.ErrBlobDigestMismatch.Err()) {
		t.Fatalf("size: %v", err)
	}
	if f.media.streamed != 0 || f.has(t, claimed) || f.has(t, digestOf(layerBytes)) {
		t.Fatalf("something was pushed: streamed=%d", f.media.streamed)
	}
}

func TestPushStagedBlobOfAnotherUserIsNotFound(t *testing.T) {
	f := newUploadFixture(t, true)
	f.media.stage(stagedID, uploader, layerBytes)
	in := PushBlobInput{FileID: stagedID, Digest: digestOf(layerBytes), Size: int64(len(layerBytes))}
	for name, ctx := range map[string]context.Context{"stranger": as(stranger), "anonymous": context.Background()} {
		if err := f.uc.PushStagedBlob(ctx, exerciseID, in); !errors.Is(err, imageRegistryModel.ErrStagedBlobNotFound.Err()) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	absent := in
	absent.FileID = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000f9")
	if err := f.uc.PushStagedBlob(as(uploader), exerciseID, absent); !errors.Is(err, imageRegistryModel.ErrStagedBlobNotFound.Err()) {
		t.Fatalf("absent: %v", err)
	}
	if f.media.streamed != 0 || f.has(t, in.Digest) {
		t.Fatal("a foreign file was pushed")
	}
}

func TestCreateImagePushesTheManifestThenRecordsIt(t *testing.T) {
	f := newUploadFixture(t, true)
	f.preload(t, configBytes, layerBytes)
	var written []postgres.UpsertPlatformImageParams
	f.q.EXPECT().UpsertPlatformImage(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.UpsertPlatformImageParams) error {
			written = append(written, p)
			return nil
		}).Times(2)

	in := testImage()
	res, err := f.uc.CreateImage(as(uploader), exerciseID, in)
	if err != nil {
		t.Fatal(err)
	}
	m, err := imageRegistryModel.BuildManifest(in.Config, in.Layers)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := imageRegistryModel.NewPlatformRef("my-web", m.Digest)
	if res.Reference != ref.String() {
		t.Fatalf("reference %q, want %q", res.Reference, ref.String())
	}
	if ok, err := f.registry().ManifestExists(context.Background(), ref.Tag()); err != nil || !ok {
		t.Fatalf("tag %s pushed=%v err=%v", ref.Tag(), ok, err)
	}
	want := postgres.UpsertPlatformImageParams{
		Digest: m.Digest, Tag: ref.Tag(), Name: "my-web",
		ExerciseID: uuid.NullUUID{UUID: exerciseID, Valid: true}, UploadedBy: uuid.NullUUID{UUID: uploader, Valid: true}, UploadedAt: t0,
	}
	if len(written) != 1 || written[0] != want {
		t.Fatalf("written %+v", written)
	}

	// Uploading the same image again later refreshes its upload time.
	f.advance(3600e9)
	if _, err = f.uc.CreateImage(as(uploader), exerciseID, in); err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 || !written[1].UploadedAt.Equal(t0.Add(3600e9)) || written[1].Digest != m.Digest {
		t.Fatalf("second %+v", written)
	}
}

func TestCreateImageWithAMissingBlobRecordsNothing(t *testing.T) {
	f := newUploadFixture(t, true)
	f.preload(t, configBytes) // the layer was never pushed
	_, err := f.uc.CreateImage(as(uploader), exerciseID, testImage())
	if !errors.Is(err, imageRegistryModel.ErrBlobMissing.Err()) {
		t.Fatalf("err %v", err)
	}
	if ctx := publicReason(t, err); ctx["digest"] != digestOf(layerBytes) {
		t.Fatalf("context %v", ctx)
	}
	// No UpsertPlatformImage expectation: gomock fails the test on any write.
}

func TestCreateImageRefusesAnUnknownLayerType(t *testing.T) {
	f := newUploadFixture(t, true)
	f.preload(t, configBytes, layerBytes)
	in := testImage()
	in.Layers[0].MediaType = "application/vnd.oci.image.layer.nondistributable.v1.tar"
	if _, err := f.uc.CreateImage(as(uploader), exerciseID, in); !errors.Is(err, imageRegistryModel.ErrInvalidImageArchive.Err()) {
		t.Fatalf("err %v", err)
	}
}

func TestUploadIsRefusedWhenTheRegistryIsNotConfigured(t *testing.T) {
	f := newUploadFixture(t, false)
	f.media.stage(stagedID, uploader, layerBytes)
	ctx := as(uploader)
	if _, err := f.uc.MissingBlobs(ctx, exerciseID, []string{digestOf(layerBytes)}); !errors.Is(err, imageRegistryModel.ErrRegistryNotConfigured.Err()) {
		t.Fatalf("missing: %v", err)
	}
	if err := f.uc.PushStagedBlob(ctx, exerciseID, PushBlobInput{FileID: stagedID, Digest: digestOf(layerBytes), Size: int64(len(layerBytes))}); !errors.Is(err, imageRegistryModel.ErrRegistryNotConfigured.Err()) {
		t.Fatalf("push: %v", err)
	}
	if _, err := f.uc.CreateImage(ctx, exerciseID, testImage()); !errors.Is(err, imageRegistryModel.ErrRegistryNotConfigured.Err()) {
		t.Fatalf("create: %v", err)
	}
	if f.media.asked != 0 || f.media.streamed != 0 {
		t.Fatal("the staged file was touched")
	}
}

func TestUploadIsRefusedWhenTheRegistryFails(t *testing.T) {
	f := newUploadFixture(t, true)
	f.media.stage(stagedID, uploader, layerBytes)
	f.reg.srv.Close()
	ctx := as(uploader)
	check := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, imageRegistryModel.ErrRegistryUnavailable.Err()) {
			t.Fatalf("%s: %v", name, err)
		}
		if c := publicReason(t, err); c["reason"] != string(imageRegistryModel.ReasonUnreachable) || c["token"] != "push" {
			t.Fatalf("%s context %v", name, c)
		}
		if strings.Contains(err.Error(), pushToken) {
			t.Fatalf("%s leaks the token", name)
		}
	}
	_, err := f.uc.MissingBlobs(ctx, exerciseID, []string{digestOf(layerBytes)})
	check("missing", err)
	check("push", f.uc.PushStagedBlob(ctx, exerciseID, PushBlobInput{FileID: stagedID, Digest: digestOf(layerBytes), Size: int64(len(layerBytes))}))
	_, err = f.uc.CreateImage(ctx, exerciseID, testImage())
	check("create", err)
	// No UpsertPlatformImage expectation: nothing is recorded.
}

// A registry that refuses the push token mid-upload: the refusal names the reason for admins.
func TestUploadUnauthorizedNamesTheReason(t *testing.T) {
	f := newUploadFixture(t, true)
	f.reg.allow[pushToken] = false
	_, err := f.uc.MissingBlobs(as(uploader), exerciseID, []string{digestOf(layerBytes)})
	if !errors.Is(err, imageRegistryModel.ErrRegistryUnavailable.Err()) || publicReason(t, err)["reason"] != string(imageRegistryModel.ReasonUnauthorized) {
		t.Fatalf("err %v", err)
	}
}
