package imageRegistryUseCase

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// dockerHub is an in-process stand-in for Docker Hub: library/* is public,
// everything else wants bob's token.
func dockerHub(t *testing.T, f *accessFixture) *testRegistry {
	hub := newTestRegistry(t, "bob", secretToken, func(r *testRegistry) { r.open = []string{"library/nginx"} })
	f.hosts["docker.io"] = hub.host()
	pushImage(t, hub.host(), "library/nginx", "", "", "1.27")
	pushImage(t, hub.host(), "acme/app", "bob", secretToken, "1")
	return hub
}

// A host-only organizer secret does not capture the public images on its
// host: they are checked and pulled anonymously first.
func TestOrganizerSecretMatchIsCheckedAnonymouslyFirst(t *testing.T) {
	ctx := context.Background()
	hostOnly := func(f *accessFixture) {
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
			f.row(t, secID, "docker.io", "", secretToken),
		}, nil)
	}

	t.Run("platform on Docker Hub and a public image: no conflict, the public image stays anonymous", func(t *testing.T) {
		f := newAccessFixtureOn(t, true, "dockerhub")
		dockerHub(t, f)
		ref, _ := f.platformImage(t)
		hostOnly(f)
		got, err := f.uc.PrepareDeployImages(ctx, eventScope(eventA), topo(ref, "docker.io/library/nginx:1.27"))
		if err != nil {
			t.Fatalf("prepare: %v %+v", err, got.Unavailable)
		}
		want := infraModel.RegistryCredential{Registry: "index.docker.io", Username: "acme", Password: pullToken}
		if len(got.Credentials) != 1 || got.Credentials[0] != want {
			t.Fatalf("only the platform credential: %v", got.Credentials)
		}
		for _, c := range f.externalCalls() {
			if c.password != "" {
				t.Fatalf("the public image was checked with a credential: %+v", c)
			}
		}
	})

	t.Run("a private organizer image still gets the secret", func(t *testing.T) {
		f := newAccessFixtureOn(t, true, "dockerhub")
		dockerHub(t, f)
		hostOnly(f)
		got, err := f.uc.PrepareDeployImages(ctx, eventScope(eventA), topo("docker.io/acme/app:1"))
		if err != nil {
			t.Fatalf("prepare: %v %+v", err, got.Unavailable)
		}
		want := infraModel.RegistryCredential{Registry: "index.docker.io", Username: "bob", Password: secretToken}
		if len(got.Credentials) != 1 || got.Credentials[0] != want {
			t.Fatalf("credentials %v", got.Credentials)
		}
	})

	t.Run("platform and a private organizer image on one host still conflict", func(t *testing.T) {
		f := newAccessFixtureOn(t, true, "dockerhub")
		dockerHub(t, f)
		ref, _ := f.platformImage(t)
		hostOnly(f)
		got, err := f.uc.PrepareDeployImages(ctx, eventScope(eventA), topo(ref, "docker.io/acme/app:1", "docker.io/library/nginx:1.27"))
		if !errors.Is(err, imageRegistryModel.ErrImagesUnavailable.Err()) {
			t.Fatalf("want a refusal, got %v", err)
		}
		r := reasons(got.Unavailable)
		if len(r) != 2 || r[ref] != imageRegistryModel.ReasonCredentialConflict || r["docker.io/acme/app:1"] != imageRegistryModel.ReasonCredentialConflict {
			t.Fatalf("unavailable %v", got.Unavailable)
		}
	})

	t.Run("an anonymous positive result is cached", func(t *testing.T) {
		f := newAccessFixtureOn(t, true, "dockerhub")
		hub := dockerHub(t, f)
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
			f.row(t, secID, "docker.io", "", secretToken),
		}, nil).Times(2)
		before := hub.headCount()
		for range 2 {
			if av, err := f.uc.CheckImageAvailability(ctx, eventScope(eventA), []string{"docker.io/library/nginx:1.27"}); err != nil || av[0].Status != imageRegistryModel.StatusAvailable {
				t.Fatalf("%v %v", av, err)
			}
		}
		if n := hub.headCount() - before; n != 1 {
			t.Fatalf("HEADs %d, want 1 (the second check is cached)", n)
		}
	})
}
