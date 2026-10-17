package imageRegistryUseCase

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/pkg/ociregistry"
)

var (
	uploader   = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000a1")
	stranger   = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000a2")
	exerciseID = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000e1")
	uploadID   = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000f1")

	layerBytes  = []byte("fixed layer bytes of the test image")
	configBytes = []byte(fmt.Sprintf(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[%q]}}`, digestOf(layerBytes)))
)

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// fakeMedia is the chunked uploads: one upload per id with its bytes.
type fakeMedia struct {
	mu      sync.Mutex
	uploads map[uuid.UUID]mediaModel.Upload
	data    map[uuid.UUID][]byte
	asked   int
	opened  int
	dropped []uuid.UUID
	// failAfter > 0: the upload's reader fails after that many bytes (a chunk object gone).
	failAfter int
	// blockAfter > 0: the upload's reader blocks after that many bytes until the push's context ends.
	blockAfter int
}

// blockingReader returns its bytes up to n, then blocks until ctx ends and returns ctx.Err() (a chunk read cut
// off by the job's timeout).
type blockingReader struct {
	ctx context.Context
	r   io.Reader
	n   int
}

func (b *blockingReader) Read(p []byte) (int, error) {
	if b.n <= 0 {
		<-b.ctx.Done()
		return 0, b.ctx.Err()
	}
	if len(p) > b.n {
		p = p[:b.n]
	}
	n, err := b.r.Read(p)
	b.n -= n
	return n, err
}

// failingReader returns its bytes up to n, then a storage error.
type failingReader struct {
	r io.Reader
	n int
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("chunk object not found")
	}
	if len(p) > f.n {
		p = p[:f.n]
	}
	n, err := f.r.Read(p)
	f.n -= n
	return n, err
}

func newFakeMedia() *fakeMedia {
	return &fakeMedia{uploads: map[uuid.UUID]mediaModel.Upload{}, data: map[uuid.UUID][]byte{}}
}

// stage records a complete image-blob upload of owner holding b.
func (m *fakeMedia) stage(id, owner uuid.UUID, b []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	up := mediaModel.NewUpload(id, owner, "sha256-x", "application/octet-stream", int64(len(b)), 4, time.Hour, t0, mediaModel.UploadPurposeImageBlob)
	up.ChunksReceived = up.ChunkCount()
	m.uploads[id], m.data[id] = up, b
}

func (m *fakeMedia) UploadStatus(_ context.Context, id, owner uuid.UUID) (mediaModel.Upload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.asked++
	up, ok := m.uploads[id]
	if !ok || up.CreatedBy != owner {
		return mediaModel.Upload{}, mediaModel.ErrUploadNotFound.Err()
	}
	return up, nil
}

func (m *fakeMedia) OpenUpload(ctx context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.Upload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.opened++
	up, ok := m.uploads[id]
	if !ok {
		return nil, mediaModel.Upload{}, mediaModel.ErrUploadNotFound.Err()
	}
	if m.blockAfter > 0 {
		return io.NopCloser(&blockingReader{ctx: ctx, r: bytes.NewReader(m.data[id]), n: m.blockAfter}), up, nil
	}
	if m.failAfter > 0 {
		return io.NopCloser(&failingReader{r: bytes.NewReader(m.data[id]), n: m.failAfter}), up, nil
	}
	return io.NopCloser(bytes.NewReader(m.data[id])), up, nil
}

func (m *fakeMedia) DropUpload(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.uploads, id)
	m.dropped = append(m.dropped, id)
	return nil
}

func (m *fakeMedia) has(id uuid.UUID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.uploads[id]
	return ok
}

type uploadFixture struct {
	*accessFixture
	media  *fakeMedia
	queued []jobsModel.ImageBlobPushArgs
	// pushes is the image_blob_pushes table.
	pushes map[string]postgres.ImageBlobPush
}

func newUploadFixture(t *testing.T, configured bool) *uploadFixture {
	media := newFakeMedia()
	f := &uploadFixture{media: media, pushes: map[string]postgres.ImageBlobPush{}}
	f.accessFixture = newAccessFixture(t, configured, func(d *Dependencies) {
		d.Media = media
		d.QueueBlobPush = func(_ context.Context, args jobsModel.ImageBlobPushArgs) error {
			f.queued = append(f.queued, args)
			return nil
		}
	})
	f.expectPushRows()
	return f
}

// expectPushRows backs the image_blob_pushes queries with f.pushes.
func (f *uploadFixture) expectPushRows() {
	f.q.EXPECT().UpsertImageBlobPush(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, p postgres.UpsertImageBlobPushParams) error {
		// Like pgx: a finished context writes nothing.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		f.pushes[p.Digest] = postgres.ImageBlobPush(p)
		return nil
	}).AnyTimes()
	f.q.EXPECT().GetImageBlobPush(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, d string) (postgres.ImageBlobPush, error) {
		p, ok := f.pushes[d]
		if !ok {
			return postgres.ImageBlobPush{}, pgx.ErrNoRows
		}
		return p, nil
	}).AnyTimes()
	f.q.EXPECT().DeleteImageBlobPush(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, d string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delete(f.pushes, d)
		return nil
	}).AnyTimes()
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

func layerPush() PushBlobInput {
	return PushBlobInput{UploadID: uploadID, Digest: digestOf(layerBytes), Size: int64(len(layerBytes))}
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
	got, err := f.uc.MissingBlobs(as(uploader), []string{digestOf(configBytes), absent, absent})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != absent {
		t.Fatalf("missing %v", got)
	}
	got, err = f.uc.MissingBlobs(as(uploader), []string{digestOf(configBytes)})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("nothing missing: %v %v", got, err)
	}
}

// Many digests are checked in parallel and come back in the order sent.
func TestMissingBlobsKeepsTheOrderWithParallelChecks(t *testing.T) {
	f := newUploadFixture(t, true)
	var digests, want []string
	for i := 0; i < 40; i++ {
		b := []byte(fmt.Sprintf("blob %02d", i))
		if i%3 == 0 {
			f.preload(t, b)
		} else {
			want = append(want, digestOf(b))
		}
		digests = append(digests, digestOf(b))
	}
	got, err := f.uc.MissingBlobs(as(uploader), digests)
	if err != nil || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestMissingBlobsRefusesABadDigest(t *testing.T) {
	f := newUploadFixture(t, true)
	for _, d := range []string{"", "sha256:abc", "md5:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("A", 64)} {
		if _, err := f.uc.MissingBlobs(as(uploader), []string{d}); !errors.Is(err, imageRegistryModel.ErrInvalidBlobDigest.Err()) {
			t.Fatalf("%q: %v", d, err)
		}
	}
}

// The request only queues the push; the job streams the upload into the registry, then drops it.
func TestPushStagedBlobQueuesThePushAndTheJobStreamsIt(t *testing.T) {
	f := newUploadFixture(t, true)
	f.media.stage(uploadID, uploader, layerBytes)
	st, err := f.uc.PushStagedBlob(as(uploader), layerPush())
	if err != nil || st.State != imageRegistryModel.BlobPushing {
		t.Fatalf("state %+v err %v", st, err)
	}
	want := jobsModel.ImageBlobPushArgs{Digest: digestOf(layerBytes), UploadID: uploadID, Size: int64(len(layerBytes))}
	if len(f.queued) != 1 || f.queued[0] != want || f.media.opened != 0 || f.has(t, want.Digest) {
		t.Fatalf("queued %+v opened %d", f.queued, f.media.opened)
	}
	if p := f.pushes[want.Digest]; p.State != "pushing" || !p.UpdatedAt.Equal(t0) {
		t.Fatalf("push row %+v", p)
	}
	if st, _ = f.uc.ImageBlobStatus(as(uploader), want.Digest); st.State != imageRegistryModel.BlobPushing {
		t.Fatalf("status while pushing %+v", st)
	}

	if err = f.uc.PushImageBlob(context.Background(), f.queued[0]); err != nil {
		t.Fatal(err)
	}
	if !f.has(t, want.Digest) || f.media.has(uploadID) {
		t.Fatalf("pushed=%v upload left=%v", f.has(t, want.Digest), f.media.has(uploadID))
	}
	if _, ok := f.pushes[want.Digest]; ok {
		t.Fatal("the push row stays after success")
	}
	if st, _ = f.uc.ImageBlobStatus(as(uploader), want.Digest); st.State != imageRegistryModel.BlobPresent {
		t.Fatalf("status after %+v", st)
	}
}

func TestPushStagedBlobAlreadyPresentQueuesNothing(t *testing.T) {
	f := newUploadFixture(t, true)
	f.preload(t, layerBytes)
	f.media.stage(uploadID, uploader, layerBytes)
	st, err := f.uc.PushStagedBlob(as(uploader), layerPush())
	if err != nil || st.State != imageRegistryModel.BlobPresent || len(f.queued) != 0 || f.media.has(uploadID) {
		t.Fatalf("state %+v err %v queued %d", st, err, len(f.queued))
	}
}

func TestPushStagedBlobChecksTheUpload(t *testing.T) {
	f := newUploadFixture(t, true)
	f.media.stage(uploadID, uploader, layerBytes)

	// Another user's upload, or one that does not exist: not found, indistinguishable.
	if _, err := f.uc.PushStagedBlob(as(stranger), layerPush()); !errors.Is(err, imageRegistryModel.ErrStagedBlobNotFound.Err()) {
		t.Fatalf("stranger: %v", err)
	}
	absent := layerPush()
	absent.UploadID = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000f9")
	if _, err := f.uc.PushStagedBlob(as(uploader), absent); !errors.Is(err, imageRegistryModel.ErrStagedBlobNotFound.Err()) {
		t.Fatalf("absent: %v", err)
	}
	if _, err := f.uc.PushStagedBlob(context.Background(), layerPush()); !errors.Is(err, imageRegistryModel.ErrUploaderUnknown.Err()) {
		t.Fatalf("anonymous: %v", err)
	}
	// The declared size is not the upload's.
	wrong := layerPush()
	wrong.Size++
	if _, err := f.uc.PushStagedBlob(as(uploader), wrong); !errors.Is(err, imageRegistryModel.ErrBlobDigestMismatch.Err()) {
		t.Fatalf("size: %v", err)
	}
	// An ordinary file upload, or one still missing chunks.
	up := f.media.uploads[uploadID]
	up.Purpose = mediaModel.UploadPurposeFile
	f.media.uploads[uploadID] = up
	if _, err := f.uc.PushStagedBlob(as(uploader), layerPush()); !errors.Is(err, imageRegistryModel.ErrBlobUploadNotReady.Err()) {
		t.Fatalf("file purpose: %v", err)
	}
	up.Purpose, up.ChunksReceived = mediaModel.UploadPurposeImageBlob, up.ChunkCount()-1
	f.media.uploads[uploadID] = up
	if _, err := f.uc.PushStagedBlob(as(uploader), layerPush()); !errors.Is(err, imageRegistryModel.ErrBlobUploadNotReady.Err()) {
		t.Fatalf("incomplete: %v", err)
	}
	if len(f.queued) != 0 || len(f.pushes) != 0 {
		t.Fatalf("queued %v", f.queued)
	}
}

// Bytes that do not hash to the digest: the job records digest_mismatch, nothing is stored, the upload is dropped.
func TestPushImageBlobDigestMismatchFails(t *testing.T) {
	f := newUploadFixture(t, true)
	f.media.stage(uploadID, uploader, []byte("other bytes of the same length here!"[:len(layerBytes)]))
	args := jobsModel.ImageBlobPushArgs{Digest: digestOf(layerBytes), UploadID: uploadID, Size: int64(len(layerBytes))}
	if err := f.uc.PushImageBlob(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if f.has(t, args.Digest) || f.media.has(uploadID) {
		t.Fatal("a mismatching blob was stored or kept")
	}
	st, err := f.uc.ImageBlobStatus(as(uploader), args.Digest)
	if err != nil || st.State != imageRegistryModel.BlobFailed || st.Reason != imageRegistryModel.ReasonDigestMismatch {
		t.Fatalf("status %+v err %v", st, err)
	}
}

// The registry failing during the job: failed with the reason; the upload is kept for a retry.
func TestPushImageBlobRegistryFailureKeepsTheUpload(t *testing.T) {
	f := newUploadFixture(t, true)
	f.media.stage(uploadID, uploader, layerBytes)
	f.reg.allow[pushToken] = false
	args := jobsModel.ImageBlobPushArgs{Digest: digestOf(layerBytes), UploadID: uploadID, Size: int64(len(layerBytes))}
	if err := f.uc.PushImageBlob(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if p := f.pushes[args.Digest]; p.State != "failed" || p.Reason != string(imageRegistryModel.ReasonUnauthorized) {
		t.Fatalf("row %+v", p)
	}
	if !f.media.has(uploadID) {
		t.Fatal("the upload must stay for a retry")
	}
}

func TestPushImageBlobUploadGone(t *testing.T) {
	f := newUploadFixture(t, true)
	args := jobsModel.ImageBlobPushArgs{Digest: digestOf(layerBytes), UploadID: uploadID, Size: int64(len(layerBytes))}
	if err := f.uc.PushImageBlob(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if p := f.pushes[args.Digest]; p.State != "failed" || p.Reason != string(imageRegistryModel.ReasonUploadMissing) {
		t.Fatalf("row %+v", p)
	}
}

func TestImageBlobStatusMissingAndStale(t *testing.T) {
	f := newUploadFixture(t, true)
	d := digestOf(layerBytes)
	if st, err := f.uc.ImageBlobStatus(as(uploader), d); err != nil || st.State != imageRegistryModel.BlobMissing {
		t.Fatalf("missing %+v %v", st, err)
	}
	// A push that has said nothing for longer than a push can take is reported failed (unreachable).
	f.pushes[d] = postgres.ImageBlobPush{Digest: d, State: "pushing", UpdatedAt: t0}
	f.advance(imageRegistryModel.BlobPushStaleAfter + time.Second)
	if st, err := f.uc.ImageBlobStatus(as(uploader), d); err != nil || st.State != imageRegistryModel.BlobFailed || st.Reason != imageRegistryModel.ReasonUnreachable {
		t.Fatalf("stale %+v %v", st, err)
	}
	if _, err := f.uc.ImageBlobStatus(as(uploader), "sha256:x"); !errors.Is(err, imageRegistryModel.ErrInvalidBlobDigest.Err()) {
		t.Fatalf("bad digest %v", err)
	}
}

func TestCreateImageRecordsThenPushesTheManifest(t *testing.T) {
	f := newUploadFixture(t, true)
	f.preload(t, configBytes, layerBytes)
	in := testImage()
	m, err := imageRegistryModel.BuildManifest(in.Config, in.Layers)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := imageRegistryModel.NewPlatformRef("my-web", m.Digest)
	f.q.EXPECT().PlatformImageTagTakenByOtherDigest(gomock.Any(), postgres.PlatformImageTagTakenByOtherDigestParams{Tag: ref.Tag(), Digest: m.Digest}).
		Return(false, nil).Times(2)
	var written []postgres.UpsertPlatformImageParams
	f.q.EXPECT().UpsertPlatformImage(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p postgres.UpsertPlatformImageParams) error {
			// The row comes first: the tag must not exist yet.
			if ok, _ := f.registry().ManifestExists(context.Background(), ref.Tag()); ok && len(written) == 0 {
				t.Fatal("the manifest was pushed before the row was written")
			}
			written = append(written, p)
			return nil
		}).Times(2)

	res, err := f.uc.CreateImage(as(uploader), exerciseID, in)
	if err != nil {
		t.Fatal(err)
	}
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
	f.advance(time.Hour)
	if _, err = f.uc.CreateImage(as(uploader), exerciseID, in); err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 || !written[1].UploadedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("second %+v", written)
	}
}

// A tag another digest already maps to is never re-pointed (full-digest tags make this a bug, not an upload):
// nothing is recorded and no manifest is pushed.
func TestCreateImageRefusesATagOfAnotherDigest(t *testing.T) {
	f := newUploadFixture(t, true)
	f.preload(t, configBytes, layerBytes)
	in := testImage()
	m, err := imageRegistryModel.BuildManifest(in.Config, in.Layers)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := imageRegistryModel.NewPlatformRef("my-web", m.Digest)
	f.q.EXPECT().PlatformImageTagTakenByOtherDigest(gomock.Any(), postgres.PlatformImageTagTakenByOtherDigestParams{Tag: ref.Tag(), Digest: m.Digest}).
		Return(true, nil)
	// No UpsertPlatformImage expectation: nothing is recorded.

	if _, err = f.uc.CreateImage(as(uploader), exerciseID, in); !errors.Is(err, imageRegistryModel.ErrImageTagTaken.Err()) {
		t.Fatalf("err %v", err)
	}
	if ok, err := f.registry().ManifestExists(context.Background(), ref.Tag()); err != nil || ok {
		t.Fatalf("tag %s pushed=%v err=%v", ref.Tag(), ok, err)
	}
}

// The pre-check races a concurrent upload of another digest under the same tag: the UNIQUE (tag) violation of the
// row write is the same refusal, and no manifest is pushed.
func TestCreateImageMapsATagUniqueViolation(t *testing.T) {
	f := newUploadFixture(t, true)
	f.preload(t, configBytes, layerBytes)
	in := testImage()
	m, err := imageRegistryModel.BuildManifest(in.Config, in.Layers)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := imageRegistryModel.NewPlatformRef("my-web", m.Digest)
	f.q.EXPECT().PlatformImageTagTakenByOtherDigest(gomock.Any(), gomock.Any()).Return(false, nil)
	f.q.EXPECT().UpsertPlatformImage(gomock.Any(), gomock.Any()).
		Return(&pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "platform_images_tag_key"})

	if _, err = f.uc.CreateImage(as(uploader), exerciseID, in); !errors.Is(err, imageRegistryModel.ErrImageTagTaken.Err()) {
		t.Fatalf("err %v", err)
	}
	if ok, err := f.registry().ManifestExists(context.Background(), ref.Tag()); err != nil || ok {
		t.Fatalf("tag %s pushed=%v err=%v", ref.Tag(), ok, err)
	}
}

// The registry's sizes count, not the client's: a wrong claimed size is refused before anything is written.
func TestCreateImageRefusesAWrongClaimedSize(t *testing.T) {
	f := newUploadFixture(t, true)
	f.preload(t, configBytes, layerBytes)
	in := testImage()
	in.Layers[0].Size = 1
	if _, err := f.uc.CreateImage(as(uploader), exerciseID, in); !errors.Is(err, imageRegistryModel.ErrBlobSizeMismatch.Err()) {
		t.Fatalf("err %v", err)
	}
}

func TestCreateImageWithAMissingBlobRecordsNothing(t *testing.T) {
	f := newUploadFixture(t, true)
	f.preload(t, configBytes)
	_, err := f.uc.CreateImage(as(uploader), exerciseID, testImage())
	if !errors.Is(err, imageRegistryModel.ErrBlobMissing.Err()) {
		t.Fatalf("err %v", err)
	}
	if ctx := publicReason(t, err); ctx["digest"] != digestOf(layerBytes) {
		t.Fatalf("context %v", ctx)
	}
}

func TestCreateImageChecksTheConfig(t *testing.T) {
	for name, cfg := range map[string][]byte{
		"arm64":       bytes.Replace(configBytes, []byte("amd64"), []byte("arm64"), 1),
		"layer count": []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`),
	} {
		f := newUploadFixture(t, true)
		f.preload(t, cfg, layerBytes)
		in := testImage()
		in.Config.Digest, in.Config.Size = digestOf(cfg), int64(len(cfg))
		if _, err := f.uc.CreateImage(as(uploader), exerciseID, in); !errors.Is(err, imageRegistryModel.ErrInvalidImageConfig.Err()) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestCreateImageRefusesAnUnknownLayerTypeAndAnonymous(t *testing.T) {
	f := newUploadFixture(t, true)
	f.preload(t, configBytes, layerBytes)
	in := testImage()
	in.Layers[0].MediaType = "application/vnd.oci.image.layer.nondistributable.v1.tar"
	if _, err := f.uc.CreateImage(as(uploader), exerciseID, in); !errors.Is(err, imageRegistryModel.ErrInvalidImageArchive.Err()) {
		t.Fatalf("type: %v", err)
	}
	if _, err := f.uc.CreateImage(context.Background(), exerciseID, testImage()); !errors.Is(err, imageRegistryModel.ErrUploaderUnknown.Err()) {
		t.Fatalf("anonymous: %v", err)
	}
}

func TestUploadIsRefusedWhenTheRegistryIsNotConfigured(t *testing.T) {
	f := newUploadFixture(t, false)
	f.media.stage(uploadID, uploader, layerBytes)
	ctx := as(uploader)
	notConfigured := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, imageRegistryModel.ErrRegistryNotConfigured.Err()) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	_, err := f.uc.MissingBlobs(ctx, []string{digestOf(layerBytes)})
	notConfigured("missing", err)
	_, err = f.uc.PushStagedBlob(ctx, layerPush())
	notConfigured("push", err)
	_, err = f.uc.ImageBlobStatus(ctx, digestOf(layerBytes))
	notConfigured("status", err)
	_, err = f.uc.CreateImage(ctx, exerciseID, testImage())
	notConfigured("create", err)
	if f.media.asked != 0 || len(f.queued) != 0 {
		t.Fatal("the upload was touched")
	}
	// A queued push whose registry went away fails with not_configured and keeps the upload.
	if err = f.uc.PushImageBlob(context.Background(), jobsModel.ImageBlobPushArgs{Digest: digestOf(layerBytes), UploadID: uploadID, Size: int64(len(layerBytes))}); err != nil {
		t.Fatal(err)
	}
	if p := f.pushes[digestOf(layerBytes)]; p.State != "failed" || p.Reason != string(imageRegistryModel.ReasonNotConfigured) || !f.media.has(uploadID) {
		t.Fatalf("row %+v", p)
	}
}

func TestUploadIsRefusedWhenTheRegistryFails(t *testing.T) {
	f := newUploadFixture(t, true)
	f.media.stage(uploadID, uploader, layerBytes)
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
	_, err := f.uc.MissingBlobs(ctx, []string{digestOf(layerBytes)})
	check("missing", err)
	_, err = f.uc.PushStagedBlob(ctx, layerPush())
	check("push", err)
	_, err = f.uc.ImageBlobStatus(ctx, digestOf(layerBytes))
	check("status", err)
	_, err = f.uc.CreateImage(ctx, exerciseID, testImage())
	check("create", err)
	// No UpsertPlatformImage expectation: nothing is recorded.
}

func TestUploadUnauthorizedNamesTheReason(t *testing.T) {
	f := newUploadFixture(t, true)
	f.reg.allow[pushToken] = false
	_, err := f.uc.MissingBlobs(as(uploader), []string{digestOf(layerBytes)})
	if !errors.Is(err, imageRegistryModel.ErrRegistryUnavailable.Err()) || publicReason(t, err)["reason"] != string(imageRegistryModel.ReasonUnauthorized) {
		t.Fatalf("err %v", err)
	}
}

// The job's context ends (timeout, shutdown): the push still ends recorded as failed, never "pushing".
func TestPushImageBlobCancelledContextRecordsFailed(t *testing.T) {
	f := newUploadFixture(t, true)
	f.media.stage(uploadID, uploader, layerBytes)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	args := jobsModel.ImageBlobPushArgs{Digest: digestOf(layerBytes), UploadID: uploadID, Size: int64(len(layerBytes))}
	if err := f.uc.PushImageBlob(ctx, args); err != nil {
		t.Fatal(err)
	}
	if p := f.pushes[args.Digest]; p.State != "failed" || p.Reason != string(imageRegistryModel.ReasonUnreachable) {
		t.Fatalf("row %+v", p)
	}
	if !f.media.has(uploadID) {
		t.Fatal("the upload must stay for a retry")
	}
}

// Queueing fails after the "pushing" row was written: the row is removed, the blob reads missing again.
func TestPushStagedBlobQueueFailureLeavesNoPushingRow(t *testing.T) {
	media := newFakeMedia()
	f := &uploadFixture{media: media, pushes: map[string]postgres.ImageBlobPush{}}
	f.accessFixture = newAccessFixture(t, true, func(d *Dependencies) {
		d.Media = media
		d.QueueBlobPush = func(context.Context, jobsModel.ImageBlobPushArgs) error { return errors.New("queue down") }
	})
	f.expectPushRows()
	media.stage(uploadID, uploader, layerBytes)
	if _, err := f.uc.PushStagedBlob(as(uploader), layerPush()); err == nil {
		t.Fatal("queue failure must be an error")
	}
	if _, ok := f.pushes[digestOf(layerBytes)]; ok {
		t.Fatal("a pushing row stayed without a job")
	}
}

// A chunk object missing in the middle of the push: upload_missing, not unreachable.
func TestPushImageBlobMissingChunkIsUploadMissing(t *testing.T) {
	f := newUploadFixture(t, true)
	f.media.stage(uploadID, uploader, layerBytes)
	f.media.failAfter = 10
	args := jobsModel.ImageBlobPushArgs{Digest: digestOf(layerBytes), UploadID: uploadID, Size: int64(len(layerBytes))}
	if err := f.uc.PushImageBlob(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if p := f.pushes[args.Digest]; p.State != "failed" || p.Reason != string(imageRegistryModel.ReasonUploadMissing) {
		t.Fatalf("row %+v", p)
	}
	if f.has(t, args.Digest) {
		t.Fatal("a partial blob was stored")
	}
}

// The job times out while a chunk is being read: that is the job's deadline, not a broken upload — unreachable,
// and the upload is kept.
func TestPushImageBlobTimeoutMidChunkIsUnreachable(t *testing.T) {
	f := newUploadFixture(t, true)
	f.media.stage(uploadID, uploader, layerBytes)
	f.media.blockAfter = 10
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	args := jobsModel.ImageBlobPushArgs{Digest: digestOf(layerBytes), UploadID: uploadID, Size: int64(len(layerBytes))}
	if err := f.uc.PushImageBlob(ctx, args); err != nil {
		t.Fatal(err)
	}
	if p := f.pushes[args.Digest]; p.State != "failed" || p.Reason != string(imageRegistryModel.ReasonUnreachable) {
		t.Fatalf("row %+v", p)
	}
	if !f.media.has(uploadID) {
		t.Fatal("the upload must stay for a retry")
	}
}
