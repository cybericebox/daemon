// Package imageRegistryUseCase owns the platform image registry settings
// («Панель платформи»): the provider, account and repository, and the push and
// pull tokens, stored sealed with the platform cipher and never shown back. It
// also hands the other use cases (image upload, deploy) a ready registry
// client. The backend starts and works without the settings.
package imageRegistryUseCase

import (
	"context"
	"errors"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/imageRegistryRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/ociregistry/provider"
	"github.com/cybericebox/daemon/pkg/secret"
)

// Token sealing contexts: a ciphertext opens only as the token it was sealed as.
const (
	pushContext = "image-registry:push"
	pullContext = "image-registry:pull"
)

type (
	// ClientFactory builds the OCI client for one repository of a provider's registry.
	ClientFactory func(p imageRegistryModel.Provider, repository, username, password string) *ociregistry.Client
	// ExternalClientFactory builds the OCI client for a registry host taken from an image reference (public
	// and organizer images). The default dials through ociregistry.GuardedTransport.
	ExternalClientFactory func(host, repository, username, password string) *ociregistry.Client
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
		// Now defaults to time.Now.
		Now func() time.Time
		// Media reads and drops the chunked uploads of image blobs; QueueBlobPush queues their background push.
		// Nil: asking for a blob push is a platform error.
		Media         MediaSource
		QueueBlobPush BlobPushQueue
		// HeadTimeout bounds one registry request of an availability check (default 10 s);
		// CallTimeout bounds one whole check call (default 30 s).
		HeadTimeout, CallTimeout time.Duration
	}

	ImageRegistryUseCase struct {
		settings *imageRegistryRepo.Repository
		secrets  *imageRegistryRepo.EventSecrets
		images   *imageRegistryRepo.PlatformImages
		media    MediaSource

		blobPushes    *imageRegistryRepo.BlobPushes
		queueBlobPush BlobPushQueue
		cipher        secret.Sealer
		newClient     ClientFactory
		external      ExternalClientFactory
		adapter       AdapterFactory
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
		Transport: ociregistry.GuardedTransport(nil),
	})
}

// DefaultAdapter is the provider's real API.
func DefaultAdapter(p imageRegistryModel.Provider) provider.Adapter {
	return provider.Default(string(p))
}

func New(deps Dependencies) *ImageRegistryUseCase {
	u := &ImageRegistryUseCase{
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
func (u *ImageRegistryUseCase) PlatformRegistryClient(ctx context.Context, push bool) (*ociregistry.Client, imageRegistryModel.Settings, error) {
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
		token = u.open(s.PullTokenCiphertext, pullContext)
	}
	if token == "" {
		token = u.open(s.PushTokenCiphertext, pushContext)
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
