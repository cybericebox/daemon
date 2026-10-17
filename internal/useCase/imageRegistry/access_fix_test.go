package imageRegistryUseCase

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// The agent takes one credential per bare host: Docker Hub spellings collapse
// to index.docker.io and the same secret used twice is one entry.
func TestDockerHubCredentialIsOneEntryOnIndexDockerIO(t *testing.T) {
	f := newAccessFixture(t, true)
	priv := newTestRegistry(t, "bob", secretToken)
	f.hosts["docker.io"] = priv.host()
	pushImage(t, priv.host(), "acme/app", "bob", secretToken, "1")
	pushImage(t, priv.host(), "acme/app", "bob", secretToken, "2")
	f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
		f.row(t, secID, "docker.io", "acme", secretToken),
	}, nil)
	got, err := f.uc.PrepareDeployImages(context.Background(), eventScope(eventA), topo("docker.io/acme/app:1", "index.docker.io/acme/app:2"))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	want := infraModel.RegistryCredential{Registry: "index.docker.io", Username: "bob", Password: secretToken}
	if len(got.Credentials) != 1 || got.Credentials[0] != want {
		t.Fatalf("credentials %v", got.Credentials)
	}
	if got.Topology.Devices[0].Image != "docker.io/acme/app:1" || got.Topology.Devices[1].Image != "index.docker.io/acme/app:2" {
		t.Fatalf("image refs stay as written: %+v", got.Topology.Devices)
	}
}

// Two different credentials for one host cannot be sent: the deploy is refused.
func TestTwoCredentialsForOneHostConflict(t *testing.T) {
	ctx := context.Background()
	t.Run("platform and organizer on ghcr.io", func(t *testing.T) {
		f := newAccessFixture(t, true)
		ref, _ := f.platformImage(t)
		priv := newTestRegistry(t, "bob", secretToken)
		f.hosts["ghcr.io"] = priv.host()
		pushImage(t, priv.host(), "other/app", "bob", secretToken, "1")
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
			f.row(t, secID, "ghcr.io", "other", secretToken),
		}, nil)
		got, err := f.uc.PrepareDeployImages(ctx, eventScope(eventA), topo(ref, "ghcr.io/other/app:1"))
		if !errors.Is(err, imageRegistryModel.ErrImagesUnavailable.Err()) || len(got.Credentials) != 0 {
			t.Fatalf("got %+v %v", got, err)
		}
		r := reasons(got.Unavailable)
		if len(r) != 2 || r[ref] != imageRegistryModel.ReasonCredentialConflict || r["ghcr.io/other/app:1"] != imageRegistryModel.ReasonCredentialConflict {
			t.Fatalf("unavailable %v", got.Unavailable)
		}
	})
	t.Run("two organizer secrets with different prefixes on one host", func(t *testing.T) {
		f := newAccessFixture(t, true)
		priv := newTestRegistry(t, "bob", secretToken, func(r *testRegistry) { r.also = []string{"token-b"} })
		f.hosts["quay.io"] = priv.host()
		pushImage(t, priv.host(), "a/app", "bob", secretToken, "1")
		pushImage(t, priv.host(), "b/app", "bob", secretToken, "1")
		other := uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000e2"))
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
			f.row(t, secID, "quay.io", "a", secretToken), f.row(t, other, "quay.io", "b", "token-b"),
		}, nil)
		got, err := f.uc.PrepareDeployImages(ctx, eventScope(eventA), topo("quay.io/a/app:1", "quay.io/b/app:1"))
		if !errors.Is(err, imageRegistryModel.ErrImagesUnavailable.Err()) || len(reasons(got.Unavailable)) != 2 {
			t.Fatalf("got %+v %v", got, err)
		}
		for _, a := range got.Unavailable {
			if a.Reason != imageRegistryModel.ReasonCredentialConflict {
				t.Fatalf("unavailable %v", got.Unavailable)
			}
		}
	})
	t.Run("more than 8 hosts", func(t *testing.T) {
		f := newAccessFixture(t, true)
		priv := newTestRegistry(t, "bob", secretToken)
		pushImage(t, priv.host(), "acme/app", "bob", secretToken, "1")
		var rows []postgres.EventRegistrySecret
		var images []string
		for i := range 9 {
			host := fmt.Sprintf("r%d.example.com", i)
			f.hosts[host] = priv.host()
			rows = append(rows, f.row(t, uuid.Must(uuid.FromString(fmt.Sprintf("0199a000-0000-7000-8000-0000000001%02d", i))), host, "", secretToken))
			images = append(images, host+"/acme/app:1")
		}
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return(rows, nil)
		got, err := f.uc.PrepareDeployImages(ctx, eventScope(eventA), topo(images...))
		if !errors.Is(err, imageRegistryModel.ErrImagesUnavailable.Err()) || len(got.Unavailable) != 9 || got.Unavailable[0].Reason != imageRegistryModel.ReasonCredentialConflict {
			t.Fatalf("got %+v %v", got.Unavailable, err)
		}
	})
}

// hangingRegistry never answers until the client gives up.
func hangingRegistry(t *testing.T) string {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(done) })
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestARegistryThatHangsIsUnreachable(t *testing.T) {
	ctx := context.Background()
	t.Run("per request timeout", func(t *testing.T) {
		f := newAccessFixture(t, true, func(d *Dependencies) { d.HeadTimeout = 50 * time.Millisecond })
		img := hangingRegistry(t) + "/acme/app:1"
		start := time.Now()
		av, err := f.uc.CheckImageAvailability(ctx, Scope{}, []string{img})
		if err != nil || len(av) != 1 || av[0].Reason != imageRegistryModel.ReasonUnreachable {
			t.Fatalf("got %v %v", av, err)
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("the timeout was not applied")
		}
	})
	t.Run("overall deadline", func(t *testing.T) {
		f := newAccessFixture(t, true, func(d *Dependencies) { d.HeadTimeout = time.Minute; d.CallTimeout = 80 * time.Millisecond })
		host := hangingRegistry(t)
		var images []string
		for i := range 20 {
			images = append(images, fmt.Sprintf("%s/acme/app:%d", host, i))
		}
		start := time.Now()
		got, err := f.uc.PrepareDeployImages(ctx, Scope{}, topo(images...))
		if !errors.Is(err, imageRegistryModel.ErrImagesUnavailable.Err()) || len(got.Unavailable) != 20 {
			t.Fatalf("got %+v %v", got.Unavailable, err)
		}
		for _, a := range got.Unavailable {
			if a.Reason != imageRegistryModel.ReasonUnreachable {
				t.Fatalf("got %v", got.Unavailable)
			}
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("the overall deadline was not applied")
		}
	})
	t.Run("a cancelled caller gets the context error", func(t *testing.T) {
		f := newAccessFixture(t, true, func(d *Dependencies) { d.HeadTimeout = time.Minute })
		img := hangingRegistry(t) + "/acme/app:1"
		cctx, cancel := context.WithCancel(ctx)
		time.AfterFunc(50*time.Millisecond, cancel)
		if _, err := f.uc.CheckImageAvailability(cctx, Scope{}, []string{img}); !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
		if _, err := f.uc.PrepareDeployImages(cctx, Scope{}, topo(img)); !errors.Is(err, context.Canceled) {
			t.Fatalf("prepare: want context.Canceled, got %v", err)
		}
	})
}

// A changed secret (new token → new UpdatedAt) does not reuse a positive result.
func TestCacheForgetsAChangedSecret(t *testing.T) {
	f := newAccessFixture(t, true)
	ctx := context.Background()
	priv := newTestRegistry(t, "bob", secretToken)
	pushImage(t, priv.host(), "acme/app", "bob", secretToken, "1")
	img := priv.host() + "/acme/app:1"
	first := f.row(t, secID, priv.host(), "", secretToken)
	changed := first
	changed.UpdatedAt = t0.Add(time.Second)
	gomock.InOrder(
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{first}, nil),
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{first}, nil),
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{changed}, nil),
	)
	for range 3 {
		if av, err := f.uc.CheckImageAvailability(ctx, eventScope(eventA), []string{img}); err != nil || av[0].Status != imageRegistryModel.StatusAvailable {
			t.Fatalf("%v %v", av, err)
		}
	}
	// each check first tries anonymously (refused, never cached), then with
	// the secret: cached once, checked again after the change
	if n := priv.headCount(); n != 3+2 {
		t.Fatalf("HEADs %d: 3 anonymous + 2 with the secret", n)
	}
}

func TestExerciseImageScope(t *testing.T) {
	ctx := context.Background()
	exID := uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000a1"))
	f := newFixture(t, true)
	gomock.InOrder(
		f.q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID, OwnerEventID: uuid.NullUUID{UUID: eventA, Valid: true}}, nil),
		f.q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID}, nil),
		f.q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{}, pgx.ErrNoRows),
	)
	if s, err := f.uc.ExerciseImageScope(ctx, exID); err != nil || s.EventID == nil || *s.EventID != eventA {
		t.Fatalf("event-owned: %+v %v", s, err)
	}
	if s, err := f.uc.ExerciseImageScope(ctx, exID); err != nil || s.EventID != nil {
		t.Fatalf("catalog: %+v %v", s, err)
	}
	if _, err := f.uc.ExerciseImageScope(ctx, exID); !errors.Is(err, exerciseModel.ErrExerciseNotFound.Err()) {
		t.Fatalf("missing: %v", err)
	}
}
