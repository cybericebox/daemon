package imageRegistryUseCase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	"github.com/cybericebox/daemon/pkg/ociregistry"
)

// ownRegistry is an in-process stand-in for the zot of the laboratory chart: basic auth for the platform-writer
// account ("acme" with pushToken), blobs kept per repository (the in-memory registry keeps one pool), cross
// repository mounts when mounts is true, and a log of every request.
type ownRegistry struct {
	srv    *httptest.Server
	mounts bool

	mu   sync.Mutex
	log  []string
	have map[string]bool // repository + " " + digest
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func newOwnRegistry(t *testing.T, mounts bool) *ownRegistry {
	o := &ownRegistry{mounts: mounts, have: map[string]bool{}}
	inner := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	o.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "acme" || p != pushToken {
			w.Header().Set("WWW-Authenticate", `Basic realm="fake"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		o.mu.Lock()
		o.log = append(o.log, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		o.mu.Unlock()
		repo, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v2/"), "/blobs/")
		digest := strings.TrimPrefix(rest, "/")
		switch {
		case strings.Contains(r.URL.Path, "/blobs/") && !strings.Contains(r.URL.Path, "/blobs/uploads") && (r.Method == http.MethodHead || r.Method == http.MethodGet):
			o.mu.Lock()
			ok := o.have[repo+" "+digest]
			o.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		case r.Method == http.MethodPost && r.URL.Query().Get("mount") != "" && o.mounts:
			o.mu.Lock()
			o.have[strings.TrimSuffix(repo, "/blobs/uploads/")+" "+r.URL.Query().Get("mount")] = true
			o.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			return
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/blobs/uploads/"):
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			inner.ServeHTTP(sw, r)
			if sw.status == http.StatusCreated {
				upRepo, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v2/"), "/blobs/uploads/")
				o.mu.Lock()
				o.have[upRepo+" "+r.URL.Query().Get("digest")] = true
				o.mu.Unlock()
			}
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(o.srv.Close)
	return o
}

func (o *ownRegistry) host() string { return strings.TrimPrefix(o.srv.URL, "http://") }

func (o *ownRegistry) settings() OwnRegistry {
	return OwnRegistry{Host: o.host(), Username: "acme", Password: pushToken}
}

func (o *ownRegistry) client(repository string) *ociregistry.Client {
	return DefaultOwnClient(o.settings(), repository)
}

func (o *ownRegistry) requests(contains string) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []string
	for _, l := range o.log {
		if strings.Contains(l, contains) {
			out = append(out, l)
		}
	}
	return out
}

func (o *ownRegistry) preload(t *testing.T, repository string, blobs ...[]byte) {
	t.Helper()
	for _, b := range blobs {
		if err := o.client(repository).PushBlob(context.Background(), digestOf(b), int64(len(b)), bytes.NewReader(b)); err != nil {
			t.Fatal(err)
		}
	}
}

type ownFixture struct {
	*accessFixture
	own    *ownRegistry
	media  *fakeMedia
	queued []jobsModel.ImageBlobPushArgs
	pushes map[string]postgres.ImageBlobPush
}

func newOwnFixture(t *testing.T, mounts bool) *ownFixture { return newOwnFixtureWith(t, mounts, false) }

// newOwnFixtureWith can also save the legacy provider settings (ghcr, acme/tasks): the own registry ignores
// them, migrate-uploads copies from them.
func newOwnFixtureWith(t *testing.T, mounts, legacy bool) *ownFixture {
	o := newOwnRegistry(t, mounts)
	media := newFakeMedia()
	f := &ownFixture{own: o, media: media, pushes: map[string]postgres.ImageBlobPush{}}
	f.accessFixture = newAccessFixture(t, legacy, func(d *Dependencies) {
		d.Own = o.settings()
		d.Media = media
		d.QueueBlobPush = func(_ context.Context, args jobsModel.ImageBlobPushArgs) error {
			f.queued = append(f.queued, args)
			return nil
		}
	})
	return f
}

func (f *ownFixture) expectCreate(t *testing.T, recordedName string) imageRegistryModel.PlatformRef {
	t.Helper()
	in := testImage()
	m, err := imageRegistryModel.BuildManifest(in.Config, in.Layers)
	if err != nil {
		t.Fatal(err)
	}
	name := "my-web"
	if recordedName != "" {
		f.q.EXPECT().PlatformImageNameByDigest(gomock.Any(), m.Digest).Return(recordedName, nil)
		name = recordedName
	} else {
		f.q.EXPECT().PlatformImageNameByDigest(gomock.Any(), m.Digest).Return("", pgx.ErrNoRows)
	}
	ref, _ := imageRegistryModel.NewPlatformRef(name, m.Digest)
	f.q.EXPECT().PlatformImageTagTakenByOtherDigest(gomock.Any(), postgres.PlatformImageTagTakenByOtherDigestParams{Tag: ref.Tag(), Digest: m.Digest}).Return(false, nil)
	f.q.EXPECT().UpsertPlatformImage(gomock.Any(), gomock.Any()).Return(nil)
	return ref
}

// An upload: the blobs wait in the staging repository, the image is created in platform/<name>, the blobs are
// mounted into it and the manifest is pushed under the digest tag. The reference the device stores is
// platform:<name>@<digest>, which is the path the agent pulls.
func TestOwnCreateImageMountsTheStagedBlobsIntoTheImageRepository(t *testing.T) {
	f := newOwnFixture(t, true)
	f.own.preload(t, imageRegistryModel.StagingRepository, configBytes, layerBytes)
	ref := f.expectCreate(t, "")

	res, err := f.uc.CreateImage(as(uploader), exerciseID, testImage())
	if err != nil {
		t.Fatal(err)
	}
	if res.Reference != ref.String() {
		t.Fatalf("reference %q, want %q", res.Reference, ref.String())
	}
	target := f.own.client(ref.Repository())
	if ok, err := target.ManifestExists(context.Background(), ref.Digest); err != nil || !ok {
		t.Fatalf("manifest by digest: %v %v", ok, err)
	}
	if ok, err := target.ManifestExists(context.Background(), ref.Tag()); err != nil || !ok {
		t.Fatalf("manifest by tag: %v %v", ok, err)
	}
	if n := len(f.own.requests("POST /v2/platform/my-web/blobs/uploads/?from=platform%2Fblob--staging&mount=")); n != 2 {
		t.Fatalf("mount requests %d, want 2 (config and layer): %v", n, f.own.requests("mount"))
	}
	if puts := f.own.requests("PUT /v2/platform/my-web/blobs/uploads/"); len(puts) != 0 {
		t.Fatalf("no blob bytes move when the registry mounts: %v", puts)
	}
}

// A registry that does not mount gets the blobs streamed from the staging repository: same result.
func TestOwnCreateImageStreamsTheBlobsWhenTheRegistryDoesNotMount(t *testing.T) {
	f := newOwnFixture(t, false)
	f.own.preload(t, imageRegistryModel.StagingRepository, configBytes, layerBytes)
	ref := f.expectCreate(t, "")

	if _, err := f.uc.CreateImage(as(uploader), exerciseID, testImage()); err != nil {
		t.Fatal(err)
	}
	if puts := f.own.requests("PUT /v2/platform/my-web/blobs/uploads/"); len(puts) != 2 {
		t.Fatalf("streamed blob uploads %d, want 2: %v", len(puts), puts)
	}
	for _, b := range [][]byte{configBytes, layerBytes} {
		if ok, err := f.own.client(ref.Repository()).BlobExists(context.Background(), digestOf(b)); err != nil || !ok {
			t.Fatalf("blob %s not in the image repository: %v %v", digestOf(b), ok, err)
		}
	}
	if ok, _ := f.own.client(ref.Repository()).ManifestExists(context.Background(), ref.Digest); !ok {
		t.Fatal("manifest missing")
	}
}

// A blob the staging repository lacks refuses the creation before anything is recorded.
func TestOwnCreateImageNeedsTheBlobsInStaging(t *testing.T) {
	f := newOwnFixture(t, true)
	f.own.preload(t, imageRegistryModel.StagingRepository, configBytes)
	f.q.EXPECT().PlatformImageNameByDigest(gomock.Any(), gomock.Any()).Return("", pgx.ErrNoRows)
	if _, err := f.uc.CreateImage(as(uploader), exerciseID, testImage()); !errors.Is(err, imageRegistryModel.ErrBlobMissing.Err()) {
		t.Fatalf("got %v", err)
	}
}

// One digest lives in one repository: an image uploaded again under another name keeps the recorded name.
func TestOwnCreateImageKeepsTheRecordedNameOfADigest(t *testing.T) {
	f := newOwnFixture(t, true)
	f.own.preload(t, imageRegistryModel.StagingRepository, configBytes, layerBytes)
	ref := f.expectCreate(t, "first-name")

	res, err := f.uc.CreateImage(as(uploader), exerciseID, testImage())
	if err != nil {
		t.Fatal(err)
	}
	if res.Reference != ref.String() || !strings.HasPrefix(res.Reference, "platform:first-name@") {
		t.Fatalf("reference %q", res.Reference)
	}
	if ok, _ := f.own.client("platform/my-web").ManifestExists(context.Background(), ref.Digest); ok {
		t.Fatal("a second repository holds the image")
	}
}

// Blob checks and pushes go to the staging repository, whatever name the image will get.
func TestOwnBlobStepsUseTheStagingRepository(t *testing.T) {
	f := newOwnFixture(t, true)
	f.expectPushRows()
	f.media.stage(uploadID, uploader, layerBytes)
	if got, err := f.uc.MissingBlobs(as(uploader), []string{digestOf(layerBytes)}); err != nil || len(got) != 1 {
		t.Fatalf("missing %v %v", got, err)
	}
	if st, err := f.uc.PushStagedBlob(as(uploader), layerPush()); err != nil || st.State != imageRegistryModel.BlobPushing {
		t.Fatalf("push %+v %v", st, err)
	}
	if err := f.uc.PushImageBlob(context.Background(), f.queued[0]); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.own.client(imageRegistryModel.StagingRepository).BlobExists(context.Background(), digestOf(layerBytes)); !ok {
		t.Fatal("the blob is not in the staging repository")
	}
	if got, err := f.uc.MissingBlobs(as(uploader), []string{digestOf(layerBytes)}); err != nil || len(got) != 0 {
		t.Fatalf("missing after push %v %v", got, err)
	}
	if st, err := f.uc.ImageBlobStatus(as(uploader), digestOf(layerBytes)); err != nil || st.State != imageRegistryModel.BlobPresent {
		t.Fatalf("status %+v %v", st, err)
	}
}

func (f *ownFixture) expectPushRows() {
	f.q.EXPECT().UpsertImageBlobPush(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpsertImageBlobPushParams) error {
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
	f.q.EXPECT().DeleteImageBlobPush(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, d string) error {
		delete(f.pushes, d)
		return nil
	}).AnyTimes()
}

// A platform reference reaches the agent as it is, without a credential: the agent maps it to the node registry,
// the node forwarder holds the reader account. The registry is asked only whether the manifest is there.
func TestOwnDeployKeepsPlatformReferencesAndSendsNoCredential(t *testing.T) {
	f := newOwnFixture(t, true)
	f.own.preload(t, imageRegistryModel.StagingRepository, configBytes, layerBytes)
	ref := f.expectCreate(t, "")
	if _, err := f.uc.CreateImage(as(uploader), exerciseID, testImage()); err != nil {
		t.Fatal(err)
	}

	got, err := f.uc.PrepareDeployImages(context.Background(), Scope{}, topo(ref.String()))
	if err != nil {
		t.Fatal(err)
	}
	if got.Topology.Devices[0].Image != ref.String() {
		t.Fatalf("image %q, want %q unchanged", got.Topology.Devices[0].Image, ref.String())
	}
	if len(got.Credentials) != 0 {
		t.Fatalf("credentials %d, want none", len(got.Credentials))
	}
}

func TestOwnDeployRefusesAnImageTheRegistryLacks(t *testing.T) {
	f := newOwnFixture(t, true)
	missing := "platform:web@sha256:" + strings.Repeat("a", 64)
	_, err := f.uc.PrepareDeployImages(context.Background(), Scope{}, topo(missing))
	if !errors.Is(err, imageRegistryModel.ErrImagesUnavailable.Err()) {
		t.Fatalf("got %v", err)
	}
	images, _ := publicReason2(err)
	if len(images) != 1 || images[0].Reason != imageRegistryModel.ReasonNotFound {
		t.Fatalf("images %+v", images)
	}
}

func publicReason2(err error) ([]imageRegistryModel.Availability, bool) {
	var pe interface{ PublicContext() map[string]any }
	if !errors.As(err, &pe) {
		return nil, false
	}
	images, ok := pe.PublicContext()[imageRegistryModel.ContextImages].([]imageRegistryModel.Availability)
	return images, ok
}

// The collector deletes the manifest by digest from the repository of the recorded name; a manifest that is
// already gone does not keep the row.
func TestOwnCollectDeletesTheManifestAndTheRow(t *testing.T) {
	f := newOwnFixture(t, true)
	f.own.preload(t, imageRegistryModel.StagingRepository, configBytes, layerBytes)
	ref := f.expectCreate(t, "")
	if _, err := f.uc.CreateImage(as(uploader), exerciseID, testImage()); err != nil {
		t.Fatal(err)
	}

	f.expectSweep()
	f.unreferenced(postgres.PlatformImage{Digest: ref.Digest, Tag: ref.Tag(), Name: ref.Name, UploadedAt: t0.Add(-48 * 3600e9)})
	f.expectCollectable(ref.Digest, true)
	f.expectRowDeleted(ref.Digest, ref.Tag())
	n, err := f.uc.CollectUnusedTags(context.Background(), t0)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if ok, _ := f.own.client(ref.Repository()).ManifestExists(context.Background(), ref.Digest); ok {
		t.Fatal("the manifest is still in the registry")
	}
	if del := f.own.requests("DELETE /v2/platform/my-web/manifests/" + ref.Digest); len(del) != 1 {
		t.Fatalf("delete requests %v", del)
	}

	// again, with the manifest gone: not a failure, the row goes
	f.expectSweep()
	f.unreferenced(postgres.PlatformImage{Digest: ref.Digest, Tag: ref.Tag(), Name: ref.Name, UploadedAt: t0.Add(-48 * 3600e9)})
	f.expectCollectable(ref.Digest, true)
	f.expectRowDeleted(ref.Digest, ref.Tag())
	if n, err = f.uc.CollectUnusedTags(context.Background(), t0); err != nil || n != 1 {
		t.Fatalf("second pass n=%d err=%v", n, err)
	}
}

// A registry failure keeps the row for the next pass.
func TestOwnCollectKeepsTheRowWhenTheRegistryFails(t *testing.T) {
	f := newOwnFixture(t, true)
	f.own.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	digest := "sha256:" + strings.Repeat("b", 64)
	f.expectSweep()
	f.unreferenced(postgres.PlatformImage{Digest: digest, Tag: "sha256-" + strings.Repeat("b", 64), Name: "web", UploadedAt: t0.Add(-48 * 3600e9)})
	f.expectCollectable(digest, true)
	n, err := f.uc.CollectUnusedTags(context.Background(), t0)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// The panel shows the own registry as managed; saving, listing and the provider settings do not apply.
func TestOwnSettingsAreManagedByTheDeployment(t *testing.T) {
	f := newOwnFixture(t, true)
	v, err := f.uc.GetImageRegistrySettings(context.Background())
	if err != nil || !v.Configured || !v.Managed || v.Provider != "own" || v.PushTokenSet || v.PullTokenSet || v.SecretsKeyMissing {
		t.Fatalf("view %+v %v", v, err)
	}
	if _, err = f.uc.SaveImageRegistrySettings(context.Background(), SaveSettingsInput{Provider: "ghcr", Account: "a", Repository: "a/b", PushToken: "t"}); !errors.Is(err, imageRegistryModel.ErrRegistryManaged.Err()) {
		t.Fatalf("save %v", err)
	}
	if _, err = f.uc.ListImageRegistryRepositories(context.Background(), ListRepositoriesInput{Provider: "ghcr", Account: "a", Token: "t"}); !errors.Is(err, imageRegistryModel.ErrRegistryManaged.Err()) {
		t.Fatalf("list %v", err)
	}
	v, err = f.uc.CheckPlatformRegistry(context.Background())
	if err != nil || v.PushCheck.State != string(imageRegistryModel.CheckOK) {
		t.Fatalf("check %+v %v", v, err)
	}
}

func TestOwnRegistryValidate(t *testing.T) {
	good := OwnRegistry{Host: "laboratory-registry.lab.svc:5000", Username: "platform-writer", Password: "pw-secret-2"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (OwnRegistry{}).Validate(); err != nil {
		t.Fatalf("not configured is valid: %v", err)
	}
	for name, o := range map[string]OwnRegistry{
		"scheme":        {Host: "http://r:5000", Username: "u", Password: "pw-secret-1"},
		"path":          {Host: "r:5000/platform", Username: "u", Password: "pw-secret-1"},
		"space":         {Host: "r :5000", Username: "u", Password: "pw-secret-1"},
		"no account":    {Host: "r:5000", Password: "pw-secret-1"},
		"no password":   {Host: "r:5000", Username: "u"},
		"colon in user": {Host: "r:5000", Username: "u:v", Password: "pw-secret-1"},
	} {
		err := o.Validate()
		if err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if o.Password != "" && strings.Contains(err.Error(), o.Password) {
			t.Fatalf("%s: the error carries the password", name)
		}
	}
}

// An archive preview reads the digest tag from the repository of the image.
func TestOwnExportReadsFromTheImageRepository(t *testing.T) {
	f := newOwnFixture(t, true)
	f.own.preload(t, imageRegistryModel.StagingRepository, configBytes, layerBytes)
	ref := f.expectCreate(t, "")
	if _, err := f.uc.CreateImage(as(uploader), exerciseID, testImage()); err != nil {
		t.Fatal(err)
	}
	exp, err := f.uc.ResolveImageExport(context.Background(), ref.String())
	if err != nil || exp.Digest() != ref.Digest {
		t.Fatalf("export %v %v", exp.Digest(), err)
	}
}

// migrate-uploads: every recorded image and every reference a device names is copied from the provider repository
// into platform/<name>; a second run finds everything present; an image the provider no longer has fails alone.
func TestMigrateUploadsCopiesIntoTheOwnRegistry(t *testing.T) {
	// the legacy provider registry: the fixture's fake, repository acme/tasks, one image under its tag
	f := newOwnFixtureWith(t, true, true)
	digest := pushImage(t, f.reg.host(), "acme/tasks", "acme", pushToken, "web-0123456789ab")
	gone := "sha256:" + strings.Repeat("0", 64)
	f.q.EXPECT().ListPlatformImages(gomock.Any()).Return([]postgres.PlatformImage{
		{Digest: digest, Tag: "sha256-" + strings.TrimPrefix(digest, "sha256:"), Name: "web", UploadedAt: t0},
	}, nil).Times(2)
	f.q.EXPECT().ListReferencedPlatformImages(gomock.Any()).Return([]string{
		"platform:other@" + digest, "platform:web@" + digest, "platform:lost@" + gone, "platform:BAD NAME@" + digest, "nginx:1",
	}, nil).Times(2)

	var lines []string
	report, err := f.uc.MigrateUploads(context.Background(), func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatal(err)
	}
	if report.Images != 3 || report.Copied != 2 || report.Present != 0 || report.Failed != 1 {
		t.Fatalf("report %+v lines %v", report, lines)
	}
	for _, name := range []string{"web", "other"} {
		ref, _ := imageRegistryModel.NewPlatformRef(name, digest)
		c := f.own.client(ref.Repository())
		if ok, err := c.ManifestExists(context.Background(), ref.Digest); err != nil || !ok {
			t.Fatalf("%s: manifest by digest %v %v", name, ok, err)
		}
		if ok, err := c.ManifestExists(context.Background(), ref.Tag()); err != nil || !ok {
			t.Fatalf("%s: manifest by tag %v %v", name, ok, err)
		}
	}
	for _, l := range lines {
		if strings.Contains(l, pushToken) || strings.Contains(l, pullToken) {
			t.Fatalf("a token in the output: %q", l)
		}
	}

	lines = nil
	report, err = f.uc.MigrateUploads(context.Background(), func(l string) { lines = append(lines, l) })
	if err != nil || report.Copied != 0 || report.Present != 2 || report.Failed != 1 {
		t.Fatalf("second run %+v %v lines %v", report, err, lines)
	}
}

func TestMigrateUploadsNeedsTheOwnRegistryAndASource(t *testing.T) {
	f := newAccessFixture(t, true)
	if _, err := f.uc.MigrateUploads(context.Background(), func(string) {}); !errors.Is(err, ErrMigrateNeedsOwn) {
		t.Fatalf("no own registry: %v", err)
	}
	g := newOwnFixture(t, true)
	if _, err := g.uc.MigrateUploads(context.Background(), func(string) {}); !errors.Is(err, ErrMigrateNoSource) {
		t.Fatalf("no provider settings: %v", err)
	}
}
