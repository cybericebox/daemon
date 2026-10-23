package exercise_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	yzip "github.com/yeka/zip"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/ociregistry/archivetest"
	"github.com/cybericebox/daemon/pkg/secret"
)

var (
	exportTaskA = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000c1")
	exportTaskB = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000c2")
)

// A device image sits with its variant; devices sharing one image put it in shared/ of the lowest level covering them.
func TestAssignExportPaths_DeviceImages(t *testing.T) {
	paths := exercise.AssignExportPaths("images", []exercise.FileUse{
		{Hash: "one", Name: "web.tar", Version: 1, Variant: 2, Device: 1},
		{Hash: "two", Name: "db.tar", Version: 1, Variant: 2, Device: 2},
		{Hash: "both", Name: "proxy.tar", Version: 1, Variant: 1, Device: 3},
		{Hash: "both", Name: "proxy-b.tar", Version: 1, Variant: 1, Device: 4},
		{Hash: "everywhere", Name: "lab.tar", Version: 1, Variant: 1, Device: 5},
		{Hash: "everywhere", Name: "lab.tar", Version: 2, Variant: 1, Device: 1},
		{Hash: "versions", Name: "base.tar", Version: 1, Variant: 1, Device: 6},
		{Hash: "versions", Name: "base.tar", Version: 1, Variant: 2, Device: 3},
	})
	require.Equal(t, map[string]string{
		"one":        "images/v1/variant-2/web.tar",
		"two":        "images/v1/variant-2/db.tar",
		"both":       "images/v1/variant-1/shared/proxy.tar",
		"everywhere": "images/shared/lab.tar",
		"versions":   "images/v1/shared/base.tar",
	}, paths)
}

// exportImageFake is a platform image held in memory: it writes the real image archive of the registry package.
type exportImageFake struct {
	layout ociregistry.ImageLayout
	blobs  map[string][]byte
	// gone: blobs the registry has lost (opening them fails after the headers are written).
	gone  map[string]bool
	calls int
}

func newExportImageFake(t *testing.T, name string, img archivetest.Image) *exportImageFake {
	t.Helper()
	manifest := img.Manifest()
	layout, err := ociregistry.ParseImageLayout("", manifest, name+":"+strings.TrimPrefix(archivetest.Digest(manifest), "sha256:")[:12])
	require.NoError(t, err)
	blobs := map[string][]byte{archivetest.Digest(img.Config()): img.Config()}
	for _, l := range img.Layers {
		blobs[archivetest.Digest(l)] = l
	}
	return &exportImageFake{layout: layout, blobs: blobs, gone: map[string]bool{}}
}

func (f *exportImageFake) reference(name string) string {
	return "platform:" + name + "@" + f.layout.ManifestDigest
}
func (f *exportImageFake) Digest() string { return f.layout.ManifestDigest }
func (f *exportImageFake) Size() int64    { return f.layout.Size() }
func (f *exportImageFake) WriteTo(_ context.Context, w io.Writer) error {
	f.calls++
	err := f.layout.WriteTo(w, func(digest string) (io.ReadCloser, error) {
		if f.gone[digest] {
			return nil, imageRegistryModel.ErrImageNotExportable.WithPublicContext(imageRegistryModel.ContextReason, "not_found").Err()
		}
		return io.NopCloser(bytes.NewReader(f.blobs[digest])), nil
	})
	return err
}

type fakeExporter struct {
	images map[string]*exportImageFake // reference → image
	// failFor: references the registry refuses.
	failFor map[string]string
	asked   []string
	// override, when set, is answered for every reference.
	override exercise.ExportedImage
}

func (f *fakeExporter) ExportedImage(_ context.Context, reference string) (exercise.ExportedImage, error) {
	f.asked = append(f.asked, reference)
	if f.override != nil {
		return f.override, nil
	}
	if reason, refused := f.failFor[reference]; refused {
		return nil, imageRegistryModel.ErrImageNotExportable.WithPublicContext(imageRegistryModel.ContextReason, reason).Err()
	}
	image, ok := f.images[reference]
	if !ok {
		return nil, imageRegistryModel.ErrImageNotExportable.WithPublicContext(imageRegistryModel.ContextReason, "not_found").Err()
	}
	return image, nil
}

// exportFiles stores the finished zip in memory.
type exportFiles struct {
	files map[uuid.UUID][]byte
	meta  map[uuid.UUID]mediaModel.File
	// maxBytes is the cap the last upload was given.
	maxBytes int64
}

func (f *exportFiles) UploadLargeFile(_ context.Context, name, contentType string, r io.Reader, createdBy uuid.UUID, maxBytes int64) (mediaModel.File, error) {
	f.maxBytes = maxBytes
	b, err := io.ReadAll(r)
	if err != nil {
		return mediaModel.File{}, fmt.Errorf("store: %w", err)
	}
	id := uuid.Must(uuid.NewV7())
	file := mediaModel.File{ID: id, Name: name, ContentType: contentType, SizeBytes: int64(len(b)), CreatedBy: uuid.NullUUID{UUID: createdBy, Valid: true}}
	f.files[id], f.meta[id] = b, file
	return file, nil
}

func (f *exportFiles) GetFile(_ context.Context, id uuid.UUID) (mediaModel.File, error) {
	file, ok := f.meta[id]
	if !ok {
		return mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	return file, nil
}

type imageExportFixture struct {
	uc       *exercise.ExerciseUseCase
	q        *postgresMocks.MockQuerier
	exporter *fakeExporter
	files    *exportFiles
	queued   []jobsModel.ExerciseExportArgs
	jobs     map[uuid.UUID]postgres.ExerciseExportJob
	cipher   *secret.Cipher
	now      time.Time
	web      *exportImageFake
	tool     *exportImageFake
}

func newExportFixture(t *testing.T) *imageExportFixture {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	cipher, err := secret.NewFromBytes([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	f := &imageExportFixture{
		q: q, exporter: &fakeExporter{images: map[string]*exportImageFake{}, failFor: map[string]string{}},
		files: &exportFiles{files: map[uuid.UUID][]byte{}, meta: map[uuid.UUID]mediaModel.File{}},
		jobs:  map[uuid.UUID]postgres.ExerciseExportJob{}, cipher: cipher, now: importJobT0,
		web:  newExportImageFake(t, "web", webImage),
		tool: newExportImageFake(t, "tool", archivetest.Amd64([]byte("fixed tool layer"), []byte("a second tool layer"))),
	}
	f.exporter.images[f.web.reference("web")] = f.web
	f.exporter.images[f.tool.reference("tool")] = f.tool
	f.uc = exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	f.uc.SetClock(func() time.Time { return f.now })
	f.uc.SetArchiveExport(exercise.ArchiveExportDependencies{
		Images: f.exporter, Files: f.files, Sealer: cipher,
		Queue: func(_ context.Context, args jobsModel.ExerciseExportArgs) error {
			f.queued = append(f.queued, args)
			return nil
		},
	})
	q.EXPECT().CreateExerciseExportJob(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseExportJobParams) error {
		f.jobs[p.ID] = postgres.ExerciseExportJob(p)
		return nil
	}).AnyTimes()
	q.EXPECT().GetExerciseExportJob(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.ExerciseExportJob, error) {
		j, ok := f.jobs[id]
		if !ok {
			return postgres.ExerciseExportJob{}, pgx.ErrNoRows
		}
		return j, nil
	}).AnyTimes()
	q.EXPECT().UpdateExerciseExportJob(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdateExerciseExportJobParams) (int64, error) {
		j := f.jobs[p.ID]
		j.State, j.FileID, j.Error, j.UpdatedAt = p.State, p.FileID, p.Error, p.UpdatedAt
		f.jobs[p.ID] = j
		return 1, nil
	}).AnyTimes()
	return f
}

type deviceSpec struct{ name, image string }

// task stages a task of one version whose first variant has the devices.
func (f *imageExportFixture) task(id uuid.UUID, name string, devices ...deviceSpec) {
	f.variants(id, name, [][]deviceSpec{devices})
}

// variants stages a task of one version with a variant per device list.
func (f *imageExportFixture) variants(id uuid.UUID, name string, variants [][]deviceSpec) {
	var all []exerciseModel.Variant
	for vi, devices := range variants {
		variant := exerciseModel.Variant{ID: uuid.Must(uuid.NewV7()), Index: int32(vi + 1)}
		for _, d := range devices {
			variant.Topology.Devices = append(variant.Topology.Devices, exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: d.name, Type: "container", Image: d.image})
		}
		all = append(all, variant)
	}
	raw, err := json.Marshal(all)
	if err != nil {
		panic(err)
	}
	f.q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(postgres.Exercise{ID: id, Name: name}, nil).AnyTimes()
	f.q.EXPECT().ListExerciseVersions(gomock.Any(), id).Return([]postgres.ExerciseVersion{
		{ID: uuid.Must(uuid.NewV7()), ExerciseID: id, Status: "draft", Variants: raw, CreatedAt: importJobT0},
	}, nil).AnyTimes()
}

// run starts the export and runs its job; it returns the job as its user reads it.
func (f *imageExportFixture) run(t *testing.T, in exercise.StartExportInput) exercise.ExportJobView {
	t.Helper()
	in.CreatedBy = importer
	if in.FileName == "" {
		in.FileName = "tasks.cybericebox.zip"
	}
	id, err := f.uc.StartArchiveExport(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, f.queued, 1)
	require.NoError(t, f.uc.RunArchiveExport(context.Background(), f.queued[len(f.queued)-1]))
	view, err := f.uc.ArchiveExportJob(context.Background(), id, importer)
	require.NoError(t, err)
	return view
}

func readZip(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	require.NoError(t, err)
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err)
		body, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		out[f.Name] = body
	}
	return out
}

func (f *imageExportFixture) onlyZip(t *testing.T, view exercise.ExportJobView) []byte {
	t.Helper()
	require.Equal(t, exerciseModel.ImportJobDone, view.State, "error %+v", view.Error)
	require.NotNil(t, view.File)
	return f.files.files[view.File.ID]
}

// An export «з образами»: the platform image of two devices is written once under images/, the document names the file
// for both, a public reference stays a reference, manifest.json covers every entry (the image included), and the
// archive imports again with the same image content.
func TestExportWithImagesRoundTrip(t *testing.T) {
	f := newExportFixture(t)
	f.task(exportTaskA, "Web lab",
		deviceSpec{"web", f.web.reference("web")}, deviceSpec{"web-copy", f.web.reference("web")}, deviceSpec{"proxy", "nginx:1.27"})

	view := f.run(t, exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA}})
	archive := f.onlyZip(t, view)
	require.Equal(t, "tasks.cybericebox.zip", view.File.Name)
	require.Equal(t, exerciseModel.DefaultArchiveLimits().WithImagesBytes(), f.files.maxBytes, "the stored zip may carry 10 GiB of images")
	require.Equal(t, 1, f.web.calls, "an image used by two devices is read once")

	entries := readZip(t, archive)
	require.Contains(t, entries, "images/v1/variant-1/shared/web.tar")
	require.Contains(t, entries, "exercise.json")
	var doc struct {
		Versions []struct {
			Variants []struct {
				Topology struct {
					Devices []struct {
						Name  string
						Image json.RawMessage
					}
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal(entries["exercise.json"], &doc))
	images := map[string]string{}
	for _, d := range doc.Versions[0].Variants[0].Topology.Devices {
		var compact bytes.Buffer
		require.NoError(t, json.Compact(&compact, d.Image))
		images[d.Name] = compact.String()
	}
	require.Equal(t, map[string]string{
		"web": `{"file":"images/v1/variant-1/shared/web.tar"}`, "web-copy": `{"file":"images/v1/variant-1/shared/web.tar"}`, "proxy": `"nginx:1.27"`,
	}, images)

	var manifest struct {
		Version   int
		Checksums map[string]string
	}
	require.NoError(t, json.Unmarshal(entries["manifest.json"], &manifest))
	for name, body := range entries {
		if name == "manifest.json" {
			continue
		}
		sum := sha256.Sum256(body)
		require.Equal(t, hex.EncodeToString(sum[:]), manifest.Checksums[name], name)
	}
	require.Len(t, manifest.Checksums, len(entries)-1)

	// The image file is a docker load archive: manifest.json beside the OCI layout.
	require.Contains(t, string(readTar(t, entries["images/v1/variant-1/shared/web.tar"])["manifest.json"]), `"RepoTags"`)

	// Imported again: the same content is pushed (config digest, layers).
	imp := newImportFixture(t)
	imp.expectCreation(t)
	imp.sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
	done := imp.startAndRun(t)
	require.Equal(t, exerciseModel.ImportJobDone, done.State, "error %+v", done.Error)
	require.Len(t, imp.images.pushes, 1)
	require.Equal(t, archivetest.Digest(webImage.Config()), imp.images.pushes[0].Archive.Config.Digest)
	require.Len(t, imp.images.pushes[0].Archive.Layers, 1)
	require.Equal(t, archivetest.Digest(webImage.Layers[0]), imp.images.pushes[0].Archive.Layers[0].Digest)
}

// A password-protected export (secrets) carries the images inside the encrypted zip too, and imports with the password.
func TestExportWithImagesProtected(t *testing.T) {
	f := newExportFixture(t)
	f.task(exportTaskA, "Web lab", deviceSpec{"web", f.web.reference("web")})
	view := f.run(t, exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA}, IncludeSecrets: true, Password: "s3cret-pass"})
	archive := f.onlyZip(t, view)
	require.NotContains(t, f.queued[0].SealedPassword, "s3cret-pass", "the password is sealed in the job")

	zr, err := yzip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	require.NoError(t, err)
	names := map[string]bool{}
	for _, entry := range zr.File {
		require.True(t, entry.IsEncrypted(), entry.Name)
		names[entry.Name] = true
	}
	require.True(t, names["images/v1/variant-1/web.tar"], "the image is in the protected zip")

	imp := newImportFixture(t)
	imp.expectCreation(t)
	imp.sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
	start, err := imp.uc.ImportUploadedArchive(context.Background(), exercise.ImportUploadInput{UploadID: archiveUpID, Password: "s3cret-pass", CreatedBy: importer})
	require.NoError(t, err)
	require.NoError(t, imp.uc.RunArchiveImport(context.Background(), imp.queued[0]))
	done, err := imp.uc.ArchiveImportJob(context.Background(), start.JobID, importer)
	require.NoError(t, err)
	require.Equal(t, exerciseModel.ImportJobDone, done.State, "error %+v", done.Error)
	require.Equal(t, archivetest.Digest(webImage.Config()), imp.images.pushes[0].Archive.Config.Digest)
}

// Several tasks: one folder each (named after the task), one manifest.json at the root; an image shared by two tasks
// is written once in each folder.
func TestExportBundleWithImages(t *testing.T) {
	f := newExportFixture(t)
	f.task(exportTaskA, "Task A", deviceSpec{"web", f.web.reference("web")})
	f.task(exportTaskB, "Task B", deviceSpec{"web", f.web.reference("web")}, deviceSpec{"tool", f.tool.reference("tool")})

	view := f.run(t, exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA, exportTaskB}, FileName: "exercises.cybericebox.zip"})
	entries := readZip(t, f.onlyZip(t, view))
	for _, name := range []string{
		"Task A/exercise.json", "Task A/images/v1/variant-1/web.tar",
		"Task B/exercise.json", "Task B/images/v1/variant-1/web.tar", "Task B/images/v1/variant-1/tool.tar", "manifest.json",
	} {
		require.Contains(t, entries, name)
	}
	require.Len(t, entries, 6)
	var manifest struct{ Checksums map[string]string }
	require.NoError(t, json.Unmarshal(entries["manifest.json"], &manifest))
	require.Contains(t, manifest.Checksums, "Task B/images/v1/variant-1/tool.tar", "the root manifest covers entries by their full path")
}

// An image used in different variants lies in the shared folder of the version; devices of other variants keep theirs.
func TestExportImagePlacementAcrossVariants(t *testing.T) {
	f := newExportFixture(t)
	f.variants(exportTaskA, "Task A", [][]deviceSpec{
		{{"web", f.web.reference("web")}, {"tool", f.tool.reference("tool")}},
		{{"web", f.web.reference("web")}},
	})
	entries := readZip(t, f.onlyZip(t, f.run(t, exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA}})))
	require.Contains(t, entries, "images/v1/shared/web.tar")
	require.Contains(t, entries, "images/v1/variant-1/tool.tar")
	require.Len(t, entries, 4)
}

// The registry refusing an image fails the job with the registry's reason and the device and file it is about; no
// file is stored and no internal text leaks.
func TestExportImageRefusedFailsTheJob(t *testing.T) {
	f := newExportFixture(t)
	f.exporter.failFor[f.tool.reference("tool")] = "unauthorized"
	f.task(exportTaskA, "Task A", deviceSpec{"web", f.web.reference("web")}, deviceSpec{"tool", f.tool.reference("tool")})

	view := f.run(t, exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA}})
	require.Equal(t, exerciseModel.ImportJobFailed, view.State)
	require.Nil(t, view.File)
	want := imageRegistryModel.ErrImageNotExportable.Err().StatusCode()
	require.Equal(t, want.FullCode(), view.Error.Code)
	require.Equal(t, "unauthorized", view.Error.Context["reason"])
	require.Equal(t, "tool", view.Error.Context["device"])
	require.Equal(t, "images/v1/variant-1/tool.tar", view.Error.Context["file"])
	require.Empty(t, f.files.files, "nothing is stored")
}

// A blob lost from the registry after the zip began: the writer's failure is the job's error, not the store's failure
// to read a broken pipe, and nothing is stored.
func TestExportBlobLostWhileStreaming(t *testing.T) {
	f := newExportFixture(t)
	f.web.gone[archivetest.Digest(webImage.Layers[0])] = true
	f.task(exportTaskA, "Task A", deviceSpec{"web", f.web.reference("web")})

	view := f.run(t, exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA}})
	require.Equal(t, exerciseModel.ImportJobFailed, view.State)
	want := imageRegistryModel.ErrImageNotExportable.Err().StatusCode()
	require.Equal(t, want.FullCode(), view.Error.Code)
	require.Equal(t, "not_found", view.Error.Context["reason"])
}

// A reference that is not a platform image stays a reference: no image files, no registry call, a plain zip.
func TestExportWithoutPlatformImages(t *testing.T) {
	f := newExportFixture(t)
	f.task(exportTaskA, "Task A", deviceSpec{"proxy", "nginx:1.27"})
	entries := readZip(t, f.onlyZip(t, f.run(t, exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA}})))
	require.Len(t, entries, 2)
	require.Empty(t, f.exporter.asked)
}

// The job belongs to the user who started it; another user's reads as not found, like an absent job.
func TestExportJobOwnership(t *testing.T) {
	f := newExportFixture(t)
	f.task(exportTaskA, "Task A", deviceSpec{"proxy", "nginx:1.27"})
	view := f.run(t, exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA}})
	_, err := f.uc.ArchiveExportJob(context.Background(), view.ID, otherUser)
	require.ErrorIs(t, err, exerciseModel.ErrExportJobNotFound.Err())
	_, err = f.uc.ArchiveExportJob(context.Background(), uuid.Must(uuid.NewV7()), importer)
	require.ErrorIs(t, err, exerciseModel.ErrExportJobNotFound.Err())
}

// A running export silent past its job's time reads as failed (the server restarted).
func TestExportJobInterrupted(t *testing.T) {
	f := newExportFixture(t)
	id, err := f.uc.StartArchiveExport(context.Background(), exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA}, FileName: "x.zip", CreatedBy: importer})
	require.NoError(t, err)
	f.now = f.now.Add(exerciseModel.ExportJobStaleAfter + time.Minute)
	view, err := f.uc.ArchiveExportJob(context.Background(), id, importer)
	require.NoError(t, err)
	require.Equal(t, exerciseModel.ImportJobFailed, view.State)
	require.Equal(t, exerciseModel.ErrArchiveExportInterrupted.Err().StatusCode().FullCode(), view.Error.Code)
}

// A job that cannot be queued is recorded failed and the cause returned; an export without its dependencies is a
// platform error.
func TestExportStartFailures(t *testing.T) {
	f := newExportFixture(t)
	f.uc.SetArchiveExport(exercise.ArchiveExportDependencies{
		Images: f.exporter, Files: f.files, Sealer: f.cipher,
		Queue: func(context.Context, jobsModel.ExerciseExportArgs) error { return errors.New("redis down at 10.0.0.5") },
	})
	_, err := f.uc.StartArchiveExport(context.Background(), exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA}, FileName: "x.zip", CreatedBy: importer})
	require.Error(t, err)
	require.Len(t, f.jobs, 1)
	for _, j := range f.jobs {
		require.Equal(t, "failed", j.State)
		require.NotContains(t, string(j.Error), "10.0.0.5", "the user is never told the cause")
	}

	bare := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: f.q, Media: newFakeMedia()})
	_, err = bare.StartArchiveExport(context.Background(), exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA}, FileName: "x.zip", CreatedBy: importer})
	require.Error(t, err)
}

// The synchronous export refuses images: only the job streams them.
func TestExportExerciseArchiveRefusesImages(t *testing.T) {
	f := newExportFixture(t)
	_, err := f.uc.ExportExerciseArchive(context.Background(), exportTaskA, exercise.ExportOptions{IncludeImages: true}, exercise.DefaultLimits())
	require.Error(t, err)
}

func readTar(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		require.NoError(t, err)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		out[h.Name] = body
	}
}

// hugeImage announces a size without holding it: it is refused before any byte is streamed.
type hugeImage struct {
	digest string
	size   int64
	wrote  bool
}

func (h *hugeImage) Digest() string { return h.digest }
func (h *hugeImage) Size() int64    { return h.size }
func (h *hugeImage) WriteTo(context.Context, io.Writer) error {
	h.wrote = true
	return nil
}

// Each task is within 10 GiB of images, the bundle is not (an image shared by two tasks is written in both): the
// export is refused up front as too large, before anything is streamed or stored.
func TestExportBundleImagesOverTheArchiveLimitRefusedEarly(t *testing.T) {
	f := newExportFixture(t)
	big := &hugeImage{digest: f.web.layout.ManifestDigest, size: 6 << 30}
	f.exporter.override = big
	f.task(exportTaskA, "Task A", deviceSpec{"web", f.web.reference("web")})
	f.task(exportTaskB, "Task B", deviceSpec{"web", f.web.reference("web")})

	view := f.run(t, exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA, exportTaskB}})
	require.Equal(t, exerciseModel.ImportJobFailed, view.State)
	require.Equal(t, exerciseModel.ErrArchiveTooLarge.Err().StatusCode().FullCode(), view.Error.Code)
	require.False(t, big.wrote, "refused before streaming")
	require.Empty(t, f.files.files)

	// One task alone (6 GiB) is fine for the limit check.
	f2 := newExportFixture(t)
	big2 := &hugeImage{digest: f2.web.layout.ManifestDigest, size: 6 << 30}
	f2.exporter.override = big2
	f2.task(exportTaskA, "Task A", deviceSpec{"web", f2.web.reference("web")})
	view = f2.run(t, exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA}})
	require.NotEqual(t, exerciseModel.ErrArchiveTooLarge.Err().StatusCode().FullCode(), errCode(view))
}

func errCode(v exercise.ExportJobView) int {
	if v.Error == nil {
		return 0
	}
	return v.Error.Code
}

// A done export whose stored file the media cleanup removed reads as failed (expired), not as a raw 404.
func TestExportJobFileRemoved(t *testing.T) {
	f := newExportFixture(t)
	f.task(exportTaskA, "Task A", deviceSpec{"proxy", "nginx:1.27"})
	view := f.run(t, exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA}})
	delete(f.files.meta, view.File.ID)
	got, err := f.uc.ArchiveExportJob(context.Background(), view.ID, importer)
	require.NoError(t, err)
	require.Equal(t, exerciseModel.ImportJobFailed, got.State)
	require.Nil(t, got.File)
	require.Equal(t, exerciseModel.ErrExportFileRemoved.Err().StatusCode().FullCode(), got.Error.Code)
}

// A linked variant's own platform image is shipped like any device image: the compact form names the file under
// device_images, and the archive imports again with the pushed image as the linked variant's own.
func TestExportWithImagesLinkedOwnImageRoundTrip(t *testing.T) {
	f := newExportFixture(t)
	webID := uuid.Must(uuid.NewV7())
	base := exerciseModel.Variant{ID: uuid.Must(uuid.NewV7()), Index: 1, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{
		{ID: webID, Name: "web", Type: "container", Image: f.web.reference("web")},
	}}}
	version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{base, {
		ID: uuid.Must(uuid.NewV7()), Index: 2, LinkedTo: uuid.NullUUID{UUID: base.ID, Valid: true},
		ImageOverrides: []exerciseModel.ImageOverride{{DeviceID: webID, Image: f.tool.reference("tool")}},
	}}}
	require.NoError(t, version.SyncLinkedVariants())
	raw, err := json.Marshal(version.Variants)
	require.NoError(t, err)
	f.q.EXPECT().GetExerciseByID(gomock.Any(), exportTaskA).Return(postgres.Exercise{ID: exportTaskA, Name: "Web lab"}, nil).AnyTimes()
	f.q.EXPECT().ListExerciseVersions(gomock.Any(), exportTaskA).Return([]postgres.ExerciseVersion{
		{ID: uuid.Must(uuid.NewV7()), ExerciseID: exportTaskA, Status: "draft", Variants: raw, CreatedAt: importJobT0},
	}, nil).AnyTimes()

	archive := f.onlyZip(t, f.run(t, exercise.StartExportInput{IDs: []uuid.UUID{exportTaskA}}))
	entries := readZip(t, archive)
	var doc struct {
		Versions []struct {
			Variants []map[string]json.RawMessage
		}
	}
	require.NoError(t, json.Unmarshal(entries["exercise.json"], &doc))
	linked := doc.Versions[0].Variants[1]
	require.NotContains(t, linked, "topology")
	var own map[string]map[string]string
	require.NoError(t, json.Unmarshal(linked["device_images"], &own))
	require.Contains(t, own, "web")
	require.Contains(t, entries, own["web"]["file"], "the own image file is in the archive")
	require.Equal(t, 1, f.tool.calls)

	imp := newImportFixture(t)
	imp.expectCreation(t)
	imp.sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
	done := imp.startAndRun(t)
	require.Equal(t, exerciseModel.ImportJobDone, done.State, "error %+v", done.Error)
	require.Len(t, imp.images.pushes, 2)
	var written []exerciseModel.Variant
	require.NoError(t, json.Unmarshal(imp.written[0], &written))
	require.Len(t, written, 2)
	ownRef := written[1].ImageOverrides[0].Image
	require.True(t, strings.HasPrefix(ownRef, "platform:"), ownRef)
	require.NotEqual(t, "@"+archivetest.Digest(webImage.Config()), ownRef[strings.Index(ownRef, "@"):], "the own image is the tool image")
	require.Equal(t, ownRef, written[1].Topology.Devices[0].Image)
	require.Equal(t, written[0].Topology.Devices[0].ID, written[1].ImageOverrides[0].DeviceID)
	require.NotEqual(t, ownRef, written[0].Topology.Devices[0].Image, "the base keeps its image")
}
