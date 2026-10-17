package event

import (
	"context"
	"sync"
	"time"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

// ImageAccess checks a lab's images right before its deploy (or prewarm) and
// gives the topology to send (platform references rewritten) with exactly the
// pull credentials it needs (satisfied by imageRegistryUseCase.ImageRegistryUseCase).
type ImageAccess interface {
	PrepareDeployImages(ctx context.Context, scope imageRegistryUseCase.Scope, topo exerciseModel.Topology) (imageRegistryUseCase.DeployImages, error)
}

// SetImageAccess wires the image registry; nil (narrow constructions) deploys
// and prewarms the topology as stored, without checks or credentials.
func (u *EventUseCase) SetImageAccess(a ImageAccess) { u.images = a }

// prepareEventLabImages checks the images of one of the event's labs with the
// event's own scope (its organizer secrets, the platform registry, anonymous).
// An unavailable image returns ErrImagesUnavailable with the list in
// DeployImages.Unavailable.
func (u *EventUseCase) prepareEventLabImages(ctx context.Context, eventID uuid.UUID, topo exerciseModel.Topology) (imageRegistryUseCase.DeployImages, error) {
	if u.images == nil {
		return imageRegistryUseCase.DeployImages{Topology: topo}, nil
	}
	id := eventID
	return u.images.PrepareDeployImages(ctx, imageRegistryUseCase.Scope{EventID: &id}, topo)
}

// standImages is one variant's image check within a stand pass (shared by every team that deploys it).
type standImages struct {
	imageRegistryUseCase.DeployImages
	err error
}

// blockingImage names the image a refused Lab reports: the first one with a definitive reason, else the
// first one; transient is true when every reason may clear by itself.
func blockingImage(unavailable []imageRegistryModel.Availability) (image string, transient bool) {
	for _, a := range unavailable {
		if !a.Reason.Transient() {
			return a.Image, false
		}
	}
	return unavailable[0].Image, true
}

// imageWaitTTL drops a wait the engine no longer asks about (the Lab was removed or the event ended).
const imageWaitTTL = 24 * time.Hour

// imageWaits remembers, per Lab (binding and Lab name, so a recreate starts over), the first pass whose
// deploy was held back by transient image trouble. In memory: a restart starts the wait over, which only
// delays the failure.
type imageWaits struct {
	mu    sync.Mutex
	first map[string]time.Time
}

func newImageWaits() *imageWaits { return &imageWaits{first: map[string]time.Time{}} }

// since is the start of the Lab's wait, starting it now when there is none.
func (w *imageWaits) since(key string, now time.Time) time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	if at, ok := w.first[key]; ok {
		return at
	}
	for k, at := range w.first {
		if now.Sub(at) > imageWaitTTL {
			delete(w.first, k)
		}
	}
	w.first[key] = now
	return now
}

func (w *imageWaits) done(key string) {
	w.mu.Lock()
	delete(w.first, key)
	w.mu.Unlock()
}
