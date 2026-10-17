package imageRegistryUseCase

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
)

// The editor check shows a credential conflict before any start: the same rule as the deploy.
func TestCheckImageAvailabilityShowsCredentialConflicts(t *testing.T) {
	f := newAccessFixture(t, true)
	ref, _ := f.platformImage(t)
	priv := newTestRegistry(t, "bob", secretToken)
	f.hosts["ghcr.io"] = priv.host()
	pushImage(t, priv.host(), "other/app", "bob", secretToken, "1")
	f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
		f.row(t, secID, "ghcr.io", "other", secretToken),
	}, nil)
	av, err := f.uc.CheckImageAvailability(context.Background(), eventScope(eventA), []string{"ghcr.io/other/app:1", ref, ref})
	if err != nil {
		t.Fatal(err)
	}
	r := reasons(av)
	if r[ref] != imageRegistryModel.ReasonCredentialConflict || r["ghcr.io/other/app:1"] != imageRegistryModel.ReasonCredentialConflict {
		t.Fatalf("availability %+v", av)
	}
	if len(av) != 2 || av[0].Image != "ghcr.io/other/app:1" || av[1].Image != ref {
		t.Fatalf("one result per distinct image, in input order: %+v", av)
	}
}

// The event panel shows conflicts per attachment: the images one attachment deploys together.
func TestEventImagesAvailabilityShowsCredentialConflicts(t *testing.T) {
	f := newAccessFixture(t, true)
	ref, _ := f.platformImage(t)
	priv := newTestRegistry(t, "bob", secretToken)
	f.hosts["ghcr.io"] = priv.host()
	pushImage(t, priv.host(), "other/app", "bob", secretToken, "1")
	attA, attB := uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000a2")), uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000b2"))
	f.q.EXPECT().ListEventExerciseImages(gomock.Any(), eventA).Return([]postgres.ListEventExerciseImagesRow{
		{EventExerciseID: attA, ExerciseName: "Mixed", Images: []string{ref, "ghcr.io/other/app:1"}},
		{EventExerciseID: attB, ExerciseName: "Platform only", Images: []string{ref}},
	}, nil)
	f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
		f.row(t, secID, "ghcr.io", "other", secretToken),
	}, nil)
	got, err := f.uc.EventImagesAvailability(context.Background(), eventA)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].EventExerciseID != attA {
		t.Fatalf("only the mixed attachment conflicts: %+v", got)
	}
	r := reasons(got[0].Unavailable)
	if len(r) != 2 || r[ref] != imageRegistryModel.ReasonCredentialConflict || r["ghcr.io/other/app:1"] != imageRegistryModel.ReasonCredentialConflict {
		t.Fatalf("unavailable %+v", got[0].Unavailable)
	}
}

// The laboratory agent refuses a credential with an empty password or a username with ':'; such a
// credential is never sent: the image is unavailable (unauthorized) and its registry is not asked.
func TestACredentialTheAgentRefusesIsNeverSent(t *testing.T) {
	ctx := context.Background()
	t.Run("username with a colon", func(t *testing.T) {
		f := newAccessFixture(t, true)
		open := newTestRegistry(t, "", "")
		f.hosts["quay.io"] = open.host()
		pushImage(t, open.host(), "acme/app", "", "", "1")
		row := f.row(t, secID, "quay.io", "acme", secretToken)
		row.Username = "bo:b"
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{row}, nil).Times(2)
		got, err := f.uc.PrepareDeployImages(ctx, eventScope(eventA), topo("quay.io/acme/app:1"))
		if !errors.Is(err, imageRegistryModel.ErrImagesUnavailable.Err()) || len(got.Credentials) != 0 {
			t.Fatalf("got %+v %v", got, err)
		}
		if r := reasons(got.Unavailable); r["quay.io/acme/app:1"] != imageRegistryModel.ReasonUnauthorized {
			t.Fatalf("unavailable %+v", got.Unavailable)
		}
		av, err := f.uc.CheckImageAvailability(ctx, eventScope(eventA), []string{"quay.io/acme/app:1"})
		if err != nil || len(av) != 1 || av[0].Reason != imageRegistryModel.ReasonUnauthorized {
			t.Fatalf("check %+v %v", av, err)
		}
		if n := open.headCount(); n != 0 {
			t.Fatalf("the registry was asked with a credential the agent refuses: %d", n)
		}
	})
	// A secret whose token is empty (or does not open) is not used at all: the image is checked
	// anonymously, so a private one is unavailable and no credential is sent.
	t.Run("empty password", func(t *testing.T) {
		f := newAccessFixture(t, true)
		priv := newTestRegistry(t, "bob", secretToken)
		f.hosts["quay.io"] = priv.host()
		pushImage(t, priv.host(), "acme/app", "bob", secretToken, "1")
		f.q.EXPECT().ListEventRegistrySecrets(gomock.Any(), eventA).Return([]postgres.EventRegistrySecret{
			f.row(t, secID, "quay.io", "acme", ""),
		}, nil)
		got, err := f.uc.PrepareDeployImages(ctx, eventScope(eventA), topo("quay.io/acme/app:1"))
		if !errors.Is(err, imageRegistryModel.ErrImagesUnavailable.Err()) || len(got.Credentials) != 0 {
			t.Fatalf("got %+v %v", got, err)
		}
		if r := reasons(got.Unavailable); r["quay.io/acme/app:1"] != imageRegistryModel.ReasonUnauthorized {
			t.Fatalf("unavailable %+v", got.Unavailable)
		}
	})
}
