package imageRegistryUseCase

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/pkg/ociregistry"
)

const (
	// availableFor is how long a positive check is trusted. Negative results are
	// never cached: a fixed image must show as fixed on the next check.
	availableFor = 60 * time.Second
	// defaultHeadTimeout bounds one registry request; defaultCallTimeout one
	// whole check call. A registry that runs out of time is unreachable.
	defaultHeadTimeout = 10 * time.Second
	defaultCallTimeout = 30 * time.Second
	// parallelChecks is how many images are checked at once.
	parallelChecks = 8
)

type (
	// Scope says whose credentials a check may use. EventID set: that event's
	// organizer secrets (and only that event's); nil: platform and anonymous only.
	Scope struct{ EventID *uuid.UUID }

	// DeployImages is what one lab deploy needs from the image registry.
	DeployImages struct {
		// Topology is a copy of the input with platform references rewritten to
		// <registry host>/<repository>@sha256:<digest>; other images unchanged.
		Topology exerciseModel.Topology
		// Credentials are exactly the pull credentials the images need, one per
		// registry host (imageRegistryModel.CredentialHost, at most
		// MaxDeployCredentials): the platform pull credential when a platform
		// image is present, the event secret(s) matching organizer images
		// present. Nil on a refusal.
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

// ExerciseImageScope is the scope an exercise's images are checked and pulled
// with outside an event (the editor check, test and preview labs): the owner
// event for an event-owned exercise, none for a catalog exercise.
func (u *ImageRegistryUseCase) ExerciseImageScope(ctx context.Context, exerciseID uuid.UUID) (Scope, error) {
	owner, err := u.settings.ExerciseOwnerEvent(ctx, exerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return Scope{}, exerciseModel.ErrExerciseNotFound.Err()
		}
		return Scope{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load the exercise").Err()
	}
	if !owner.Valid {
		return Scope{}, nil
	}
	id := owner.UUID
	return Scope{EventID: &id}, nil
}

// CheckImageAvailability checks each distinct non-empty image (results in the
// order given) with the credentials scope allows.
func (u *ImageRegistryUseCase) CheckImageAvailability(ctx context.Context, scope Scope, images []string) ([]imageRegistryModel.Availability, error) {
	checked, err := u.checkImages(ctx, scope, images)
	if err != nil {
		return nil, err
	}
	results := agentCredentialRules(checked, images)
	out := make([]imageRegistryModel.Availability, 0, len(results))
	seen := make(map[string]bool, len(results))
	for _, image := range images {
		if r, ok := results[image]; ok && !seen[image] {
			seen[image] = true
			out = append(out, r.availability)
		}
	}
	return out, nil
}

// PrepareDeployImages checks every device image of a lab topology right before
// the deploy (forwarding devices and empty images are skipped), rewrites the
// platform references and collects the pull credentials, one per registry
// host. Two different credentials for one host, or more hosts than the agent
// takes, make the images concerned unavailable (credential_conflict). Any
// unavailable image refuses the deploy with ErrImagesUnavailable, whose public
// context "images" carries the list (also in DeployImages.Unavailable);
// nothing else is returned then.
func (u *ImageRegistryUseCase) PrepareDeployImages(ctx context.Context, scope Scope, topo exerciseModel.Topology) (DeployImages, error) {
	var images []string
	for _, d := range topo.Devices {
		if !d.Type.IsForwarding() && d.Image != "" {
			images = append(images, d.Image)
		}
	}
	checked, err := u.checkImages(ctx, scope, images)
	if err != nil {
		return DeployImages{}, err
	}
	results := agentCredentialRules(checked, images)

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
		r := results[d.Image]
		var host string
		if r.credential != nil {
			host = imageRegistryModel.CredentialHost(r.credential.Registry)
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
		if host != "" && !credSeen[host] {
			credSeen[host] = true
			c := *r.credential
			c.Registry = host
			creds = append(creds, c)
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
	var images []string
	for _, row := range rows {
		images = append(images, row.Images...)
	}
	checked, err := u.checkImages(ctx, Scope{EventID: &eventID}, images)
	if err != nil {
		return nil, err
	}
	out := []EventExerciseImages{}
	for _, row := range rows {
		// an attachment's images deploy together: the credential rules apply to them as one set
		results := agentCredentialRules(checked, row.Images)
		var bad []imageRegistryModel.Availability
		for _, image := range row.Images {
			if r, ok := results[image]; ok && r.availability.Status != imageRegistryModel.StatusAvailable {
				bad = append(bad, r.availability)
			}
		}
		if len(bad) > 0 {
			out = append(out, EventExerciseImages{
				EventExerciseID: row.EventExerciseID, ExerciseID: row.ExerciseID,
				ExerciseName: row.ExerciseName, Unavailable: bad,
			})
		}
	}
	return out, nil
}

// agentCredentialRules applies the laboratory agent's credential rules to the
// checked images that are deployed (or shown) together: it takes one credential
// per registry host (imageRegistryModel.CredentialHost) and at most
// MaxDeployCredentials of them. Two different credential identities for one
// host, or more hosts than that, make every available image that needs a
// credential on them unavailable with credential_conflict. Anonymous images
// never count. The result holds the given images only; checked is not changed.
func agentCredentialRules(checked map[string]checkResult, images []string) map[string]checkResult {
	out := make(map[string]checkResult, len(images))
	hosts := map[string]map[string]bool{}
	for _, image := range images {
		r, ok := checked[image]
		if !ok {
			continue
		}
		out[image] = r
		if r.credential != nil && r.availability.Status == imageRegistryModel.StatusAvailable {
			h := imageRegistryModel.CredentialHost(r.credential.Registry)
			if hosts[h] == nil {
				hosts[h] = map[string]bool{}
			}
			hosts[h][r.identity] = true
		}
	}
	tooMany := len(hosts) > imageRegistryModel.MaxDeployCredentials
	for image, r := range out {
		if r.credential == nil || r.availability.Status != imageRegistryModel.StatusAvailable {
			continue
		}
		if tooMany || len(hosts[imageRegistryModel.CredentialHost(r.credential.Registry)]) > 1 {
			out[image] = unavailableResult(image, imageRegistryModel.ReasonCredentialConflict)
		}
	}
	return out
}

// agentRefuses is true for a credential the laboratory agent rejects (it would
// refuse the whole deploy): an empty password or a username with ':'.
func agentRefuses(username, password string) bool {
	return password == "" || strings.Contains(username, ":")
}

// checkImages checks the distinct non-empty images, at most parallelChecks at
// once, within the call timeout. The error is a failure to read the settings
// or the secrets, or the caller's own context error; registry answers
// (including running out of time) are results.
func (u *ImageRegistryUseCase) checkImages(parent context.Context, scope Scope, images []string) (map[string]checkResult, error) {
	ctx, cancel := context.WithTimeout(parent, u.callTimeout)
	defer cancel()

	var distinct []string
	seen := map[string]bool{}
	needPlatform, needSecrets := false, false
	for _, image := range images {
		if image == "" || seen[image] {
			continue
		}
		seen[image] = true
		distinct = append(distinct, image)
		if strings.HasPrefix(image, "platform:") {
			needPlatform = true
		} else {
			needSecrets = true
		}
	}
	c := &imageChecker{u: u, scope: scope}
	if needPlatform {
		s, token, err := u.platformToken(ctx, false)
		if err != nil {
			return nil, contextErr(parent, err)
		}
		c.platform, c.platformToken = s, token
	}
	if needSecrets && scope.EventID != nil {
		secrets, tokens, err := u.EventRegistrySecretsForDeploy(ctx, *scope.EventID)
		if err != nil {
			return nil, contextErr(parent, err)
		}
		c.secrets, c.tokens = secrets, tokens
	}

	results := make([]checkResult, len(distinct))
	sem := make(chan struct{}, parallelChecks)
	var wg sync.WaitGroup
	for i, image := range distinct {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = c.check(ctx, image)
		}()
	}
	wg.Wait()
	if err := parent.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]checkResult, len(distinct))
	for i, image := range distinct {
		out[image] = results[i]
	}
	return out, nil
}

// contextErr prefers the caller's context error over a failure it caused.
func contextErr(parent context.Context, err error) error {
	if perr := parent.Err(); perr != nil {
		return perr
	}
	return err
}

// imageChecker checks images for one scope with the settings and secrets
// read up front; check is safe for concurrent use.
type imageChecker struct {
	u     *ImageRegistryUseCase
	scope Scope

	platform      imageRegistryModel.Settings
	platformToken string

	secrets []imageRegistryModel.EventSecret
	tokens  map[uuid.UUID]string
}

type checkResult struct {
	availability imageRegistryModel.Availability
	rewritten    string
	credential   *infraModel.RegistryCredential
	// identity names the credential (never the token); "" for anonymous.
	identity string
}

func unavailableResult(image string, reason imageRegistryModel.Reason) checkResult {
	return checkResult{availability: imageRegistryModel.Availability{Image: image, Status: imageRegistryModel.StatusUnavailable, Reason: reason}}
}

func (c *imageChecker) check(ctx context.Context, image string) checkResult {
	if strings.HasPrefix(image, "platform:") {
		ref, ok := imageRegistryModel.ParsePlatformRef(image)
		if !ok {
			return unavailableResult(image, imageRegistryModel.ReasonInvalidReference)
		}
		return c.checkPlatform(ctx, image, ref)
	}
	ref, err := imageRegistryModel.ParseImageRef(image)
	if err != nil {
		return unavailableResult(image, imageRegistryModel.ReasonInvalidReference)
	}
	reference := ref.Digest
	if reference == "" {
		reference = ref.Tag
	}
	if kind, secret := imageRegistryModel.Classify(image, c.secrets); kind == imageRegistryModel.KindOrganizer && secret != nil {
		token := c.tokens[secret.ID]
		if agentRefuses(secret.Username, token) {
			return unavailableResult(image, imageRegistryModel.ReasonUnauthorized)
		}
		identity := "secret:" + secret.ID.String() + "@" + strconv.FormatInt(secret.UpdatedAt.UnixNano(), 10)
		r := c.manifest(ctx, image, identity, reference, c.u.external(ref.Host, ref.Repository, secret.Username, token), false)
		r.credential = &infraModel.RegistryCredential{Registry: secret.Host, Username: secret.Username, Password: token}
		return r
	}
	r := c.manifest(ctx, image, "anon", reference, c.u.external(ref.Host, ref.Repository, "", ""), true)
	r.identity = ""
	return r
}

func (c *imageChecker) checkPlatform(ctx context.Context, image string, ref imageRegistryModel.PlatformRef) checkResult {
	if c.platformToken == "" {
		return unavailableResult(image, imageRegistryModel.ReasonNotConfigured)
	}
	s := c.platform
	if agentRefuses(s.Account, c.platformToken) {
		return unavailableResult(image, imageRegistryModel.ReasonUnauthorized)
	}
	host := s.Provider.RegistryHost()
	identity := "platform:" + string(s.Provider) + "/" + s.Account + "/" + s.Repository + "@" + strconv.FormatInt(s.UpdatedAt.UnixNano(), 10)
	r := c.manifest(ctx, image, identity, ref.Digest, c.u.newClient(s.Provider, s.Repository, s.Account, c.platformToken), false)
	r.rewritten = host + "/" + s.Repository + "@" + ref.Digest
	r.credential = &infraModel.RegistryCredential{Registry: host, Username: s.Account, Password: c.platformToken}
	return r
}

// manifest asks the registry for the manifest (within the request timeout),
// unless a positive answer for the same image and credential identity is
// younger than availableFor.
func (c *imageChecker) manifest(ctx context.Context, image, identity, reference string, client *ociregistry.Client, public bool) checkResult {
	key := cacheKey{image: image, identity: identity}
	now := c.u.now()
	if !c.u.available.fresh(key, now) {
		hctx, cancel := context.WithTimeout(ctx, c.u.headTimeout)
		ok, err := client.ManifestExists(hctx, reference)
		cancel()
		var res checkResult
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
			// rate limits, timeouts and everything else: the registry cannot serve the pull now
			res = unavailableResult(image, reason)
		case !ok:
			res = unavailableResult(image, imageRegistryModel.ReasonNotFound)
		}
		if err != nil || !ok {
			res.identity = identity
			return res
		}
		c.u.available.put(key, now)
	}
	return checkResult{identity: identity, availability: imageRegistryModel.Availability{Image: image, Status: imageRegistryModel.StatusAvailable}}
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
