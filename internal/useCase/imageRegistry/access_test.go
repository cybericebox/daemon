package imageRegistryUseCase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/google/go-containerregistry/pkg/registry"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/ociregistry/provider"
)

const missingDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000001"

// testRegistry is an in-process registry. With a user set it wants basic auth
// with that user and password; with status set it answers every request with
// it. It counts manifest HEADs and records the passwords it saw.
type testRegistry struct {
	srv        *httptest.Server
	user, pass string
	mu         sync.Mutex
	status     int
	heads      int
	seen       []string
}

func newTestRegistry(t *testing.T, user, pass string) *testRegistry {
	r := &testRegistry{user: user, pass: pass}
	inner := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		status := r.status
		if req.Method == http.MethodHead && strings.Contains(req.URL.Path, "/manifests/") {
			r.heads++
		}
		u, p, ok := req.BasicAuth()
		if ok {
			r.seen = append(r.seen, p)
		}
		r.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		if r.user != "" && (!ok || u != r.user || p != r.pass) {
			w.Header().Set("WWW-Authenticate", `Basic realm="fake"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, req)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *testRegistry) host() string { return strings.TrimPrefix(r.srv.URL, "http://") }

func (r *testRegistry) headCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.heads
}

func (r *testRegistry) passwords() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

func (r *testRegistry) setStatus(s int) {
	r.mu.Lock()
	r.status = s
	r.mu.Unlock()
}

// push uploads a fixed one-config image under tag and returns its digest.
func pushImage(t *testing.T, host, repository, user, pass, tag string) string {
	t.Helper()
	c := ociregistry.New(ociregistry.Options{Host: host, Repository: repository, Username: user, Password: pass, Insecure: true, AllowInternal: true})
	cfg := []byte(`{"architecture":"amd64","os":"linux"}`)
	sum := sha256.Sum256(cfg)
	cfgDigest := "sha256:" + hex.EncodeToString(sum[:])
	ctx := context.Background()
	if err := c.PushBlob(ctx, cfgDigest, int64(len(cfg)), strings.NewReader(string(cfg))); err != nil {
		t.Fatalf("push config: %v", err)
	}
	manifest := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":%d},"layers":[]}`, cfgDigest, len(cfg))
	d, err := c.PutManifest(ctx, tag, "application/vnd.oci.image.manifest.v1+json", []byte(manifest))
	if err != nil {
		t.Fatalf("put manifest: %v", err)
	}
	return d
}

type externalCall struct{ host, repository, username, password string }

type accessFixture struct {
	*fixture
	now      time.Time
	mu       sync.Mutex
	external []externalCall
}

// newAccessFixture: the platform registry is the fixture's fake (account
// "acme", repository acme/tasks on ghcr); external registries are reached
// without the guard (in-process fakes on loopback), recording each client.
func newAccessFixture(t *testing.T, configured bool) *accessFixture {
	ctrl := gomock.NewController(t)
	base := &fixture{reg: newFakeRegistry(t), adapter: &fakeAdapter{}}
	base.q = postgresMocks.NewMockQuerier(ctrl)
	f := &accessFixture{fixture: base, now: t0}
	base.cipher = testCipher(t)
	base.uc = New(Dependencies{
		Repo:      base.q,
		Cipher:    base.cipher,
		NewClient: base.reg.newClient,
		Adapter:   func(imageRegistryModel.Provider) provider.Adapter { return base.adapter },
		Now:       func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now },
		NewExternalClient: func(host, repository, username, password string) *ociregistry.Client {
			f.mu.Lock()
			f.external = append(f.external, externalCall{host, repository, username, password})
			f.mu.Unlock()
			return ociregistry.New(ociregistry.Options{Host: host, Repository: repository, Username: username, Password: password, Insecure: true, AllowInternal: true})
		},
	})
	if configured {
		base.stored(postgres.ImageRegistrySetting{
			ID: true, Provider: "ghcr", Account: "acme", Repository: "acme/tasks",
			PushTokenCiphertext: base.seal(t, pushToken, "push"), PullTokenCiphertext: base.seal(t, pullToken, "pull"), UpdatedAt: t0,
		})
	} else {
		base.empty()
	}
	return f
}

func (f *accessFixture) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func (f *accessFixture) externalCalls() []externalCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]externalCall(nil), f.external...)
}

func (f *accessFixture) platformImage(t *testing.T) (ref, digest string) {
	d := pushImage(t, f.reg.host(), "acme/tasks", "acme", pushToken, "web-0123456789ab")
	return "platform:web@" + d, d
}

func (r *fakeRegistry) host() string { return strings.TrimPrefix(r.srv.URL, "http://") }

func topo(images ...string) exerciseModel.Topology {
	var t exerciseModel.Topology
	for i, img := range images {
		t.Devices = append(t.Devices, exerciseModel.Device{
			ID: uuid.Must(uuid.FromString(fmt.Sprintf("0199a000-0000-7000-8000-%012d", i+1))), Name: fmt.Sprintf("d%d", i+1),
			Type: exerciseModel.DeviceTypeContainer, Image: img,
		})
	}
	t.Devices = append(t.Devices, exerciseModel.Device{
		ID: uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000ff")), Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch,
	})
	return t
}

func eventScope(id uuid.UUID) Scope { return Scope{EventID: &id} }

func reasons(av []imageRegistryModel.Availability) map[string]imageRegistryModel.Reason {
	out := map[string]imageRegistryModel.Reason{}
	for _, a := range av {
		out[a.Image] = a.Reason
	}
	return out
}

func TestPrepareDeployRewritesPlatformImagesAndHandsThePullCredential(t *testing.T) {
	f := newAccessFixture(t, true)
	ref, digest := f.platformImage(t)
	in := topo(ref)

	got, err := f.uc.PrepareDeployImages(context.Background(), Scope{}, in)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if want := "ghcr.io/acme/tasks@" + digest; got.Topology.Devices[0].Image != want {
		t.Fatalf("rewritten image %q, want %q", got.Topology.Devices[0].Image, want)
	}
	if in.Devices[0].Image != ref {
		t.Fatal("the input topology must not change")
	}
	if got.Topology.Devices[1].Image != "" || got.Topology.Devices[1].Type != exerciseModel.DeviceTypeUnmanagedSwitch {
		t.Fatalf("forwarding device kept as is: %+v", got.Topology.Devices[1])
	}
	want := []infraModel.RegistryCredential{{Registry: "ghcr.io", Username: "acme", Password: pullToken}}
	if len(got.Credentials) != 1 || got.Credentials[0] != want[0] {
		t.Fatalf("credentials %v", got.Credentials)
	}
	if len(got.Unavailable) != 0 {
		t.Fatalf("unavailable %v", got.Unavailable)
	}
	if pw := f.reg.passwords(); pw[len(pw)-1] != pullToken {
		t.Fatalf("the check uses the pull token, saw %v", pw)
	}
}

func TestPrepareDeployRefusesAMissingPlatformImage(t *testing.T) {
	f := newAccessFixture(t, true)
	ref := "platform:web@" + missingDigest

	got, err := f.uc.PrepareDeployImages(context.Background(), Scope{}, topo(ref))
	if !errors.Is(err, imageRegistryModel.ErrImagesUnavailable.Err()) {
		t.Fatalf("want ErrImagesUnavailable, got %v", err)
	}
	if len(got.Credentials) != 0 {
		t.Fatalf("no credentials on a refusal: %v", got.Credentials)
	}
	if len(got.Unavailable) != 1 || got.Unavailable[0] != (imageRegistryModel.Availability{Image: ref, Status: imageRegistryModel.StatusUnavailable, Reason: imageRegistryModel.ReasonNotFound}) {
		t.Fatalf("unavailable %v", got.Unavailable)
	}
	// The client gets the list in the public context, never a token.
	var pe interface{ PublicContext() map[string]any }
	if !errors.As(err, &pe) {
		t.Fatalf("not an app error: %v", err)
	}
	body, _ := json.Marshal(pe.PublicContext())
	if string(body) != `{"images":[{"Image":"`+ref+`","Status":"unavailable","Reason":"not_found"}]}` {
		t.Fatalf("public context %s", body)
	}
	if strings.Contains(err.Error(), pullToken) || strings.Contains(err.Error(), pushToken) {
		t.Fatal("token in the error")
	}
}

func TestPlatformImageWithoutSettingsIsNotConfigured(t *testing.T) {
	f := newAccessFixture(t, false)
	ref := "platform:web@" + missingDigest
	av, err := f.uc.CheckImageAvailability(context.Background(), Scope{}, []string{ref})
	if err != nil {
		t.Fatal(err)
	}
	if len(av) != 1 || av[0].Status != imageRegistryModel.StatusUnavailable || av[0].Reason != imageRegistryModel.ReasonNotConfigured {
		t.Fatalf("got %v", av)
	}
}

func TestPublicImages(t *testing.T) {
	ctx := context.Background()
	t.Run("an anonymous image is available, with no credential", func(t *testing.T) {
		f := newAccessFixture(t, true)
		pub := newTestRegistry(t, "", "")
		d := pushImage(t, pub.host(), "library/nginx", "", "", "1.27")
		img := pub.host() + "/library/nginx:1.27"
		got, err := f.uc.PrepareDeployImages(ctx, Scope{}, topo(img, pub.host()+"/library/nginx@"+d))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Credentials) != 0 || got.Topology.Devices[0].Image != img {
			t.Fatalf("public: %+v", got)
		}
		for _, c := range f.externalCalls() {
			if c.username != "" || c.password != "" {
				t.Fatalf("public check must be anonymous: %+v", c)
			}
		}
	})
	t.Run("unauthorized without an event: needs an event secret", func(t *testing.T) {
		f := newAccessFixture(t, true)
		priv := newTestRegistry(t, "bob", secretToken)
		img := priv.host() + "/acme/app:1"
		av, err := f.uc.CheckImageAvailability(ctx, Scope{}, []string{img})
		if err != nil {
			t.Fatal(err)
		}
		if reasons(av)[img] != imageRegistryModel.ReasonNeedsEventSecret {
			t.Fatalf("got %v", av)
		}
	})
	t.Run("unauthorized inside an event without a matching secret", func(t *testing.T) {
		f := newAccessFixture(t, true)
		priv := newTestRegistry(t, "bob", secretToken)
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventB).Return(nil, nil)
		img := priv.host() + "/acme/app:1"
		got, err := f.uc.PrepareDeployImages(ctx, eventScope(eventB), topo(img))
		if !errors.Is(err, imageRegistryModel.ErrImagesUnavailable.Err()) || reasons(got.Unavailable)[img] != imageRegistryModel.ReasonUnauthorized {
			t.Fatalf("got %+v %v", got, err)
		}
	})
	t.Run("missing and throttled", func(t *testing.T) {
		f := newAccessFixture(t, true)
		pub := newTestRegistry(t, "", "")
		missing := pub.host() + "/library/nginx:404"
		av, err := f.uc.CheckImageAvailability(ctx, Scope{}, []string{missing})
		if err != nil || reasons(av)[missing] != imageRegistryModel.ReasonNotFound {
			t.Fatalf("missing: %v %v", av, err)
		}
		pub.setStatus(http.StatusTooManyRequests)
		throttled := pub.host() + "/library/nginx:1"
		av, err = f.uc.CheckImageAvailability(ctx, Scope{}, []string{throttled})
		if err != nil || reasons(av)[throttled] != imageRegistryModel.ReasonUnreachable {
			t.Fatalf("429: %v %v", av, err)
		}
	})
	t.Run("invalid references", func(t *testing.T) {
		f := newAccessFixture(t, true)
		av, err := f.uc.CheckImageAvailability(ctx, Scope{}, []string{"platform:Bad@sha256:x", "http://x/y", "a b"})
		if err != nil {
			t.Fatal(err)
		}
		if len(av) != 3 {
			t.Fatalf("got %v", av)
		}
		for _, a := range av {
			if a.Reason != imageRegistryModel.ReasonInvalidReference || a.Status != imageRegistryModel.StatusUnavailable {
				t.Fatalf("got %v", av)
			}
		}
		if len(f.externalCalls()) != 0 {
			t.Fatal("an invalid reference reaches no registry")
		}
	})
}

func TestEventSecretOnlyInsideItsEvent(t *testing.T) {
	ctx := context.Background()
	priv := newTestRegistry(t, "bob", secretToken)
	img := priv.host() + "/acme/app:1"
	other := uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000e2"))

	t.Run("matching event: checked and handed with the secret", func(t *testing.T) {
		f := newAccessFixture(t, true)
		pushImage(t, priv.host(), "acme/app", "bob", secretToken, "1")
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
			f.row(t, secID, priv.host(), "acme", secretToken),
			f.row(t, other, "quay.io", "", "unrelated-token"),
		}, nil)
		got, err := f.uc.PrepareDeployImages(ctx, eventScope(eventA), topo(img))
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		want := infraModel.RegistryCredential{Registry: priv.host(), Username: "bob", Password: secretToken}
		if len(got.Credentials) != 1 || got.Credentials[0] != want {
			t.Fatalf("credentials %v", got.Credentials)
		}
		if got.Topology.Devices[0].Image != img {
			t.Fatalf("organizer images are not rewritten: %q", got.Topology.Devices[0].Image)
		}
		for _, c := range f.externalCalls() {
			if c.password == "unrelated-token" {
				t.Fatal("an unrelated secret was used")
			}
		}
	})
	t.Run("another event never gets it", func(t *testing.T) {
		f := newAccessFixture(t, true)
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventB).Return(nil, nil)
		got, err := f.uc.PrepareDeployImages(ctx, eventScope(eventB), topo(img))
		if !errors.Is(err, imageRegistryModel.ErrImagesUnavailable.Err()) || len(got.Credentials) != 0 {
			t.Fatalf("got %+v %v", got, err)
		}
		for _, c := range f.externalCalls() {
			if c.password != "" {
				t.Fatalf("a credential was used outside its event: %+v", c)
			}
		}
	})
}

func TestCredentialsListOnlyWhatTheImagesNeed(t *testing.T) {
	f := newAccessFixture(t, true)
	ctx := context.Background()
	pub := newTestRegistry(t, "", "")
	pushImage(t, pub.host(), "library/nginx", "", "", "1.27")
	f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
		f.row(t, secID, "ghcr.io", "acme", secretToken),
	}, nil)
	// a public image only: neither the platform credential nor the event secret
	got, err := f.uc.PrepareDeployImages(ctx, eventScope(eventA), topo(pub.host()+"/library/nginx:1.27"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Credentials) != 0 {
		t.Fatalf("credentials %v", got.Credentials)
	}
	// the same platform image twice: one credential
	ref, _ := f.platformImage(t)
	got, err = f.uc.PrepareDeployImages(ctx, Scope{}, topo(ref, ref))
	if err != nil || len(got.Credentials) != 1 || got.Credentials[0].Password != pullToken {
		t.Fatalf("got %+v %v", got.Credentials, err)
	}
}

func TestPositiveResultsAreCachedForAMinute(t *testing.T) {
	f := newAccessFixture(t, true)
	ctx := context.Background()
	pub := newTestRegistry(t, "", "")
	pushImage(t, pub.host(), "library/nginx", "", "", "1.27")
	img := pub.host() + "/library/nginx:1.27"
	missing := pub.host() + "/library/nginx:404"

	for range 2 {
		if av, err := f.uc.CheckImageAvailability(ctx, Scope{}, []string{img, missing}); err != nil || av[0].Status != imageRegistryModel.StatusAvailable {
			t.Fatalf("%v %v", av, err)
		}
	}
	if n := pub.headCount(); n != 3 {
		t.Fatalf("HEADs %d: one for the cached image, two for the missing one (never cached)", n)
	}
	f.advance(59 * time.Second)
	_, _ = f.uc.CheckImageAvailability(ctx, Scope{}, []string{img})
	if n := pub.headCount(); n != 3 {
		t.Fatalf("still cached at 59 s: %d", n)
	}
	f.advance(2 * time.Second)
	_, _ = f.uc.CheckImageAvailability(ctx, Scope{}, []string{img})
	if n := pub.headCount(); n != 4 {
		t.Fatalf("expired after 60 s: %d", n)
	}
}

func TestCacheIsPerCredential(t *testing.T) {
	ctx := context.Background()
	f := newAccessFixture(t, true)
	priv := newTestRegistry(t, "bob", secretToken)
	pushImage(t, priv.host(), "acme/app", "bob", secretToken, "1")
	img := priv.host() + "/acme/app:1"
	f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
		f.row(t, secID, priv.host(), "", secretToken),
	}, nil)
	if av, err := f.uc.CheckImageAvailability(ctx, eventScope(eventA), []string{img}); err != nil || av[0].Status != imageRegistryModel.StatusAvailable {
		t.Fatalf("event A: %v %v", av, err)
	}
	// a positive result under event A's secret is no answer for an anonymous check
	if av, err := f.uc.CheckImageAvailability(ctx, Scope{}, []string{img}); err != nil || av[0].Reason != imageRegistryModel.ReasonNeedsEventSecret {
		t.Fatalf("anonymous: %v %v", av, err)
	}
}

func TestEventImagesAvailability(t *testing.T) {
	f := newAccessFixture(t, true)
	ctx := context.Background()
	pub := newTestRegistry(t, "", "")
	pushImage(t, pub.host(), "library/nginx", "", "", "1.27")
	ok := pub.host() + "/library/nginx:1.27"
	missing := pub.host() + "/library/nginx:404"
	ref, _ := f.platformImage(t)
	exA, exB := uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000a1")), uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000b1"))
	attA, attB := uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000a2")), uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000b2"))
	f.q.EXPECT().ListEventExerciseImages(gomock.Any(), eventA).Return([]postgres.ListEventExerciseImagesRow{
		{EventExerciseID: attA, ExerciseID: exA, ExerciseName: "Web", Images: []string{ok, ref}},
		{EventExerciseID: attB, ExerciseID: exB, ExerciseName: "Pwn", Images: []string{missing, "platform:gone@" + missingDigest}},
	}, nil)
	f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return(nil, nil)

	got, err := f.uc.EventImagesAvailability(ctx, eventA)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].EventExerciseID != attB || got[0].ExerciseID != exB || got[0].ExerciseName != "Pwn" {
		t.Fatalf("only attachments with unavailable images: %+v", got)
	}
	r := reasons(got[0].Unavailable)
	if len(r) != 2 || r[missing] != imageRegistryModel.ReasonNotFound || r["platform:gone@"+missingDigest] != imageRegistryModel.ReasonNotFound {
		t.Fatalf("unavailable %v", got[0].Unavailable)
	}
}

// The default client for image hosts dials through the guard: a loopback
// registry is never reached.
func TestDefaultExternalClientRefusesInternalAddresses(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := New(Dependencies{Repo: q, Cipher: testCipher(t), Now: func() time.Time { return t0 }})
	pub := newTestRegistry(t, "", "")
	img := pub.host() + "/library/nginx:1"
	av, err := uc.CheckImageAvailability(context.Background(), Scope{}, []string{img})
	if err != nil || len(av) != 1 || av[0].Reason != imageRegistryModel.ReasonUnreachable {
		t.Fatalf("got %v %v", av, err)
	}
	if n := pub.headCount(); n != 0 {
		t.Fatalf("the loopback registry was reached: %d", n)
	}
}
