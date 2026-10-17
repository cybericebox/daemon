package imageRegistryUseCase

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/pkg/ociregistry"
)

// availableFor is how long a positive check is trusted. Negative results are
// never cached: a fixed image must show as fixed on the next check.
const availableFor = 60 * time.Second

type (
	// Scope says whose credentials a check may use. EventID set: that event's
	// organizer secrets (and only that event's); nil: platform and anonymous only
	// (the exercise editor, test labs).
	Scope struct{ EventID *uuid.UUID }

	// DeployImages is what one lab deploy needs from the image registry.
	DeployImages struct {
		// Topology is a copy of the input with platform references rewritten to
		// <registry host>/<repository>@sha256:<digest>; other images unchanged.
		Topology exerciseModel.Topology
		// Credentials are exactly the pull credentials the images need: the
		// platform pull credential when a platform image is present, the event
		// secret(s) matching organizer images present. Nil on a refusal.
		Credentials []infraModel.RegistryCredential
		// Unavailable lists the images that cannot be pulled (one per image).
		Unavailable []imageRegistryModel.Availability
	}

	// EventExerciseImages is one attachment of an event with its unavailable images.
	EventExerciseImages struct {
		EventExerciseID uuid.UUID
		ExerciseID      uuid.UUID
		ExerciseName    string
		Unavailable     []imageRegistryModel.Availability
	}
)

// CheckImageAvailability checks each distinct non-empty image (in the order
// given) with the credentials scope allows.
func (u *ImageRegistryUseCase) CheckImageAvailability(ctx context.Context, scope Scope, images []string) ([]imageRegistryModel.Availability, error) {
	c := u.newImageChecker(scope)
	out := make([]imageRegistryModel.Availability, 0, len(images))
	seen := make(map[string]bool, len(images))
	for _, image := range images {
		if image == "" || seen[image] {
			continue
		}
		seen[image] = true
		r, err := c.check(ctx, image)
		if err != nil {
			return nil, err
		}
		out = append(out, r.availability)
	}
	return out, nil
}

// PrepareDeployImages checks every device image of a lab topology right before
// the deploy (forwarding devices and empty images are skipped), rewrites the
// platform references and collects the pull credentials. Any unavailable image
// refuses the deploy with ErrImagesUnavailable, whose public context "images"
// carries the list (also in DeployImages.Unavailable); nothing else is
// returned then.
func (u *ImageRegistryUseCase) PrepareDeployImages(ctx context.Context, scope Scope, topo exerciseModel.Topology) (DeployImages, error) {
	c := u.newImageChecker(scope)
	out := topo
	out.Devices = slices.Clone(topo.Devices)
	var (
		unavailable []imageRegistryModel.Availability
		creds       []infraModel.RegistryCredential
		credSeen    = map[string]bool{}
		imageSeen   = map[string]bool{}
	)
	for i, d := range out.Devices {
		if d.Type.IsForwarding() || d.Image == "" {
			continue
		}
		r, err := c.check(ctx, d.Image)
		if err != nil {
			return DeployImages{}, err
		}
		if r.availability.Status != imageRegistryModel.StatusAvailable {
			if !imageSeen[d.Image] {
				imageSeen[d.Image] = true
				unavailable = append(unavailable, r.availability)
			}
			continue
		}
		if r.rewritten != "" {
			out.Devices[i].Image = r.rewritten
		}
		if r.credential != nil && !credSeen[r.identity] {
			credSeen[r.identity] = true
			creds = append(creds, *r.credential)
		}
	}
	if len(unavailable) > 0 {
		return DeployImages{Unavailable: unavailable}, imageRegistryModel.ErrImagesUnavailable.
			WithPublicContext(imageRegistryModel.ContextImages, unavailable).Err()
	}
	return DeployImages{Topology: out, Credentials: creds}, nil
}

// EventImagesAvailability checks the images of every active attachment of the
// event (its pinned version, all variants) with the event's own credentials and
// returns the attachments that have unavailable images; none means all can be
// pulled.
func (u *ImageRegistryUseCase) EventImagesAvailability(ctx context.Context, eventID uuid.UUID) ([]EventExerciseImages, error) {
	rows, err := u.settings.EventExerciseImages(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to load the event's exercise images").Err()
	}
	c := u.newImageChecker(Scope{EventID: &eventID})
	out := []EventExerciseImages{}
	for _, row := range rows {
		var unavailable []imageRegistryModel.Availability
		for _, image := range row.Images {
			r, err := c.check(ctx, image)
			if err != nil {
				return nil, err
			}
			if r.availability.Status != imageRegistryModel.StatusAvailable {
				unavailable = append(unavailable, r.availability)
			}
		}
		if len(unavailable) > 0 {
			out = append(out, EventExerciseImages{
				EventExerciseID: row.EventExerciseID, ExerciseID: row.ExerciseID,
				ExerciseName: row.ExerciseName, Unavailable: unavailable,
			})
		}
	}
	return out, nil
}

// imageChecker checks images for one scope; the platform settings and the
// event secrets are read once, on first need.
type imageChecker struct {
	u     *ImageRegistryUseCase
	scope Scope

	platformLoaded bool
	platform       imageRegistryModel.Settings
	platformToken  string

	secretsLoaded bool
	secrets       []imageRegistryModel.EventSecret
	tokens        map[uuid.UUID]string

	results map[string]checkResult
}

type checkResult struct {
	availability imageRegistryModel.Availability
	rewritten    string
	credential   *infraModel.RegistryCredential
	identity     string
}

func (u *ImageRegistryUseCase) newImageChecker(scope Scope) *imageChecker {
	return &imageChecker{u: u, scope: scope, results: map[string]checkResult{}}
}

func unavailable(image string, reason imageRegistryModel.Reason) checkResult {
	return checkResult{availability: imageRegistryModel.Availability{Image: image, Status: imageRegistryModel.StatusUnavailable, Reason: reason}}
}

// check returns the result for one image; the error is only a failure to read
// the settings or the secrets, never a registry answer.
func (c *imageChecker) check(ctx context.Context, image string) (checkResult, error) {
	if r, ok := c.results[image]; ok {
		return r, nil
	}
	r, err := c.checkOnce(ctx, image)
	if err != nil {
		return checkResult{}, err
	}
	c.results[image] = r
	return r, nil
}

func (c *imageChecker) checkOnce(ctx context.Context, image string) (checkResult, error) {
	if strings.HasPrefix(image, "platform:") {
		ref, ok := imageRegistryModel.ParsePlatformRef(image)
		if !ok {
			return unavailable(image, imageRegistryModel.ReasonInvalidReference), nil
		}
		return c.checkPlatform(ctx, image, ref)
	}
	ref, err := imageRegistryModel.ParseImageRef(image)
	if err != nil {
		return unavailable(image, imageRegistryModel.ReasonInvalidReference), nil
	}
	if err = c.loadSecrets(ctx); err != nil {
		return checkResult{}, err
	}
	reference := ref.Digest
	if reference == "" {
		reference = ref.Tag
	}
	if kind, secret := imageRegistryModel.Classify(image, c.secrets); kind == imageRegistryModel.KindOrganizer && secret != nil {
		token := c.tokens[secret.ID]
		r := c.manifest(ctx, image, "secret:"+secret.ID.String(), reference,
			c.u.external(ref.Host, ref.Repository, secret.Username, token), false)
		r.credential = &infraModel.RegistryCredential{Registry: secret.Host, Username: secret.Username, Password: token}
		return r, nil
	}
	return c.manifest(ctx, image, "anon", reference, c.u.external(ref.Host, ref.Repository, "", ""), true), nil
}

func (c *imageChecker) checkPlatform(ctx context.Context, image string, ref imageRegistryModel.PlatformRef) (checkResult, error) {
	if !c.platformLoaded {
		s, token, err := c.u.platformToken(ctx, false)
		if err != nil {
			return checkResult{}, err
		}
		c.platform, c.platformToken, c.platformLoaded = s, token, true
	}
	if c.platformToken == "" {
		return unavailable(image, imageRegistryModel.ReasonNotConfigured), nil
	}
	s := c.platform
	host := s.Provider.RegistryHost()
	identity := "platform:" + string(s.Provider) + "/" + s.Repository
	r := c.manifest(ctx, image, identity, ref.Digest, c.u.newClient(s.Provider, s.Repository, s.Account, c.platformToken), false)
	r.rewritten = host + "/" + s.Repository + "@" + ref.Digest
	r.credential = &infraModel.RegistryCredential{Registry: host, Username: s.Account, Password: c.platformToken}
	return r, nil
}

// manifest asks the registry for the manifest, unless a positive answer for
// the same image and credential identity is younger than availableFor.
// identity names the credential (platform repository, secret id, "anon"),
// never the token.
func (c *imageChecker) manifest(ctx context.Context, image, identity, reference string, client *ociregistry.Client, public bool) checkResult {
	r := checkResult{identity: identity}
	key := cacheKey{image: image, identity: identity}
	now := c.u.now()
	if !c.u.available.fresh(key, now) {
		ok, err := client.ManifestExists(ctx, reference)
		switch {
		case err != nil:
			reason := imageRegistryModel.ReasonUnreachable
			switch {
			case errors.Is(err, ociregistry.ErrUnauthorized):
				reason = imageRegistryModel.ReasonUnauthorized
				if public && c.scope.EventID == nil {
					reason = imageRegistryModel.ReasonNeedsEventSecret
				}
			case errors.Is(err, ociregistry.ErrNotFound):
				reason = imageRegistryModel.ReasonNotFound
			}
			// rate limits and everything else: the registry cannot serve the pull now
			res := unavailable(image, reason)
			res.identity = identity
			return res
		case !ok:
			res := unavailable(image, imageRegistryModel.ReasonNotFound)
			res.identity = identity
			return res
		}
		c.u.available.put(key, now)
	}
	r.availability = imageRegistryModel.Availability{Image: image, Status: imageRegistryModel.StatusAvailable}
	return r
}

func (c *imageChecker) loadSecrets(ctx context.Context) error {
	if c.secretsLoaded {
		return nil
	}
	c.secretsLoaded = true
	if c.scope.EventID == nil {
		return nil
	}
	secrets, tokens, err := c.u.EventRegistrySecretsForDeploy(ctx, *c.scope.EventID)
	if err != nil {
		return err
	}
	c.secrets, c.tokens = secrets, tokens
	return nil
}

type cacheKey struct{ image, identity string }

// availabilityCache holds positive results only, in memory, per process.
type availabilityCache struct {
	mu    sync.Mutex
	until map[cacheKey]time.Time
}

func newAvailabilityCache() *availabilityCache {
	return &availabilityCache{until: map[cacheKey]time.Time{}}
}

func (a *availabilityCache) fresh(k cacheKey, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	until, ok := a.until[k]
	return ok && now.Before(until)
}

func (a *availabilityCache) put(k cacheKey, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, until := range a.until {
		if !now.Before(until) {
			delete(a.until, key)
		}
	}
	a.until[k] = now.Add(availableFor)
}
