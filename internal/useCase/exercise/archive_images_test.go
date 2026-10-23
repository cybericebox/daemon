package exercise_test

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/ociregistry/archivetest"
	"github.com/cybericebox/daemon/pkg/secret"
)

var (
	importer     = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000aa")
	otherUser    = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000ab")
	archiveUpID  = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000f1")
	importJobT0  = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	webImage     = archivetest.Amd64([]byte("fixed web layer"))
	webImageTar  = archivetest.Legacy(webImage)
	toolImageTar = archivetest.OCI(archivetest.Amd64([]byte("fixed tool layer")))
)

// fakeImages is the image registry: it indexes with the real reader and "pushes" by recording; a push names the
// image platform:<sanitized name>@<config digest>.
type fakeImages struct {
	mu         sync.Mutex
	notConfig  bool
	failPushAt int // > 0: the n-th push fails with the registry refusal
	pushes     []imageRegistryUseCase.PushArchiveInput
	indexed    int
	// onPush runs inside every push (a test moves its clock or reads the job there).
	onPush func(n int)
}

func (f *fakeImages) RequireImageUploads(context.Context) error {
	if f.notConfig {
		return imageRegistryModel.ErrRegistryNotConfigured.Err()
	}
	return nil
}

func (f *fakeImages) IndexImageArchive(open ociregistry.ArchiveSource, size int64) (*ociregistry.ImageArchive, error) {
	f.mu.Lock()
	f.indexed++
	f.mu.Unlock()
	a, err := ociregistry.ReadImageArchive(open, size, imageRegistryModel.MaxImageArchiveBytes)
	var refused *ociregistry.ArchiveError
	switch {
	case errors.As(err, &refused):
		return nil, imageRegistryModel.ErrImageArchiveUnreadable.WithPublicContext("reason", refused.Code).Err()
	case errors.Is(err, ociregistry.ErrSourceCorrupt):
		return nil, imageRegistryModel.ErrImageArchiveUnreadable.WithPublicContext("reason", "corrupt").Err()
	case err != nil:
		return nil, model.ErrPlatform.WithError(err).Err()
	}
	return a, nil
}

func (f *fakeImages) PushImageArchive(_ context.Context, in imageRegistryUseCase.PushArchiveInput) (imageRegistryUseCase.CreateImageResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes = append(f.pushes, in)
	if f.onPush != nil {
		f.onPush(len(f.pushes))
	}
	if f.failPushAt == len(f.pushes) {
		return imageRegistryUseCase.CreateImageResult{}, imageRegistryUseCase.Unavailable(ociregistry.ErrUnauthorized, "push")
	}
	return imageRegistryUseCase.CreateImageResult{Reference: "platform:" + imageRegistryModel.SanitizeName(in.Name) + "@" + in.Archive.Config.Digest}, nil
}

// fakeSources is the media side: chunked uploads by id.
type fakeSources struct {
	persist func(mediaModel.Upload)
	uploads map[uuid.UUID]mediaModel.Upload
	data    map[uuid.UUID][]byte
	// pads are zero bytes read before an upload's data (a large upload without holding it).
	pads    map[uuid.UUID]int64
	dropped []uuid.UUID
	// failFrom..failTo: reads of these offsets fail like a storage outage naming the object key.
	failFrom, failTo int64
}

// failingAt fails reads that touch [from, to) with a storage error naming the object key.
type failingAt struct {
	io.ReaderAt
	from, to int64
}

func (f failingAt) ReadAt(p []byte, off int64) (int, error) {
	if off < f.to && off+int64(len(p)) > f.from {
		return 0, errors.New("read media/uploads/0192f0a0/3: connection reset by s3.internal:9000")
	}
	return f.ReaderAt.ReadAt(p, off)
}

// paddedReader is pad zero bytes followed by b.
type paddedReader struct {
	pad int64
	b   []byte
}

func (p paddedReader) ReadAt(out []byte, off int64) (int, error) {
	n := 0
	for n < len(out) {
		at := off + int64(n)
		switch {
		case at < p.pad:
			out[n] = 0
			n++
		case at-p.pad < int64(len(p.b)):
			n += copy(out[n:], p.b[at-p.pad:])
		default:
			return n, io.EOF
		}
	}
	return n, nil
}

func newFakeSources() *fakeSources {
	return &fakeSources{uploads: map[uuid.UUID]mediaModel.Upload{}, data: map[uuid.UUID][]byte{}, pads: map[uuid.UUID]int64{}}
}

func (s *fakeSources) stage(id, owner uuid.UUID, b []byte, purpose mediaModel.UploadPurpose) {
	s.stagePadded(id, owner, 0, b, purpose)
}

func (s *fakeSources) stagePadded(id, owner uuid.UUID, pad int64, b []byte, purpose mediaModel.UploadPurpose) {
	s.pads[id] = pad
	up := mediaModel.NewUpload(id, owner, "tasks.zip", "application/zip", pad+int64(len(b)), 8, time.Hour, time.Now(), purpose)
	up.ChunksReceived = up.ChunkCount()
	s.uploads[id], s.data[id] = up, b
	if s.persist != nil {
		s.persist(up)
	}
}

func (s *fakeSources) UploadStatus(_ context.Context, id, owner uuid.UUID) (mediaModel.Upload, error) {
	up, ok := s.uploads[id]
	if !ok || up.CreatedBy != owner {
		return mediaModel.Upload{}, mediaModel.ErrUploadNotFound.Err()
	}
	return up, nil
}

func (s *fakeSources) OpenUploadAt(_ context.Context, id uuid.UUID) (io.ReaderAt, mediaModel.Upload, error) {
	up, ok := s.uploads[id]
	if !ok {
		return nil, mediaModel.Upload{}, mediaModel.ErrUploadNotFound.Err()
	}
	var r io.ReaderAt = paddedReader{pad: s.pads[id], b: s.data[id]}
	if s.failTo > 0 {
		r = failingAt{ReaderAt: r, from: s.failFrom, to: s.failTo}
	}
	return r, up, nil
}

func (s *fakeSources) OpenFileAt(context.Context, uuid.UUID) (io.ReaderAt, mediaModel.File, error) {
	return nil, mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
}

func (s *fakeSources) DropUpload(_ context.Context, id uuid.UUID) error {
	s.dropped = append(s.dropped, id)
	delete(s.uploads, id)
	return nil
}

// fakeImportInbox records the notes about the end of an import.
type fakeImportInbox struct {
	done, failed []inboxUseCase.ImportEnd
}

func (f *fakeImportInbox) ExerciseImportDone(_ context.Context, e inboxUseCase.ImportEnd) error {
	f.done = append(f.done, e)
	return nil
}

func (f *fakeImportInbox) ExerciseImportFailed(_ context.Context, e inboxUseCase.ImportEnd) error {
	f.failed = append(f.failed, e)
	return nil
}

// importFixture is the exercise use case with the background import wired to fakes and a job table in memory.
type importFixture struct {
	uc      *exercise.ExerciseUseCase
	q       *postgresMocks.MockQuerier
	images  *fakeImages
	sources *fakeSources
	queued  []jobsModel.ExerciseImportArgs
	jobs    map[uuid.UUID]postgres.ExerciseImportJob
	// progress are the throttled progress writes, in order; onCreate runs inside every task creation.
	progress []postgres.UpdateExerciseImportJobProgressParams
	onCreate func()
	// written are the versions' variants as inserted; created the exercises.
	written [][]byte
	created []uuid.UUID
	deleted []uuid.UUID
	cipher  *secret.Cipher
	now     time.Time
	// failUpdates > 0: that many job writes fail first.
	failUpdates int
	updates     int
	inbox       *fakeImportInbox
}

func newImportFixture(t *testing.T) *importFixture {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	cipher, err := secret.NewFromBytes([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	f := &importFixture{q: q, images: &fakeImages{}, sources: newFakeSources(), jobs: map[uuid.UUID]postgres.ExerciseImportJob{}, cipher: cipher, now: importJobT0}
	f.uc = newTestExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	f.uc.SetClock(func() time.Time { return f.now })
	f.inbox = &fakeImportInbox{}
	f.uc.SetImportInbox(f.inbox)
	f.uc.SetArchiveImport(exercise.ArchiveImportDependencies{
		Images: f.images, Sources: f.sources, Sealer: cipher,
		Queue: func(_ context.Context, args jobsModel.ExerciseImportArgs) error {
			f.queued = append(f.queued, args)
			return nil
		},
	})
	q.EXPECT().StartExerciseImportJob(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.StartExerciseImportJobParams) (postgres.ExerciseImportJob, error) {
		for _, j := range f.jobs {
			if j.SourceID == p.SourceID {
				return j, nil
			}
		}
		j := postgres.ExerciseImportJob{
			ID: p.ID, CreatedBy: p.CreatedBy, Source: p.Source, SourceID: p.SourceID, State: p.State, Generation: 1, Options: p.Options, PlanHash: p.PlanHash,
			ExerciseIds: p.ExerciseIds, Error: p.Error, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Stage: "unpacking",
		}
		f.jobs[j.ID] = j
		return j, nil
	}).AnyTimes()
	q.EXPECT().GetExerciseImportJob(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.ExerciseImportJob, error) {
		j, ok := f.jobs[id]
		if !ok {
			return postgres.ExerciseImportJob{}, pgx.ErrNoRows
		}
		return j, nil
	}).AnyTimes()
	q.EXPECT().UpdateExerciseImportJob(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, p postgres.UpdateExerciseImportJobParams) (int64, error) {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		f.updates++
		if f.failUpdates > 0 {
			f.failUpdates--
			return 0, errors.New("database unavailable")
		}
		j := f.jobs[p.ID]
		if (j.Generation != 0 && j.Generation != p.Generation) || j.State != "running" {
			return 0, nil
		}
		j.State, j.ExerciseIds, j.Error, j.UpdatedAt, j.Results = p.State, p.ExerciseIds, p.Error, p.UpdatedAt, p.Results
		j.Stage, j.ImagesDone, j.ImagesTotal, j.TasksDone, j.TasksTotal = p.Stage, p.ImagesDone, p.ImagesTotal, p.TasksDone, p.TasksTotal
		j.BytesPushed, j.BytesTotal = p.BytesPushed, p.BytesTotal
		f.jobs[p.ID] = j
		return 1, nil
	}).AnyTimes()
	q.EXPECT().UpdateExerciseImportJobProgress(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdateExerciseImportJobProgressParams) (int64, error) {
		j, ok := f.jobs[p.ID]
		if !ok || j.State != "running" || (j.Generation != 0 && j.Generation != p.Generation) {
			return 0, nil
		}
		f.progress = append(f.progress, p)
		j.Stage, j.ImagesDone, j.ImagesTotal, j.TasksDone, j.TasksTotal = p.Stage, p.ImagesDone, p.ImagesTotal, p.TasksDone, p.TasksTotal
		j.BytesPushed, j.BytesTotal, j.UpdatedAt = p.BytesPushed, p.BytesTotal, p.UpdatedAt
		f.jobs[p.ID] = j
		return 1, nil
	}).AnyTimes()
	q.EXPECT().LockExerciseImportUpload(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.MediaUpload, error) {
		up, ok := f.sources.uploads[id]
		if !ok {
			return postgres.MediaUpload{}, pgx.ErrNoRows
		}
		return postgres.MediaUpload{ID: up.ID, CreatedBy: up.CreatedBy, SizeBytes: up.SizeBytes, ChunkBytes: up.ChunkBytes, ChunksReceived: int32(up.ChunksReceived), Purpose: string(up.Purpose), ExpiresAt: up.ExpiresAt}, nil
	}).AnyTimes()
	q.EXPECT().GetOwnedExerciseImportJobBySource(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.GetOwnedExerciseImportJobBySourceParams) (postgres.ExerciseImportJob, error) {
		for _, job := range f.jobs {
			if job.SourceID == p.SourceID && job.Source == p.Source && job.CreatedBy == p.CreatedBy {
				return job, nil
			}
		}
		return postgres.ExerciseImportJob{}, pgx.ErrNoRows
	}).AnyTimes()
	q.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).Return(postgres.User{ID: importer, Role: "super_admin", Status: "active"}, nil).AnyTimes()
	q.EXPECT().LockExerciseImportJob(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.ExerciseImportJob, error) {
		j := f.jobs[id]
		if j.Generation == 0 {
			j.Generation = 1
		}
		return j, nil
	}).AnyTimes()
	q.EXPECT().RestartExerciseImportJob(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.RestartExerciseImportJobParams) (postgres.ExerciseImportJob, error) {
		j := f.jobs[p.ID]
		if j.Generation == 0 {
			j.Generation = 1
		}
		if j.Generation != p.ExpectedGeneration || (j.State != "failed" && !(j.State == "running" && j.UpdatedAt.Before(p.StaleBefore))) {
			return postgres.ExerciseImportJob{}, pgx.ErrNoRows
		}
		j.Generation++
		j.State = "running"
		j.Error = nil
		j.ExerciseIds = nil
		j.Results = []byte("[]")
		j.Options = p.Options
		j.PlanHash = p.PlanHash
		j.OwnerEventID = p.OwnerEventID
		j.UpdatedAt = p.UpdatedAt
		j.Stage = "unpacking"
		f.jobs[j.ID] = j
		return j, nil
	}).AnyTimes()
	return f
}

// expectCreation lets the import create tasks (draft versions) and records them.
func (f *importFixture) expectCreation(t *testing.T) {
	entities := map[uuid.UUID]postgres.Exercise{}
	f.q.EXPECT().TryCreateImportedExercise(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, p postgres.TryCreateImportedExerciseParams) (postgres.Exercise, error) {
		return f.q.CreateExercise(ctx, postgres.CreateExerciseParams(p))
	}).AnyTimes()
	f.q.EXPECT().GetExerciseByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.Exercise, error) { return entities[id], nil }).AnyTimes()
	t.Helper()
	expectUserNames(f.q)
	expectFilesAdoptedAsIs(f.q)
	f.q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			f.created = append(f.created, arg.ID)
			if f.onCreate != nil {
				f.onCreate()
			}
			row := postgres.Exercise{ID: arg.ID, Name: arg.Name, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}
			entities[arg.ID] = row
			return row, nil
		}).AnyTimes()
	f.q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
			f.written = append(f.written, arg.Variants)
			return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, Variants: arg.Variants, CreatedAt: arg.CreatedAt}, nil
		}).AnyTimes()
	f.q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
}

// imageTask is one task document whose devices name image files (device name → file).
func imageTask(name string, devices ...[2]string) []byte {
	var out []map[string]any
	for _, d := range devices {
		device := map[string]any{"name": d[0], "type": "container"}
		if strings.HasSuffix(d[1], ".tar") {
			device["image"] = map[string]string{"file": d[1]}
		} else {
			device["image"] = d[1]
		}
		out = append(out, device)
	}
	b, _ := json.Marshal(map[string]any{
		"schema": "cib-exercise/1", "exercise": map[string]any{"name": name},
		"versions": []any{map[string]any{"status": "draft", "variants": []any{map[string]any{"topology": map[string]any{"devices": out}}}}},
	})
	return b
}

func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	b, err := exercise.BuildArchiveV1(files)
	require.NoError(t, err)
	return b
}

// writtenImages are the images of the devices of every written version, by device name.
func (f *importFixture) writtenImages(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, raw := range f.written {
		var variants []exerciseModel.Variant
		require.NoError(t, json.Unmarshal(raw, &variants))
		for _, v := range variants {
			for _, d := range v.Topology.Devices {
				out[d.Name] = d.Image
			}
		}
	}
	return out
}

// startAndRun starts the import of the staged upload, expects a queued job and runs it.
func (f *importFixture) startAndRun(t *testing.T) exercise.ImportJobView {
	t.Helper()
	start, err := f.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, start.JobID)
	require.Empty(t, start.Exercises)
	require.Len(t, f.queued, 1)
	require.NoError(t, f.uc.RunArchiveImport(context.Background(), f.queued[len(f.queued)-1]))
	view, err := f.uc.ArchiveImportJob(context.Background(), start.JobID, importer)
	require.NoError(t, err)
	return view
}

func webReference() string { return "platform:web@" + archivetest.Digest(webImage.Config()) }

// An archive naming an image file under images/ imports in the background: the image is pushed and the device
// stores the platform reference; the same file named by two devices is pushed once (named after the first).
func TestImportArchiveWithImagePushesAndStoresTheReference(t *testing.T) {
	f := newImportFixture(t)
	f.expectCreation(t)
	f.sources.stage(archiveUpID, importer, zipOf(t, map[string][]byte{
		"exercise.json":  imageTask("Images", [2]string{"web", "images/web.tar"}, [2]string{"web-copy", "images/web.tar"}, [2]string{"proxy", "nginx:1.27"}),
		"images/web.tar": webImageTar,
	}), mediaModel.UploadPurposeExerciseArchive)

	view := f.startAndRun(t)
	require.Equal(t, exerciseModel.ImportJobDone, view.State, "error %+v", view.Error)
	require.Equal(t, f.created, view.ExerciseIDs)
	require.Len(t, f.images.pushes, 1, "one file, one push")
	require.Equal(t, "web", f.images.pushes[0].Name)
	require.Equal(t, importer, f.images.pushes[0].UploadedBy)
	require.Equal(t, map[string]string{"web": webReference(), "web-copy": webReference(), "proxy": "nginx:1.27"}, f.writtenImages(t))
	require.Equal(t, []uuid.UUID{archiveUpID}, f.sources.dropped, "the imported upload is dropped")
}

// A bundle: two task folders, each with its own image, all pushed before any task is created.
func TestImportArchiveBundleWithImages(t *testing.T) {
	f := newImportFixture(t)
	f.expectCreation(t)
	f.sources.stage(archiveUpID, importer, zipOf(t, map[string][]byte{
		"a/exercise.json":  imageTask("Task A", [2]string{"web", "images/web.tar"}),
		"a/images/web.tar": webImageTar,
		"b/exercise.json":  imageTask("Task B", [2]string{"tool", "img/tool.tar"}),
		"b/img/tool.tar":   toolImageTar,
	}), mediaModel.UploadPurposeExerciseArchive)
	view := f.startAndRun(t)
	require.Equal(t, exerciseModel.ImportJobDone, view.State, "error %+v", view.Error)
	require.Len(t, view.ExerciseIDs, 2)
	require.Len(t, f.images.pushes, 2)
	images := f.writtenImages(t)
	require.Equal(t, webReference(), images["web"])
	require.True(t, strings.HasPrefix(images["tool"], "platform:tool@sha256:"))
}

// The registry not configured: the import is refused at once with the upload refusal; nothing is queued or created.
func TestImportArchiveWithImageUnconfiguredRefused(t *testing.T) {
	f := newImportFixture(t)
	f.images.notConfig = true
	f.sources.stage(archiveUpID, importer, zipOf(t, map[string][]byte{
		"exercise.json":  imageTask("Images", [2]string{"web", "images/web.tar"}),
		"images/web.tar": webImageTar,
	}), mediaModel.UploadPurposeExerciseArchive)
	_, err := f.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
	require.ErrorIs(t, err, imageRegistryModel.ErrRegistryNotConfigured.Err())
	require.Empty(t, f.queued)
	require.Empty(t, f.jobs)
}

// A push failing at the second image fails the whole import: no task is created (the strict mock refuses any
// CreateExercise), the job records the registry refusal with the file it was about.
func TestImportArchivePushFailureCreatesNothing(t *testing.T) {
	f := newImportFixture(t)
	f.images.failPushAt = 2
	f.sources.stage(archiveUpID, importer, zipOf(t, map[string][]byte{
		"exercise.json":   imageTask("Images", [2]string{"web", "images/web.tar"}, [2]string{"tool", "images/tool.tar"}),
		"images/web.tar":  webImageTar,
		"images/tool.tar": toolImageTar,
	}), mediaModel.UploadPurposeExerciseArchive)
	view := f.startAndRun(t)
	require.Equal(t, exerciseModel.ImportJobFailed, view.State)
	require.Empty(t, view.ExerciseIDs)
	want := imageRegistryModel.ErrRegistryUnavailable.Err().StatusCode()
	require.Equal(t, want.FullCode(), view.Error.Code)
	require.Equal(t, want.Message(), view.Error.Message)
	require.Equal(t, "unauthorized", view.Error.Context["reason"])
	require.Equal(t, "images/tool.tar", view.Error.Context["file"])
	require.NotContains(t, view.Error.Message, "push-token")
	require.Empty(t, f.sources.dropped, "a failed import keeps its upload for another try")
}

func failedImport(t *testing.T, files map[string][]byte, raw ...[]byte) *exerciseModel.ImportJobError {
	t.Helper()
	f := newImportFixture(t)
	b := []byte(nil)
	if len(raw) > 0 {
		b = raw[0]
	} else {
		b = zipOf(t, files)
	}
	f.sources.stage(archiveUpID, importer, b, mediaModel.UploadPurposeExerciseArchive)
	view := f.startAndRun(t)
	require.Equal(t, exerciseModel.ImportJobFailed, view.State)
	require.Empty(t, f.images.pushes, "nothing is pushed")
	return view.Error
}

// gzip, arm64 and an archive that is no image are clear refusals naming the file; nothing is pushed or created.
func TestImportArchiveImageRefusals(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(webImageTar)
	require.NoError(t, zw.Close())
	arm := archivetest.Legacy(archivetest.Image{Layers: [][]byte{[]byte("arm")}, OS: "linux", Arch: "arm64"})
	for name, tc := range map[string]struct {
		body   []byte
		reason string
	}{"gzip": {gz.Bytes(), "gzip"}, "arm64": {arm, "no_amd64"}, "text": {[]byte(strings.Repeat("text ", 200)), "not_tar"}} {
		t.Run(name, func(t *testing.T) {
			failure := failedImport(t, map[string][]byte{
				"exercise.json":  imageTask("Images", [2]string{"web", "images/web.tar"}),
				"images/web.tar": tc.body,
			})
			require.Equal(t, imageRegistryModel.ErrImageArchiveUnreadable.Err().StatusCode().FullCode(), failure.Code)
			require.Equal(t, tc.reason, failure.Context["reason"])
			require.Equal(t, "images/web.tar", failure.Context["file"])
		})
	}
}

// An image file no device names is the existing orphan error (it is an ordinary unlinked file).
func TestImportArchiveOrphanImageFile(t *testing.T) {
	failure := failedImport(t, map[string][]byte{
		"exercise.json":    imageTask("Images", [2]string{"web", "images/web.tar"}),
		"images/web.tar":   webImageTar,
		"images/spare.tar": toolImageTar,
	})
	require.Equal(t, exerciseModel.ErrArchiveFileUnlinked.Err().StatusCode().FullCode(), failure.Code)
	require.Equal(t, "images/spare.tar", failure.Context["file"])
}

// Image paths follow the attachment path rules; an image file is no attachment.
func TestImportArchiveImagePathRules(t *testing.T) {
	failure := failedImport(t, map[string][]byte{
		"a/exercise.json": imageTask("Images", [2]string{"web", "../b/web.tar"}),
		"b/exercise.json": imageTask("Task B"),
		"b/web.tar":       webImageTar,
	})
	require.Equal(t, exerciseModel.ErrArchivePathInvalid.Err().StatusCode().FullCode(), failure.Code)
	require.Equal(t, "leaves the task folder", failure.Context["reason"])

	failure = failedImport(t, map[string][]byte{
		"exercise.json":  imageTask("Images", [2]string{"web", "images/missing.tar"}, [2]string{"db", "images/web.tar"}),
		"images/web.tar": webImageTar,
	})
	require.Equal(t, exerciseModel.ErrArchiveFileMissing.Err().StatusCode().FullCode(), failure.Code)
}

// manifest.json checksums cover image files too: a changed image file fails before anything is pushed.
func TestImportArchiveImageChecksum(t *testing.T) {
	files := map[string][]byte{
		"exercise.json":  imageTask("Images", [2]string{"web", "images/web.tar"}),
		"images/web.tar": webImageTar,
	}
	// The manifest lists the original image file; the archive carries another one.
	manifest, err := json.Marshal(map[string]any{"version": 1, "checksums": map[string]string{
		"exercise.json":  archivetest.Digest(files["exercise.json"])[len("sha256:"):],
		"images/web.tar": archivetest.Digest(webImageTar)[len("sha256:"):],
	}})
	require.NoError(t, err)
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, e := range []struct {
		name string
		body []byte
	}{{"exercise.json", files["exercise.json"]}, {"images/web.tar", toolImageTar}, {"manifest.json", manifest}} {
		w, createErr := zw.Create(e.name)
		require.NoError(t, createErr)
		_, err = w.Write(e.body)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	b := out.Bytes()

	f := newImportFixture(t)
	f.sources.stage(archiveUpID, importer, b, mediaModel.UploadPurposeExerciseArchive)
	view := f.startAndRun(t)
	require.Equal(t, exerciseModel.ImportJobFailed, view.State)
	require.Equal(t, exerciseModel.ErrArchiveChecksumMismatch.Err().StatusCode().FullCode(), view.Error.Code)
	require.Equal(t, "images/web.tar", view.Error.Context["file"])
	require.Empty(t, f.images.pushes)
}

// An archive without image files imports at once from the upload (no job); the upload is dropped.
func TestImportUploadedArchiveWithoutImagesIsSynchronous(t *testing.T) {
	f := newImportFixture(t)
	f.expectCreation(t)
	f.sources.stage(archiveUpID, importer, zipOf(t, map[string][]byte{"exercise.json": imageTask("Plain", [2]string{"web", "nginx:1.27"})}), mediaModel.UploadPurposeExerciseArchive)
	start, err := f.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, start.JobID)
	require.Len(t, start.Exercises, 1)
	require.Empty(t, f.queued)
	require.Equal(t, []uuid.UUID{archiveUpID}, f.sources.dropped)
}

// Without images the archive limits stay: an upload over the archive cap is refused.
func TestImportUploadedArchiveWithoutImagesKeepsTheLimit(t *testing.T) {
	f := newImportFixture(t)
	b := zipOf(t, map[string][]byte{"exercise.json": imageTask("Plain")})
	f.sources.stagePadded(archiveUpID, importer, exercise.DefaultLimits().ArchiveBytes+1-int64(len(b)), b, mediaModel.UploadPurposeExerciseArchive)
	_, err := f.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
	require.ErrorIs(t, err, exerciseModel.ErrArchiveTooLarge.Err())
}

// Only the caller's complete task-archive upload is imported.
func TestImportUploadedArchiveChecksTheUpload(t *testing.T) {
	f := newImportFixture(t)
	b := zipOf(t, map[string][]byte{"exercise.json": imageTask("Plain")})
	f.sources.stage(archiveUpID, importer, b, mediaModel.UploadPurposeExerciseArchive)
	_, err := f.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: otherUser})
	require.ErrorIs(t, err, mediaModel.ErrUploadNotFound.Err())

	f.sources.stage(archiveUpID, importer, b, mediaModel.UploadPurposeImageBlob)
	_, err = f.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
	require.ErrorIs(t, err, exerciseModel.ErrArchiveUploadNotReady.Err())

	f.sources.stage(archiveUpID, importer, b, mediaModel.UploadPurposeExerciseArchive)
	up := f.sources.uploads[archiveUpID]
	up.ChunksReceived--
	f.sources.uploads[archiveUpID] = up
	_, err = f.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
	require.ErrorIs(t, err, exerciseModel.ErrArchiveUploadNotReady.Err())
}

// Job states: running until the job ran; one import per source (asking again returns the same job); a failed
// import runs again when asked; another user never sees a job; a silent running job reads failed (interrupted).
func TestArchiveImportJobStates(t *testing.T) {
	f := newImportFixture(t)
	f.sources.stage(archiveUpID, importer, zipOf(t, map[string][]byte{
		"exercise.json":  imageTask("Images", [2]string{"web", "images/web.tar"}),
		"images/web.tar": []byte(strings.Repeat("text ", 200)),
	}), mediaModel.UploadPurposeExerciseArchive)
	ctx := context.Background()
	start, err := f.uc.ImportUploadedArchive(ctx, exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
	require.NoError(t, err)
	view, err := f.uc.ArchiveImportJob(ctx, start.JobID, importer)
	require.NoError(t, err)
	require.Equal(t, exerciseModel.ImportJobRunning, view.State)
	require.Nil(t, view.Error)

	again, err := f.uc.ImportUploadedArchive(ctx, exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
	require.NoError(t, err)
	require.Equal(t, start.JobID, again.JobID)
	require.Len(t, f.queued, 1, "a running import is not queued twice")

	_, err = f.uc.ArchiveImportJob(ctx, start.JobID, otherUser)
	require.ErrorIs(t, err, exerciseModel.ErrImportJobNotFound.Err())
	_, err = f.uc.ArchiveImportJob(ctx, uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000ff"), importer)
	require.ErrorIs(t, err, exerciseModel.ErrImportJobNotFound.Err())

	require.NoError(t, f.uc.RunArchiveImport(ctx, f.queued[0]))
	view, err = f.uc.ArchiveImportJob(ctx, start.JobID, importer)
	require.NoError(t, err)
	require.Equal(t, exerciseModel.ImportJobFailed, view.State)

	retry, err := f.uc.ImportUploadedArchive(ctx, exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
	require.NoError(t, err)
	require.Equal(t, start.JobID, retry.JobID)
	require.Len(t, f.queued, 2, "a failed import runs again")
	view, _ = f.uc.ArchiveImportJob(ctx, start.JobID, importer)
	require.Equal(t, exerciseModel.ImportJobRunning, view.State)

	f.now = f.now.Add(exerciseModel.ImportJobStaleAfter + time.Minute)
	view, err = f.uc.ArchiveImportJob(ctx, start.JobID, importer)
	require.NoError(t, err)
	require.Equal(t, exerciseModel.ImportJobFailed, view.State)
	require.Equal(t, exerciseModel.ErrArchiveImportInterrupted.Err().StatusCode().FullCode(), view.Error.Code)
}

// The archive password reaches the job only sealed (bound to the job), and the job opens it: a protected archive
// with images imports.
func TestArchiveImportPasswordIsSealed(t *testing.T) {
	f := newImportFixture(t)
	f.expectCreation(t)
	const password = "fixed archive password"
	b, err := exercise.BuildProtectedArchiveV1(map[string][]byte{
		"exercise.json":  imageTask("Images", [2]string{"web", "images/web.tar"}),
		"images/web.tar": webImageTar,
	}, password)
	require.NoError(t, err)
	f.sources.stage(archiveUpID, importer, b, mediaModel.UploadPurposeExerciseArchive)
	start, err := f.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, Password: password, CreatedBy: importer})
	require.NoError(t, err)
	require.Len(t, f.queued, 1)
	args := f.queued[0]
	require.NotEmpty(t, args.SealedPassword)
	require.NotContains(t, args.SealedPassword, password)
	raw, _ := json.Marshal(args)
	require.NotContains(t, string(raw), password)
	_, err = f.cipher.DecryptWithContext(args.SealedPassword, []byte("exercise-import:password:"+uuid.Must(uuid.NewV7()).String()))
	require.Error(t, err, "the sealed password opens only for its job")

	require.NoError(t, f.uc.RunArchiveImport(context.Background(), args))
	view, err := f.uc.ArchiveImportJob(context.Background(), start.JobID, importer)
	require.NoError(t, err)
	require.Equal(t, exerciseModel.ImportJobDone, view.State, "error %+v", view.Error)
	require.Equal(t, webReference(), f.writtenImages(t)["web"])
}

// A job cut off by its context still ends recorded as failed.
func TestRunArchiveImportCancelledRecordsFailed(t *testing.T) {
	f := newImportFixture(t)
	f.sources.stage(archiveUpID, importer, zipOf(t, map[string][]byte{
		"exercise.json":  imageTask("Images", [2]string{"web", "images/web.tar"}),
		"images/web.tar": webImageTar,
	}), mediaModel.UploadPurposeExerciseArchive)
	start, err := f.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
	require.NoError(t, err)
	f.images.failPushAt = 1
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, f.uc.RunArchiveImport(ctx, f.queued[0]))
	view, err := f.uc.ArchiveImportJob(context.Background(), start.JobID, importer)
	require.NoError(t, err)
	require.Equal(t, exerciseModel.ImportJobFailed, view.State)
	require.Equal(t, exerciseModel.ErrArchiveImportStopped.Err().StatusCode().FullCode(), view.Error.Code, "a cut-off import is told as stopped")
}

// dataOffset is where an entry's data starts in a zip.
func dataOffset(t *testing.T, b []byte, name string) (int64, int64) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	require.NoError(t, err)
	for _, f := range zr.File {
		if f.Name == name {
			off, offErr := f.DataOffset()
			require.NoError(t, offErr)
			return off, off + int64(f.CompressedSize64)
		}
	}
	t.Fatalf("no %s", name)
	return 0, 0
}

// A storage failure while the job reads the archive is a platform failure: the user sees the generic internal
// message, never the object key or the storage host.
func TestRunArchiveImportStorageFailureIsGeneric(t *testing.T) {
	for _, entry := range []string{"exercise.json", "images/web.tar"} {
		t.Run(entry, func(t *testing.T) {
			f := newImportFixture(t)
			b := zipOf(t, map[string][]byte{
				"exercise.json":  imageTask("Images", [2]string{"web", "images/web.tar"}),
				"images/web.tar": webImageTar,
			})
			f.sources.stage(archiveUpID, importer, b, mediaModel.UploadPurposeExerciseArchive)
			start, err := f.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
			require.NoError(t, err)
			f.sources.failFrom, f.sources.failTo = dataOffset(t, b, entry)
			require.NoError(t, f.uc.RunArchiveImport(context.Background(), f.queued[0]))
			view, err := f.uc.ArchiveImportJob(context.Background(), start.JobID, importer)
			require.NoError(t, err)
			require.Equal(t, exerciseModel.ImportJobFailed, view.State)
			require.Equal(t, model.ErrPlatform.Err().StatusCode().FullCode(), view.Error.Code)
			require.Equal(t, "Internal server error", view.Error.Message)
			raw, _ := json.Marshal(view.Error)
			require.NotContains(t, string(raw), "media/uploads")
			require.NotContains(t, string(raw), "s3.internal")
		})
	}
}

// An image entry whose bytes run past its declared size is the author's broken archive (22829, corrupt), not an
// internal failure.
func TestRunArchiveImportImageEntryOverItsSize(t *testing.T) {
	doc := imageTask("Images", [2]string{"web", "images/web.tar"})
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	w, err := zw.Create("exercise.json")
	require.NoError(t, err)
	_, _ = w.Write(doc)
	raw, err := zw.CreateRaw(&zip.FileHeader{
		Name: "images/web.tar", Method: zip.Store, CRC32: crc32.ChecksumIEEE(webImageTar),
		CompressedSize64: uint64(len(webImageTar)), UncompressedSize64: uint64(len(webImageTar) - 600),
	})
	require.NoError(t, err)
	_, _ = raw.Write(webImageTar)
	require.NoError(t, zw.Close())
	failure := failedImport(t, nil, out.Bytes())
	require.Equal(t, imageRegistryModel.ErrImageArchiveUnreadable.Err().StatusCode().FullCode(), failure.Code)
	require.Equal(t, "corrupt", failure.Context["reason"])
	require.Equal(t, "images/web.tar", failure.Context["file"])
}

// Task changes and done are one database outcome; lost commit responses never replay it.
func TestRunArchiveImportDoneWriteIsAtomicWithTheImport(t *testing.T) { testAtomicImportOutcome(t) }

// The synchronous import (an archive in memory) still refuses an image file: such archives go to the job.
func TestArchiveNamesImages(t *testing.T) {
	with := zipOf(t, map[string][]byte{"exercise.json": imageTask("I", [2]string{"web", "images/web.tar"}), "images/web.tar": webImageTar})
	without := zipOf(t, map[string][]byte{"exercise.json": imageTask("P", [2]string{"web", "nginx"})})
	require.True(t, exercise.ArchiveNamesImages(with, "", exercise.DefaultLimits()))
	require.False(t, exercise.ArchiveNamesImages(without, "", exercise.DefaultLimits()))
	require.False(t, exercise.ArchiveNamesImages([]byte("not a zip"), "", exercise.DefaultLimits()))
	_, err := exercise.ParseExerciseArchive(with, "", exercise.DefaultLimits())
	require.ErrorIs(t, err, exerciseModel.ErrArchiveImageFileUnsupported.Err())
}

// The wait between outcome writes ends with the job's context.
func TestRunArchiveImportOutcomeRetryStopsWithTheContext(t *testing.T) {
	exercise.SetImportStateRetryDelay(t, time.Hour)
	f := newImportFixture(t)
	f.sources.stage(archiveUpID, importer, zipOf(t, map[string][]byte{
		"exercise.json":  imageTask("Images", [2]string{"web", "images/web.tar"}),
		"images/web.tar": []byte(strings.Repeat("text ", 200)),
	}), mediaModel.UploadPurposeExerciseArchive)
	_, err := f.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
	require.NoError(t, err)
	f.failUpdates = 100
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	began := time.Now()
	require.Error(t, f.uc.RunArchiveImport(ctx, f.queued[0]), "an unrecorded failure goes back to River")
	require.Less(t, time.Since(began), 10*time.Second, "no hour-long sleep after the context ended")
}

// bundleOfTwoImageTasks stages a bundle of two tasks, each with its own image file.
func bundleOfTwoImageTasks(t *testing.T, f *importFixture) {
	t.Helper()
	f.sources.stage(archiveUpID, importer, zipOf(t, map[string][]byte{
		"a/exercise.json":  imageTask("Task A", [2]string{"web", "images/web.tar"}),
		"a/images/web.tar": webImageTar,
		"b/exercise.json":  imageTask("Task B", [2]string{"tool", "img/tool.tar"}),
		"b/img/tool.tar":   toolImageTar,
	}), mediaModel.UploadPurposeExerciseArchive)
}

type progressStep struct {
	stage                                          string
	imagesDone, imagesTotal, tasksDone, tasksTotal int32
}

func stepsOf(writes []postgres.UpdateExerciseImportJobProgressParams) []progressStep {
	out := make([]progressStep, len(writes))
	for i, w := range writes {
		out[i] = progressStep{w.Stage, w.ImagesDone, w.ImagesTotal, w.TasksDone, w.TasksTotal}
	}
	return out
}

// The job reports its stage and counts as it goes: the totals once known, an image or a task counted when it is
// done, and the status read mid-run shows them. The clock moves 3 s per step, past the write interval.
func TestImportJobReportsStageAndCounts(t *testing.T) {
	f := newImportFixture(t)
	f.expectCreation(t)
	bundleOfTwoImageTasks(t, f)
	var midRun exercise.ImportJobView
	var jobID uuid.UUID
	f.images.onPush = func(n int) {
		f.now = f.now.Add(3 * time.Second)
		if n == 2 {
			v, err := f.uc.ArchiveImportJob(context.Background(), jobID, importer)
			require.NoError(t, err)
			midRun = v
		}
	}
	f.onCreate = func() { f.now = f.now.Add(3 * time.Second) }
	start, err := f.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
	require.NoError(t, err)
	jobID = start.JobID
	require.NoError(t, f.uc.RunArchiveImport(context.Background(), f.queued[0]))

	require.Equal(t, []progressStep{
		{"unpacking", 0, 0, 0, 2},
		{"images", 0, 2, 0, 2},
		{"images", 1, 2, 0, 2},
		{"images", 2, 2, 0, 2},
		{"tasks", 2, 2, 0, 2},
	}, stepsOf(f.progress))
	last := f.progress[len(f.progress)-1]
	require.Equal(t, last.BytesTotal, last.BytesPushed)
	require.Positive(t, last.BytesTotal)

	require.Equal(t, exerciseModel.ImportStageImages, midRun.Stage, "read while the second image is pushed")
	require.Equal(t, 1, midRun.Progress.ImagesDone)
	require.Equal(t, 2, midRun.Progress.ImagesTotal)
	require.Equal(t, 2, midRun.Progress.TasksTotal)

	done, err := f.uc.ArchiveImportJob(context.Background(), jobID, importer)
	require.NoError(t, err)
	require.Equal(t, exerciseModel.ImportStageDone, done.Stage)
	require.Equal(t, 2, done.Progress.TasksDone)
	require.Equal(t, 2, done.Progress.ImagesDone)
}

// Counts are throttled: with the clock standing still only the stage changes are written, never every count.
func TestImportJobProgressIsThrottled(t *testing.T) {
	f := newImportFixture(t)
	f.expectCreation(t)
	bundleOfTwoImageTasks(t, f)
	view := f.startAndRun(t)
	require.Equal(t, exerciseModel.ImportJobDone, view.State, "error %+v", view.Error)
	require.Equal(t, []progressStep{
		{"unpacking", 0, 0, 0, 2},
		{"images", 0, 2, 0, 2},
		{"tasks", 2, 2, 0, 2}, // the stage change writes the counts held back
	}, stepsOf(f.progress))
	require.Equal(t, exerciseModel.ImportStageDone, view.Stage)
	require.Equal(t, 2, view.Progress.TasksDone, "the finished job carries the final counts")
}

// A failed import reads as failed and keeps how far it came; a progress write never revives an ended job.
func TestImportJobFailureKeepsProgress(t *testing.T) {
	f := newImportFixture(t)
	f.images.failPushAt = 2
	f.images.onPush = func(int) { f.now = f.now.Add(3 * time.Second) }
	bundleOfTwoImageTasks(t, f)
	view := f.startAndRun(t)
	require.Equal(t, exerciseModel.ImportJobFailed, view.State)
	require.Equal(t, exerciseModel.ImportStageFailed, view.Stage)
	require.Equal(t, 1, view.Progress.ImagesDone)
	require.Equal(t, 2, view.Progress.ImagesTotal)
	require.Zero(t, view.Progress.TasksDone)
}

// The end of an import goes to the inbox of the user who started it: done with the count, failed once.
func TestImportEndIsAnnouncedToTheStarter(t *testing.T) {
	f := newImportFixture(t)
	f.expectCreation(t)
	bundleOfTwoImageTasks(t, f)
	view := f.startAndRun(t)
	require.Equal(t, exerciseModel.ImportJobDone, view.State, "error %+v", view.Error)
	require.Equal(t, []inboxUseCase.ImportEnd{{User: importer, Created: 2, At: importJobT0}}, f.inbox.done)
	require.Empty(t, f.inbox.failed)

	g := newImportFixture(t)
	g.images.failPushAt = 1
	bundleOfTwoImageTasks(t, g)
	require.Equal(t, exerciseModel.ImportJobFailed, g.startAndRun(t).State)
	require.Equal(t, []inboxUseCase.ImportEnd{{User: importer, At: importJobT0}}, g.inbox.failed)
	require.Empty(t, g.inbox.done)
}

// The caller's running imports are listed from the job table with their progress.
func TestRunningArchiveImportsListsTheCallersRunningJobs(t *testing.T) {
	f := newImportFixture(t)
	jobID := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000c9")
	f.q.EXPECT().ListRunningExerciseImportJobs(gomock.Any(), postgres.ListRunningExerciseImportJobsParams{
		CreatedBy: importer, StaleBefore: importJobT0.Add(-exerciseModel.ImportJobStaleAfter),
	}).Return([]postgres.ExerciseImportJob{{
		ID: jobID, CreatedBy: importer, State: "running", UpdatedAt: importJobT0, Stage: "images", ImagesDone: 1, ImagesTotal: 3, TasksTotal: 4,
	}}, nil)
	got, err := f.uc.RunningArchiveImports(context.Background(), importer)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, jobID, got[0].ID)
	require.Equal(t, exerciseModel.ImportStageImages, got[0].Stage)
	require.Equal(t, 3, got[0].Progress.ImagesTotal)
	require.Equal(t, 4, got[0].Progress.TasksTotal)
}

func (f *fakeImages) InspectImageArchive(ctx context.Context, a *ociregistry.ImageArchive, name string) (imageRegistryUseCase.ArchiveImageInspection, error) {
	return (&previewImages{fakeImages: f}).InspectImageArchive(ctx, a, name)
}

func TestSourceCommitRetainsThePlanOfAnExistingLegacyNewJob(t *testing.T) {
	f := newImportFixture(t)
	f.sources.stage(archiveUpID, importer, zipOf(t, map[string][]byte{"exercise.json": imageTask("Images", [2]string{"web", "images/web.tar"}), "images/web.tar": webImageTar}), mediaModel.UploadPurposeExerciseArchive)
	legacy, err := f.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, CreatedBy: importer})
	require.NoError(t, err)
	current, err := f.uc.CommitArchiveImport(context.Background(), exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}, exercise.CommitArchiveInput{PreviewArchiveInput: exercise.PreviewArchiveInput{UploadID: archiveUpID}})
	require.NoError(t, err)
	require.Equal(t, legacy.JobID, current.JobID)
	require.Len(t, f.queued, 1)
}
