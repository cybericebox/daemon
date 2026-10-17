package imageRegistryUseCase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/model"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/pkg/ociregistry"
)

// Image upload through the site. The browser reads the blob digests of a `docker save` archive, asks which ones
// the platform registry lacks (MissingBlobs), sends each missing blob through the chunked media upload (purpose
// image-blob) and asks for it to be pushed (PushStagedBlob). The push runs as a background job (PushImageBlob),
// never inside an HTTP request: it streams the upload's chunks into the registry, hashing them on the way, and its
// outcome is read with ImageBlobStatus. Last the browser creates the image (CreateImage): the manifest is built
// from blobs the registry has, the image is recorded and the manifest pushed under the platform tag.
//
// Access: the exercise edit access is checked by the route; the use case needs a signed-in caller (the uploader),
// and an upload is only ever pushed by the user who made it. Blob existence is checked platform-wide by design:
// the one platform repository stores each layer once for all tasks, so a layer any task uploaded is not sent
// again.
//
// The registry not configured: ErrRegistryNotConfigured; the registry failing: ErrRegistryUnavailable with the
// reason for admins. CreateImage writes its platform_images row before the manifest push, so a tag never exists
// without a row (a failed push leaves a row the tag collector clears after its grace).

type (
	// MediaSource reads and drops the chunked uploads of image blobs.
	MediaSource interface {
		// UploadStatus returns an upload of owner (somebody else's, expired or absent: ErrUploadNotFound).
		UploadStatus(ctx context.Context, id, owner uuid.UUID) (mediaModel.Upload, error)
		// OpenUpload streams the upload's chunks in order.
		OpenUpload(ctx context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.Upload, error)
		// DropUpload removes the upload and its chunks.
		DropUpload(ctx context.Context, id uuid.UUID) error
	}

	// BlobPushQueue queues the background push of one blob (unique per digest while queued or running).
	BlobPushQueue func(ctx context.Context, args jobsModel.ImageBlobPushArgs) error

	// Descriptor names one blob of an image.
	Descriptor = imageRegistryModel.Descriptor

	// PushBlobInput names a complete chunked upload (purpose image-blob) of the caller and the blob it must be.
	PushBlobInput struct {
		UploadID uuid.UUID
		Digest   string
		Size     int64
	}

	// BlobStatus is where one blob stands: present, pushing, failed (with Reason) or missing.
	BlobStatus struct {
		State  imageRegistryModel.BlobState
		Reason imageRegistryModel.Reason
	}

	// CreateImageInput is an image made of blobs already in the registry.
	CreateImageInput struct {
		// Name is sanitized into the platform image name.
		Name   string
		Config Descriptor
		Layers []Descriptor
	}

	CreateImageResult struct {
		// Reference is platform:<name>@sha256:<manifest digest>, what the device stores.
		Reference string
	}
)

const (
	// pushTokenName names the push token in the refusal's public context.
	pushTokenName = "push"
	// parallelBlobChecks is how many blob HEADs MissingBlobs runs at once.
	parallelBlobChecks = 8
)

// MissingBlobs returns the digests (deduplicated, in the order sent) the platform registry does not have.
func (u *ImageRegistryUseCase) MissingBlobs(ctx context.Context, digests []string) ([]string, error) {
	if err := checkDigests(digests...); err != nil {
		return nil, err
	}
	client, _, err := u.PlatformRegistryClient(ctx, true)
	if err != nil {
		return nil, err
	}
	unique := make([]string, 0, len(digests))
	seen := make(map[string]bool, len(digests))
	for _, d := range digests {
		if !seen[d] {
			seen[d] = true
			unique = append(unique, d)
		}
	}
	present := make([]bool, len(unique))
	errs := make([]error, len(unique))
	sem := make(chan struct{}, parallelBlobChecks)
	var wg sync.WaitGroup
	for i, d := range unique {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			present[i], errs[i] = client.BlobExists(ctx, d)
		}()
	}
	wg.Wait()
	missing := make([]string, 0, len(unique))
	for i, d := range unique {
		if errs[i] != nil {
			return nil, Unavailable(errs[i], pushTokenName)
		}
		if !present[i] {
			missing = append(missing, d)
		}
	}
	return missing, nil
}

// PushStagedBlob asks for a complete image-blob upload of the caller to be pushed as the named blob. A blob the
// registry already has answers present (the upload is dropped); otherwise the push is queued and answers pushing.
func (u *ImageRegistryUseCase) PushStagedBlob(ctx context.Context, in PushBlobInput) (BlobStatus, error) {
	if err := checkDigests(in.Digest); err != nil {
		return BlobStatus{}, err
	}
	client, _, err := u.PlatformRegistryClient(ctx, true)
	if err != nil {
		return BlobStatus{}, err
	}
	user, err := uploaderOf(ctx)
	if err != nil {
		return BlobStatus{}, err
	}
	if u.media == nil || u.queueBlobPush == nil {
		return BlobStatus{}, model.ErrPlatform.WithMessage("Image blob pushes are not wired").Err()
	}
	upload, err := u.media.UploadStatus(ctx, in.UploadID, user)
	if errors.Is(err, mediaModel.ErrUploadNotFound.Err()) {
		return BlobStatus{}, imageRegistryModel.ErrStagedBlobNotFound.WithError(err).Err()
	}
	if err != nil {
		return BlobStatus{}, err
	}
	if upload.Purpose != mediaModel.UploadPurposeImageBlob || !upload.Complete() {
		return BlobStatus{}, imageRegistryModel.ErrBlobUploadNotReady.Err()
	}
	if upload.SizeBytes != in.Size {
		return BlobStatus{}, imageRegistryModel.ErrBlobDigestMismatch.Err()
	}
	exists, err := client.BlobExists(ctx, in.Digest)
	if err != nil {
		return BlobStatus{}, Unavailable(err, pushTokenName)
	}
	if exists {
		u.dropUpload(ctx, in.UploadID)
		return BlobStatus{State: imageRegistryModel.BlobPresent}, nil
	}
	if err = u.blobPushes.Save(ctx, imageRegistryModel.NewBlobPush(in.Digest, u.now())); err != nil {
		return BlobStatus{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record the blob push").Err()
	}
	if err = u.queueBlobPush(ctx, jobsModel.ImageBlobPushArgs{Digest: in.Digest, UploadID: in.UploadID, Size: in.Size}); err != nil {
		// No job: the "pushing" row must not stay (the blob reads missing again).
		if derr := u.blobPushes.Delete(context.WithoutCancel(ctx), in.Digest); derr != nil {
			log.Warn().Err(derr).Str("digest", in.Digest).Msg("image upload: removing the blob push row failed")
		}
		return BlobStatus{}, model.ErrPlatform.WithError(err).WithMessage("Failed to queue the blob push").Err()
	}
	return BlobStatus{State: imageRegistryModel.BlobPushing}, nil
}

// PushImageBlob is the background push (River job image_blob_push): it streams the upload's chunks into the
// registry with the declared size, hashing them on the way. Success removes the push row and the upload. A digest
// mismatch fails the push and drops the upload (its bytes are wrong); a registry failure or a missing upload fails
// it with the reason and keeps the upload, so the client may ask for the push again. Only a failure to record the
// state is returned (River retries the job then). The state is written on a context of its own (not cancelled with
// the job, bounded by stateWriteTimeout), so a push that times out or is cancelled still ends recorded as failed.
// The start marks the push "pushing" again: this run is pushing, whatever an earlier attempt recorded.
func (u *ImageRegistryUseCase) PushImageBlob(ctx context.Context, args jobsModel.ImageBlobPushArgs) error {
	push := imageRegistryModel.NewBlobPush(args.Digest, u.now())
	if err := u.writePushState(ctx, func(c context.Context) error { return u.blobPushes.Save(c, push) }); err != nil {
		return err
	}
	reason, drop := u.streamBlob(ctx, args)
	if reason == "" {
		u.dropUpload(context.WithoutCancel(ctx), args.UploadID)
		return u.writePushState(ctx, func(c context.Context) error { return u.blobPushes.Delete(c, args.Digest) })
	}
	if drop {
		u.dropUpload(context.WithoutCancel(ctx), args.UploadID)
	}
	push.Fail(reason, u.now())
	return u.writePushState(ctx, func(c context.Context) error { return u.blobPushes.Save(c, push) })
}

// stateWriteTimeout bounds one write of a blob push state.
const stateWriteTimeout = 10 * time.Second

// writePushState runs write on a context that outlives the job's (cancellation, timeout) for at most
// stateWriteTimeout.
func (u *ImageRegistryUseCase) writePushState(ctx context.Context, write func(context.Context) error) error {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), stateWriteTimeout)
	defer cancel()
	return write(c)
}

// streamBlob pushes the upload; it returns the failure reason ("" on success) and whether the upload is useless.
func (u *ImageRegistryUseCase) streamBlob(ctx context.Context, args jobsModel.ImageBlobPushArgs) (imageRegistryModel.Reason, bool) {
	client, _, err := u.PlatformRegistryClient(ctx, true)
	if err != nil {
		if errors.Is(err, imageRegistryModel.ErrRegistryNotConfigured.Err()) {
			return imageRegistryModel.ReasonNotConfigured, false
		}
		return imageRegistryModel.ReasonUnreachable, false
	}
	if u.media == nil {
		return imageRegistryModel.ReasonUploadMissing, false
	}
	rc, upload, err := u.media.OpenUpload(ctx, args.UploadID)
	if err != nil {
		return imageRegistryModel.ReasonUploadMissing, false
	}
	defer func() { _ = rc.Close() }()
	if upload.SizeBytes != args.Size {
		return imageRegistryModel.ReasonDigestMismatch, true
	}
	vr := &verifyingReader{r: rc, h: sha256.New(), want: args.Digest}
	if err = client.PushBlob(ctx, args.Digest, args.Size, vr); err != nil {
		if vr.mismatch {
			return imageRegistryModel.ReasonDigestMismatch, true
		}
		if vr.sourceErr != nil {
			// A chunk object could not be read: the upload is broken, not the registry.
			log.Warn().Err(vr.sourceErr).Str("digest", args.Digest).Msg("image upload: reading the blob upload failed")
			return imageRegistryModel.ReasonUploadMissing, false
		}
		log.Warn().Err(err).Str("digest", args.Digest).Msg("image upload: blob push failed")
		return ReasonOf(err), false
	}
	return "", false
}

// verifyingReader hashes what passes through and, at the end, fails the read instead of returning io.EOF when the
// bytes are not the digest: the registry never receives the end of a wrong blob.
// It also remembers a read failure of the upload itself (a chunk object gone), so it is not taken for a registry
// failure.
type verifyingReader struct {
	r         io.Reader
	h         hash.Hash
	want      string
	mismatch  bool
	sourceErr error
}

func (v *verifyingReader) Read(p []byte) (int, error) {
	n, err := v.r.Read(p)
	v.h.Write(p[:n])
	if err != nil && !errors.Is(err, io.EOF) {
		v.sourceErr = err
	}
	if errors.Is(err, io.EOF) && "sha256:"+hex.EncodeToString(v.h.Sum(nil)) != v.want {
		v.mismatch = true
		return n, errBlobDigestMismatch
	}
	return n, err
}

var errBlobDigestMismatch = errors.New("blob bytes do not match the digest")

// ImageBlobStatus reports where a blob stands: present when the registry has it, otherwise the recorded push
// state (a push silent for too long counts as failed), otherwise missing.
func (u *ImageRegistryUseCase) ImageBlobStatus(ctx context.Context, digest string) (BlobStatus, error) {
	if err := checkDigests(digest); err != nil {
		return BlobStatus{}, err
	}
	client, _, err := u.PlatformRegistryClient(ctx, true)
	if err != nil {
		return BlobStatus{}, err
	}
	exists, err := client.BlobExists(ctx, digest)
	if err != nil {
		return BlobStatus{}, Unavailable(err, pushTokenName)
	}
	if exists {
		return BlobStatus{State: imageRegistryModel.BlobPresent}, nil
	}
	push, found, err := u.blobPushes.Get(ctx, digest)
	if err != nil {
		return BlobStatus{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the blob push").Err()
	}
	if !found {
		return BlobStatus{State: imageRegistryModel.BlobMissing}, nil
	}
	state, reason := push.Current(u.now())
	return BlobStatus{State: state, Reason: reason}, nil
}

// CreateImage builds the image manifest from blobs the registry already has, records the image and pushes the
// manifest under the platform tag (PlatformRef.Tag). Every blob must be in the registry with the stated size
// (the registry's sizes count: their total is at most MaxImageArchiveBytes); the config must be linux/amd64 with
// one diff id per layer.
func (u *ImageRegistryUseCase) CreateImage(ctx context.Context, exerciseID uuid.UUID, in CreateImageInput) (CreateImageResult, error) {
	user, err := uploaderOf(ctx)
	if err != nil {
		return CreateImageResult{}, err
	}
	manifest, err := imageRegistryModel.BuildManifest(in.Config, in.Layers)
	if err != nil {
		return CreateImageResult{}, err
	}
	ref, err := imageRegistryModel.NewPlatformRef(imageRegistryModel.SanitizeName(in.Name), manifest.Digest)
	if err != nil {
		return CreateImageResult{}, err
	}
	client, _, err := u.PlatformRegistryClient(ctx, true)
	if err != nil {
		return CreateImageResult{}, err
	}
	if err = checkBlobs(ctx, client, append([]Descriptor{in.Config}, in.Layers...)); err != nil {
		return CreateImageResult{}, err
	}
	if err = u.checkConfig(ctx, client, in.Config, len(in.Layers)); err != nil {
		return CreateImageResult{}, err
	}
	if err = u.images.Save(ctx, imageRegistryModel.NewPlatformImage(ref, exerciseID, user, u.now())); err != nil {
		return CreateImageResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record the uploaded image").Err()
	}
	if _, err = client.PutManifest(ctx, ref.Tag(), manifest.MediaType, manifest.Body); err != nil {
		return CreateImageResult{}, Unavailable(err, pushTokenName)
	}
	return CreateImageResult{Reference: ref.String()}, nil
}

// checkBlobs requires every blob in the registry with exactly the stated size. BuildManifest already bounded the
// stated total, so with equal sizes the registry's total is bounded too.
func checkBlobs(ctx context.Context, client *ociregistry.Client, blobs []Descriptor) error {
	seen := map[string]bool{}
	for _, d := range blobs {
		if seen[d.Digest] {
			continue
		}
		seen[d.Digest] = true
		size, ok, err := client.BlobSize(ctx, d.Digest)
		if err != nil {
			return Unavailable(err, pushTokenName)
		}
		if !ok {
			return imageRegistryModel.ErrBlobMissing.WithPublicContext(imageRegistryModel.ContextDigest, d.Digest).Err()
		}
		if size != d.Size {
			return imageRegistryModel.ErrBlobSizeMismatch.WithPublicContext(imageRegistryModel.ContextDigest, d.Digest).Err()
		}
	}
	return nil
}

// checkConfig reads the config blob (at most MaxImageConfigBytes) and checks it.
func (u *ImageRegistryUseCase) checkConfig(ctx context.Context, client *ociregistry.Client, config Descriptor, layers int) error {
	body := []byte(nil)
	if config.Size <= imageRegistryModel.MaxImageConfigBytes {
		var err error
		body, err = client.GetBlob(ctx, config.Digest, imageRegistryModel.MaxImageConfigBytes)
		if err != nil && !errors.Is(err, ociregistry.ErrBlobTooLarge) {
			return Unavailable(err, pushTokenName)
		}
	}
	return imageRegistryModel.CheckImageConfig(body, layers)
}

// dropUpload removes an upload whose bytes are no longer needed. Best effort: what a failure leaves, the upload
// expiry cleanup takes.
func (u *ImageRegistryUseCase) dropUpload(ctx context.Context, id uuid.UUID) {
	if err := u.media.DropUpload(ctx, id); err != nil {
		log.Warn().Err(err).Str("upload", id.String()).Msg("image upload: dropping the blob upload failed, its expiry removes it")
	}
}

// uploaderOf is the signed-in caller of an image upload call.
func uploaderOf(ctx context.Context) (uuid.UUID, error) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx)
	if !ok || claims.UserID == uuid.Nil {
		return uuid.Nil, imageRegistryModel.ErrUploaderUnknown.Err()
	}
	return claims.UserID, nil
}

// checkDigests refuses any digest that is not sha256 with 64 lowercase hex characters.
func checkDigests(digests ...string) error {
	for _, d := range digests {
		if !imageRegistryModel.ValidDigest(d) {
			return imageRegistryModel.ErrInvalidBlobDigest.Err()
		}
	}
	return nil
}
