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
	// AdapterFactory returns the provider API adapter (repository listing, tag deletion); nil for an unknown provider.
	AdapterFactory func(p imageRegistryModel.Provider) provider.Adapter

	Dependencies struct {
		Repo imageRegistryRepo.Queries
		// Cipher seals the tokens (PLATFORM_SECRETS_KEY). Nil: tokens cannot be
		// saved and the registry counts as not configured.
		Cipher secret.Sealer
		// NewClient defaults to the provider's real registry host.
		NewClient ClientFactory
		// Adapter defaults to provider.Default.
		Adapter AdapterFactory
		// Now defaults to time.Now.
		Now func() time.Time
	}

	ImageRegistryUseCase struct {
		settings  *imageRegistryRepo.Repository
		cipher    secret.Sealer
		newClient ClientFactory
		adapter   AdapterFactory
		now       func() time.Time
	}
)

// DefaultClient talks to the provider's public registry host.
func DefaultClient(p imageRegistryModel.Provider, repository, username, password string) *ociregistry.Client {
	return ociregistry.New(ociregistry.Options{Host: p.RegistryHost(), Repository: repository, Username: username, Password: password})
}

// DefaultAdapter is the provider's real API.
func DefaultAdapter(p imageRegistryModel.Provider) provider.Adapter {
	return provider.Default(string(p))
}

func New(deps Dependencies) *ImageRegistryUseCase {
	u := &ImageRegistryUseCase{
		settings:  imageRegistryRepo.New(deps.Repo),
		cipher:    deps.Cipher,
		newClient: deps.NewClient,
		adapter:   deps.Adapter,
		now:       deps.Now,
	}
	if u.newClient == nil {
		u.newClient = DefaultClient
	}
	if u.adapter == nil {
		u.adapter = DefaultAdapter
	}
	if u.now == nil {
		u.now = time.Now
	}
	return u
}

// PlatformRegistryClient returns a client for the platform repository and the stored
// settings (tokens still sealed). push selects the push token; a pull uses the
// pull token, falling back to the push token. Not configured, or no cipher to
// open the tokens: ErrRegistryNotConfigured.
func (u *ImageRegistryUseCase) PlatformRegistryClient(ctx context.Context, push bool) (*ociregistry.Client, imageRegistryModel.Settings, error) {
	var (
		s     imageRegistryModel.Settings
		token string
	)
	if u.cipher != nil {
		var (
			found bool
			err   error
		)
		if s, found, err = u.load(ctx); err != nil {
			return nil, imageRegistryModel.Settings{}, err
		}
		if found && s.Configured() {
			ct, kind := s.PushTokenCiphertext, pushContext
			if !push && s.PullTokenCiphertext != "" {
				ct, kind = s.PullTokenCiphertext, pullContext
			}
			token = u.open(ct, kind)
		}
	}
	if token == "" {
		return nil, imageRegistryModel.Settings{}, imageRegistryModel.ErrRegistryNotConfigured.Err()
	}
	return u.newClient(s.Provider, s.Repository, s.Account, token), s, nil
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
