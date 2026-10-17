package event_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

// fakeEventImages stands in for the image registry: listed images are unavailable, the others are
// rewritten (rewrite) and need the credential listed for them.
type fakeEventImages struct {
	mu          sync.Mutex
	unavailable map[string]imageRegistryModel.Reason
	rewrite     map[string]string
	creds       map[string]infraModel.RegistryCredential
	scopes      []imageRegistryUseCase.Scope
	checked     map[string]int // calls per image (prewarm also checks the event's images)
}

func (f *fakeEventImages) PrepareDeployImages(_ context.Context, scope imageRegistryUseCase.Scope, topo exerciseModel.Topology) (imageRegistryUseCase.DeployImages, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopes = append(f.scopes, scope)
	if f.checked == nil {
		f.checked = map[string]int{}
	}
	for _, d := range topo.Devices {
		f.checked[d.Image]++
	}
	var bad []imageRegistryModel.Availability
	out := topo
	out.Devices = slices.Clone(topo.Devices)
	var creds []infraModel.RegistryCredential
	for i, d := range out.Devices {
		if d.Image == "" {
			continue
		}
		if reason, ok := f.unavailable[d.Image]; ok {
			bad = append(bad, imageRegistryModel.Availability{Image: d.Image, Status: imageRegistryModel.StatusUnavailable, Reason: reason})
			continue
		}
		if c, ok := f.creds[d.Image]; ok && !slices.Contains(creds, c) {
			creds = append(creds, c)
		}
		if to, ok := f.rewrite[d.Image]; ok {
			out.Devices[i].Image = to
		}
	}
	if len(bad) > 0 {
		return imageRegistryUseCase.DeployImages{Unavailable: bad}, imageRegistryModel.ErrImagesUnavailable.
			WithPublicContext(imageRegistryModel.ContextImages, bad).Err()
	}
	return imageRegistryUseCase.DeployImages{Topology: out, Credentials: creds}, nil
}

const standPlatformImage = "platform:web@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

var standPull = infraModel.RegistryCredential{Registry: "ghcr.io", Username: "acme", Password: "pull-token-fixed"}

// standWithImage makes the infrastructure task's device use image.
func (f *standFixture) standWithImage(t *testing.T, image string) {
	t.Helper()
	var versionID uuid.UUID
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT ee.exercise_version_id FROM event_challenges c
		JOIN event_exercises ee ON ee.id = c.event_exercise_id WHERE c.id = $1`, f.infraOne).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	f.sets.topology[versionID] = exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: f.infraDevice, Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: image}}}
}

// A stand Lab with an unavailable image is failed with the reason before anything is deployed.
func TestStandEngine_UnavailableImageFailsTheLabWithoutADeploy(t *testing.T) {
	f := newStandFixture(t)
	f.standWithImage(t, standPlatformImage)
	images := &fakeEventImages{unavailable: map[string]imageRegistryModel.Reason{standPlatformImage: imageRegistryModel.ReasonNotFound}}
	f.uc.SetImageAccess(images)
	f.pass(t)
	f.pass(t)
	if len(f.agent.deployed) != 0 {
		t.Fatalf("nothing may be deployed: %v", f.agent.deployed)
	}
	f.assertStatuses(t, map[string]string{"Blue": "failed", "Red": "failed"})
	view, err := f.uc.GetEventStands(context.Background(), f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	want := eventStandModel.ImageUnavailableReason(standPlatformImage)
	for _, item := range view.Items {
		if item.Reason != want {
			t.Fatalf("stand %s reason %q, want %q", item.TeamName, item.Reason, want)
		}
	}
	for _, scope := range images.scopes {
		if scope.EventID == nil || *scope.EventID != f.eventID {
			t.Fatalf("a stand pulls with its event's scope: %+v", scope)
		}
	}
}

// An available image: the Lab gets the rewritten topology and exactly the credentials it needs.
func TestStandEngine_DeploysWithTheRewrittenImageAndItsCredentials(t *testing.T) {
	f := newStandFixture(t)
	f.standWithImage(t, standPlatformImage)
	rewritten := "ghcr.io/acme/tasks@sha256:" + strings.Repeat("a", 64)
	images := &fakeEventImages{rewrite: map[string]string{standPlatformImage: rewritten}, creds: map[string]infraModel.RegistryCredential{standPlatformImage: standPull}}
	f.uc.SetImageAccess(images)
	f.pass(t)
	if len(f.agent.deployed) != 3 {
		t.Fatalf("deployed %v", f.agent.deployed)
	}
	if n := images.checked[standPlatformImage]; n != 1 {
		t.Fatalf("three Labs of one variant must check their images once per pass, got %d checks", n)
	}
	for _, lab := range f.agent.deployed {
		topo, meta := f.agent.topos[lab], f.agent.metas[lab]
		if topo.Devices[0].Image != rewritten {
			t.Fatalf("lab %s image %q", lab, topo.Devices[0].Image)
		}
		if len(topo.Devices[0].EnvVars) == 0 {
			t.Fatalf("lab %s lost its team flag: %+v", lab, topo.Devices[0])
		}
		if len(meta.PullCredentials) != 1 || meta.PullCredentials[0] != standPull {
			t.Fatalf("lab %s credentials %v", lab, meta.PullCredentials)
		}
	}
}

// Prewarm skips the unavailable images and forwards the credentials of the others.
func TestStandEngine_PrewarmSkipsUnavailableImagesAndForwardsCredentials(t *testing.T) {
	f := newStandFixture(t)
	hub := infraModel.RegistryCredential{Registry: "index.docker.io", Username: "bob", Password: "hub-token-fixed"}
	images := &fakeEventImages{
		unavailable: map[string]imageRegistryModel.Reason{"postgres:16": imageRegistryModel.ReasonUnauthorized},
		creds:       map[string]infraModel.RegistryCredential{"nginx:1": hub},
	}
	f.uc.SetImageAccess(images)
	f.shiftLifecycle(t, 40*time.Minute, 3*time.Hour)
	f.pass(t)
	if len(f.agent.prewarmCalls) != 1 || strings.Join(f.agent.prewarmCalls[0], ",") != "nginx:1" {
		t.Fatalf("prewarm calls = %v, want the available image only", f.agent.prewarmCalls)
	}
	if len(f.agent.prewarmCreds[0]) != 1 || f.agent.prewarmCreds[0][0] != hub {
		t.Fatalf("prewarm credentials = %v", f.agent.prewarmCreds[0])
	}
	for _, scope := range images.scopes {
		if scope.EventID == nil || *scope.EventID != f.eventID {
			t.Fatalf("prewarm pulls with its event's scope: %+v", scope)
		}
	}
	view, err := f.uc.GetEventStands(context.Background(), f.eventID)
	if err != nil || view.Prewarm == nil || view.Prewarm.Total != 2 || view.Prewarm.Warming != 1 || view.Prewarm.Unavailable != 1 || view.Prewarm.Skipped != 0 {
		t.Fatalf("prewarm summary = %+v, %v", view.Prewarm, err)
	}
}

// Transient registry trouble (unreachable, rate limited, not configured) keeps the Lab pending, with
// nothing deployed, until the deploy timeout; then it fails with the image reason.
func TestStandEngine_TransientImageTroubleWaitsUntilTheDeployTimeout(t *testing.T) {
	f := newStandFixture(t)
	f.standWithImage(t, standPlatformImage)
	images := &fakeEventImages{unavailable: map[string]imageRegistryModel.Reason{standPlatformImage: imageRegistryModel.ReasonUnreachable}}
	f.uc.SetImageAccess(images)
	f.pass(t)
	f.pass(t)
	if len(f.agent.deployed) != 0 {
		t.Fatalf("nothing may be deployed: %v", f.agent.deployed)
	}
	f.assertStatuses(t, map[string]string{"Blue": "creating", "Red": "creating"})

	timeout := eventStandModel.DeployTimeout
	eventStandModel.DeployTimeout = 0
	t.Cleanup(func() { eventStandModel.DeployTimeout = timeout })
	f.pass(t)
	f.pass(t)
	if len(f.agent.deployed) != 0 {
		t.Fatalf("nothing may be deployed: %v", f.agent.deployed)
	}
	f.assertStatuses(t, map[string]string{"Blue": "failed", "Red": "failed"})
	view, err := f.uc.GetEventStands(context.Background(), f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range view.Items {
		if item.Reason != eventStandModel.ImageUnavailableReason(standPlatformImage) {
			t.Fatalf("stand %s reason %q", item.TeamName, item.Reason)
		}
	}
}

// A Lab held for transient image trouble deploys nothing, so it does not use the pass's deploy budget:
// with a budget of 3 and 3 held Labs among the pending ones (listed before or between them), the 3 Labs
// of a healthy task still deploy in the same pass.
func TestStandEngine_HeldLabsDoNotUseTheDeployBudget(t *testing.T) {
	f := newStandFixture(t)
	f.standWithImage(t, standPlatformImage)
	healthy := f.attach(t, true)
	var versionID uuid.UUID
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT ee.exercise_version_id FROM event_challenges c
		JOIN event_exercises ee ON ee.id = c.event_exercise_id WHERE c.id = $1`, healthy).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	f.sets.topology[versionID] = exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: f.infraDevice, Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx:1"}}}
	f.sets.links[versionID] = []exerciseModel.FlagLink{{TaskID: f.infraTaskID, DeviceID: f.infraDevice, Var: "TASK_FLAG"}}
	f.uc = f.newUseCase(3)
	images := &fakeEventImages{unavailable: map[string]imageRegistryModel.Reason{standPlatformImage: imageRegistryModel.ReasonUnreachable}}
	f.uc.SetImageAccess(images)
	f.pass(t)
	var nginx int
	for _, lab := range f.agent.deployed {
		if f.agent.topos[lab].Devices[0].Image == "nginx:1" {
			nginx++
		} else {
			t.Fatalf("a held Lab was deployed: %s", lab)
		}
	}
	if nginx != 3 {
		t.Fatalf("deployed %d Labs of the healthy task, want 3: %v", nginx, f.agent.deployed)
	}
}
