// Package imageRegistryUseCase owns the platform image registry settings
// («Панель платформи»): the provider, account and repository, and the push and
// pull tokens, stored sealed with the platform cipher and never shown back. It
// also hands the other use cases (image upload, deploy) a ready registry
// client. The backend starts and works without the settings.
package imageRegistryUseCase

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/imageRegistryRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/internal/secretctx"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/ociregistry/provider"
	"github.com/cybericebox/daemon/pkg/secret"
)

// Token sealing contexts: a ciphertext opens only as the token it was sealed as.
const (
	PushContext = secretctx.RegistryPush
	PullContext = secretctx.RegistryPull
)

type (
	// ClientFactory builds the OCI client for one repository of a provider's registry.
	ClientFactory func(p imageRegistryModel.Provider, repository, username, password string) *ociregistry.Client
	// ExternalClientFactory builds the OCI client for a registry host taken from an image reference (public
	// and organizer images). The default dials through ociregistry.GuardedTransport.
	ExternalClientFactory func(host, repository, username, password string) *ociregistry.Client
	// OwnClientFactory builds the OCI client for one repository of the platform's own registry.
	OwnClientFactory func(own OwnRegistry, repository string) *ociregistry.Client
	// AdapterFactory returns the provider API adapter (repository listing, tag deletion); nil for an unknown provider.
	AdapterFactory func(p imageRegistryModel.Provider) provider.Adapter

	Dependencies struct {
		Repo imageRegistryRepo.Queries
		// Cipher seals the tokens (PLATFORM_SECRETS_KEY). Nil: tokens cannot be
		// saved and the registry counts as not configured.
		Cipher secret.Sealer
		// NewClient defaults to the provider's real registry host.
		NewClient ClientFactory
		// NewExternalClient defaults to DefaultExternalClient (guarded transport).
		NewExternalClient ExternalClientFactory
		// Adapter defaults to provider.Default.
		Adapter AdapterFactory
		// Own is the platform's own registry (PLATFORM_REGISTRY_*). When it is set, every platform image lives there
		// and the Docker Hub / GHCR settings of the panel no longer apply (they stay readable for migrate-uploads).
		Own OwnRegistry
		// NewOwnClient defaults to DefaultOwnClient (plain http on the cluster address).
		NewOwnClient OwnClientFactory
		// Now defaults to time.Now.
		Now func() time.Time
		// Media reads and drops the chunked uploads of image blobs; QueueBlobPush queues their background push.
		// Nil: asking for a blob push is a platform error.
		Media         MediaSource
		QueueBlobPush BlobPushQueue
		// Configs keeps the start configuration of the platform images (entrypoint, cmd, user, working directory) so a lab with
		// a bootstrap knows what to run after it. Nil: not kept, and a deploy then finds no configuration.
		Configs ImageConfigStore
		// ExternalConfigs keeps the start configuration read from the registry for images by reference to an external
		// registry (no layer is pulled), so a deploy never needs the registry for it. Nil: not kept, and such an image has
		// no known user.
		ExternalConfigs ExternalConfigStore
		// HeadTimeout bounds one registry request of an availability check (default 10 s);
		// CallTimeout bounds one whole check call (default 30 s).
		HeadTimeout, CallTimeout time.Duration
	}

	// ImageConfigStore keeps the start configuration of a platform image by its manifest digest.
	ImageConfigStore interface {
		Save(ctx context.Context, digest string, service exerciseModel.ImageService, now time.Time) error
		Get(ctx context.Context, digests []string) (map[string]exerciseModel.ImageService, error)
	}

	// ExternalConfigStore keeps the start configuration of an external image by normalized reference and credential scope.
	ExternalConfigStore interface {
		Save(ctx context.Context, reference, scope, digest string, service exerciseModel.ImageService, now time.Time) error
		Get(ctx context.Context, references, scopes []string) (map[string]imageRegistryRepo.ExternalImageConfig, error)
	}

	ImageRegistryUseCase struct {
		configs    ImageConfigStore
		extConfigs ExternalConfigStore
		settings   *imageRegistryRepo.Repository
		secrets    *imageRegistryRepo.EventSecrets
		images     *imageRegistryRepo.PlatformImages
		media      MediaSource

		blobPushes    *imageRegistryRepo.BlobPushes
		queueBlobPush BlobPushQueue
		cipher        secret.Sealer
		notifier      CheckNotifier
		newClient     ClientFactory
		external      ExternalClientFactory
		adapter       AdapterFactory
		own           OwnRegistry
		newOwnClient  OwnClientFactory
		now           func() time.Time
		available     *availabilityCache

		headTimeout, callTimeout time.Duration
	}
)

// DefaultClient talks to the provider's public registry host.
func DefaultClient(p imageRegistryModel.Provider, repository, username, password string) *ociregistry.Client {
	return ociregistry.New(ociregistry.Options{Host: p.RegistryHost(), Repository: repository, Username: username, Password: password})
}

// DefaultExternalClient talks to a user-supplied registry host through the
// guarded transport: loopback, private, link-local and unspecified addresses
// are refused at connect time.
func DefaultExternalClient(host, repository, username, password string) *ociregistry.Client {
	return ociregistry.New(ociregistry.Options{
		Host: host, Repository: repository, Username: username, Password: password,
		Transport: ociregistry.GuardedTransport(nil), RequireHTTPS: true,
	})
}

// OwnRegistry is the platform's own registry, the zot of the laboratory chart, as the deployment configures it:
// the in-cluster address, and the platform-writer account that writes (and deletes) under platform/**. It is
// never read from a settings form or an image reference.
type OwnRegistry struct {
	// Host is host:port without a scheme (laboratory-registry.<namespace>.svc:5000). Empty: not in use.
	Host               string
	Username, Password string
}

// Enabled reports whether the platform's own registry is in use.
func (o OwnRegistry) Enabled() bool { return o.Host != "" }

// Validate checks a configured registry; the error never carries the password.
func (o OwnRegistry) Validate() error {
	if !o.Enabled() {
		return nil
	}
	if strings.ContainsAny(o.Host, "/@ \t\r\n") || strings.Contains(o.Host, "://") {
		return errors.New("PLATFORM_REGISTRY_HOST must be host:port, without a scheme or a path")
	}
	// the laboratory agent refuses a username with ':' (it would split the credential)
	if o.Username == "" || o.Password == "" || strings.Contains(o.Username, ":") {
		return errors.New("PLATFORM_REGISTRY_USERNAME and PLATFORM_REGISTRY_PASSWORD must be set (the username has no ':')")
	}
	return nil
}

// DefaultOwnClient talks to the own registry over plain http (the registry is on the cluster network and has no
// TLS). It does not use the guarded transport: the address is the operator's configuration, and it is a cluster
// address by design.
func DefaultOwnClient(own OwnRegistry, repository string) *ociregistry.Client {
	return ociregistry.New(ociregistry.Options{
		Host: own.Host, Repository: repository, Username: own.Username, Password: own.Password, PlainHTTP: true,
	})
}

// DefaultAdapter is the provider's real API.
func DefaultAdapter(p imageRegistryModel.Provider) provider.Adapter {
	return provider.Default(string(p))
}

func New(deps Dependencies) *ImageRegistryUseCase {
	u := &ImageRegistryUseCase{
		configs:       deps.Configs,
		extConfigs:    deps.ExternalConfigs,
		settings:      imageRegistryRepo.New(deps.Repo),
		secrets:       imageRegistryRepo.NewEventSecrets(deps.Repo),
		images:        imageRegistryRepo.NewPlatformImages(deps.Repo),
		media:         deps.Media,
		blobPushes:    imageRegistryRepo.NewBlobPushes(deps.Repo),
		queueBlobPush: deps.QueueBlobPush,
		cipher:        deps.Cipher,
		newClient:     deps.NewClient,
		external:      deps.NewExternalClient,
		adapter:       deps.Adapter,
		own:           deps.Own,
		newOwnClient:  deps.NewOwnClient,
		now:           deps.Now,
		available:     newAvailabilityCache(),
		headTimeout:   deps.HeadTimeout,
		callTimeout:   deps.CallTimeout,
	}
	if u.newClient == nil {
		u.newClient = DefaultClient
	}
	if u.external == nil {
		u.external = DefaultExternalClient
	}
	if u.adapter == nil {
		u.adapter = DefaultAdapter
	}
	if u.newOwnClient == nil {
		u.newOwnClient = DefaultOwnClient
	}
	if u.now == nil {
		u.now = time.Now
	}
	if u.headTimeout <= 0 {
		u.headTimeout = defaultHeadTimeout
	}
	if u.callTimeout <= 0 {
		u.callTimeout = defaultCallTimeout
	}
	return u
}

// PlatformRegistryClient returns a client for the platform repository and the stored
// settings (tokens still sealed). push selects the push token; a pull uses the
// pull token, falling back to the push token when none is set or it cannot be opened. Not configured, or no cipher to
// open the tokens: ErrRegistryNotConfigured.
//
// With the own registry in use this is the client of the staging repository (every blob of an upload waits there
// until its image is created) and the settings are the synthetic own ones; the manifest of an image is read and
// written through imageClient.
func (u *ImageRegistryUseCase) PlatformRegistryClient(ctx context.Context, push bool) (*ociregistry.Client, imageRegistryModel.Settings, error) {
	if u.own.Enabled() {
		return u.ownClient(imageRegistryModel.StagingRepository), u.ownSettings(), nil
	}
	s, token, err := u.platformToken(ctx, push)
	if err != nil {
		return nil, imageRegistryModel.Settings{}, err
	}
	if token == "" {
		return nil, imageRegistryModel.Settings{}, imageRegistryModel.ErrRegistryNotConfigured.Err()
	}
	return u.newClient(s.Provider, s.Repository, s.Account, token), s, nil
}

// RequireImageUploads refuses an image upload before it starts while the platform registry is not configured
// (ErrRegistryNotConfigured, as PlatformRegistryClient): the blobs would be uploaded for nothing.
func (u *ImageRegistryUseCase) RequireImageUploads(ctx context.Context) error {
	_, _, err := u.PlatformRegistryClient(ctx, true)
	return err
}

// platformToken returns the stored settings and the opened token (see
// PlatformRegistryClient); token "" means the registry is not configured.
// The error is only a failure to read the settings.
func (u *ImageRegistryUseCase) platformToken(ctx context.Context, push bool) (imageRegistryModel.Settings, string, error) {
	if u.cipher == nil {
		return imageRegistryModel.Settings{}, "", nil
	}
	s, found, err := u.load(ctx)
	if err != nil || !found || !s.Configured() {
		return imageRegistryModel.Settings{}, "", err
	}
	var token string
	if !push {
		// a pull token that does not open counts as not set: the push token pulls too
		token = u.open(s.PullTokenCiphertext, PullContext)
	}
	if token == "" {
		token = u.open(s.PushTokenCiphertext, PushContext)
	}
	if token == "" {
		return imageRegistryModel.Settings{}, "", nil
	}
	return s, token, nil
}

// load reads the settings; found is false when none are saved.
func (u *ImageRegistryUseCase) load(ctx context.Context) (imageRegistryModel.Settings, bool, error) {
	s, err := u.settings.Get(ctx)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return imageRegistryModel.Settings{}, false, nil
		}
		return imageRegistryModel.Settings{}, false, model.ErrPlatform.WithError(err).WithMessage("Failed to load the image registry settings").Err()
	}
	return s, true, nil
}

// open returns the plaintext token, or "" when it cannot be opened (a key no
// longer in the ring, a copied value): the token then counts as not set.
func (u *ImageRegistryUseCase) open(ciphertext, context string) string {
	if u.cipher == nil || ciphertext == "" {
		return ""
	}
	pt, err := u.cipher.DecryptWithContext(ciphertext, []byte(context))
	if err != nil {
		return ""
	}
	return string(pt)
}

func (u *ImageRegistryUseCase) seal(token, context string) (string, error) {
	ct, err := u.cipher.EncryptWithContext([]byte(token), []byte(context))
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to seal the registry token").Err()
	}
	return ct, nil
}

// ReasonOf classifies a registry or provider failure for admins. It never
// carries the error text (which could name a host or a status, never a token).
func ReasonOf(cause error) imageRegistryModel.Reason {
	switch {
	case errors.Is(cause, ociregistry.ErrUnauthorized):
		return imageRegistryModel.ReasonUnauthorized
	case errors.Is(cause, ociregistry.ErrNotFound):
		return imageRegistryModel.ReasonNotFound
	case errors.Is(cause, ociregistry.ErrRateLimited):
		return imageRegistryModel.ReasonRateLimited
	}
	return imageRegistryModel.ReasonUnreachable
}

// Unavailable is the error for a registry that refused or failed: the upload
// refusal message for everyone, the reason (and which token, when known) in
// the public context for admins. cause stays in server logs only.
func Unavailable(cause error, token string) error {
	e := imageRegistryModel.ErrRegistryUnavailable.WithError(cause).
		WithPublicContext(imageRegistryModel.ContextReason, string(ReasonOf(cause)))
	if token != "" {
		e = e.WithPublicContext(imageRegistryModel.ContextToken, token)
	}
	return e.Err()
}

// RegistryFailure is Unavailable, except that a registry that answered and refused the token (401/403) is
// ErrRegistryAccessDenied: the registry works, the platform token does not. Outages, throttling and other
// failures stay ErrRegistryUnavailable with their reason.
func RegistryFailure(cause error, token string) error {
	if !errors.Is(cause, ociregistry.ErrUnauthorized) {
		return Unavailable(cause, token)
	}
	e := imageRegistryModel.ErrRegistryAccessDenied.WithError(cause).
		WithPublicContext(imageRegistryModel.ContextReason, string(imageRegistryModel.ReasonUnauthorized))
	if token != "" {
		e = e.WithPublicContext(imageRegistryModel.ContextToken, token)
	}
	return e.Err()
}

// ownClient is the client of one repository of the own registry.
func (u *ImageRegistryUseCase) ownClient(repository string) *ociregistry.Client {
	return u.newOwnClient(u.own, repository)
}

// ownSettings are the settings the panel and the checks see when the own registry is in use: nothing stored.
func (u *ImageRegistryUseCase) ownSettings() imageRegistryModel.Settings {
	return imageRegistryModel.Settings{Provider: imageRegistryModel.ProviderOwn, Repository: imageRegistryModel.PlatformRepositoryPrefix + "**"}
}

// imageClient is the client of the repository that holds the image of a platform reference: with the own registry
// platform/<name>, otherwise the one provider repository (PlatformRegistryClient). push selects the push token
// of a provider registry; the own registry has one account.
func (u *ImageRegistryUseCase) imageClient(ctx context.Context, push bool, ref imageRegistryModel.PlatformRef) (*ociregistry.Client, error) {
	if u.own.Enabled() {
		return u.ownClient(ref.Repository()), nil
	}
	client, _, err := u.PlatformRegistryClient(ctx, push)
	return client, err
}

// ownRef is the reference an image is published under. With the own registry a digest lives in one repository, so
// a digest an earlier upload recorded under another name keeps that name (a second repository would hold a copy
// the collector does not know). Without it, ref is returned as it is.
func (u *ImageRegistryUseCase) ownRef(ctx context.Context, ref imageRegistryModel.PlatformRef) (imageRegistryModel.PlatformRef, error) {
	if !u.own.Enabled() {
		return ref, nil
	}
	name, found, err := u.images.NameOfDigest(ctx, ref.Digest)
	if err != nil {
		return ref, model.ErrPlatform.WithError(err).WithMessage("Failed to read the recorded name of the image").Err()
	}
	if !found || name == ref.Name {
		return ref, nil
	}
	return imageRegistryModel.NewPlatformRef(name, ref.Digest)
}
