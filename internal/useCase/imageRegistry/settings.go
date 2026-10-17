package imageRegistryUseCase

import (
	"context"
	"strings"
	"time"

	"github.com/cybericebox/daemon/internal/model"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
)

type (
	// SettingsView is what the platform panel sees. It never carries a token,
	// only whether one is stored.
	SettingsView struct {
		Configured   bool
		Provider     string
		Account      string
		Repository   string
		PushTokenSet bool
		PullTokenSet bool
		// SecretsKeyMissing: PLATFORM_SECRETS_KEY is unset, so stored tokens
		// cannot be opened and new ones cannot be saved.
		SecretsKeyMissing bool
		UpdatedAt         *time.Time
	}

	// SaveSettingsInput: an empty token keeps the stored one (when the provider
	// and account stay the same); ClearPullToken removes the pull token.
	SaveSettingsInput struct {
		Provider, Account, Repository string
		PushToken, PullToken          string
		ClearPullToken                bool
	}

	// ListRepositoriesInput: an empty Token uses the stored push token when the
	// provider and account are the saved ones.
	ListRepositoriesInput struct {
		Provider, Account, Token string
	}
)

// toView reports a token as set only when it can be opened (cipher present,
// ciphertext opens), so the panel never claims a token the platform cannot use.
func (u *ImageRegistryUseCase) toView(s imageRegistryModel.Settings, found bool) SettingsView {
	if !found {
		return SettingsView{SecretsKeyMissing: u.cipher == nil}
	}
	at := s.UpdatedAt
	pushSet := u.open(s.PushTokenCiphertext, pushContext) != ""
	return SettingsView{
		Configured:        s.Configured() && pushSet,
		Provider:          string(s.Provider),
		Account:           s.Account,
		Repository:        s.Repository,
		PushTokenSet:      pushSet,
		PullTokenSet:      u.open(s.PullTokenCiphertext, pullContext) != "",
		SecretsKeyMissing: u.cipher == nil,
		UpdatedAt:         &at,
	}
}

// GetImageRegistrySettings returns the saved settings without tokens; none saved: not configured.
func (u *ImageRegistryUseCase) GetImageRegistrySettings(ctx context.Context) (SettingsView, error) {
	s, found, err := u.load(ctx)
	if err != nil {
		return SettingsView{}, err
	}
	return u.toView(s, found), nil
}

// SaveImageRegistrySettings validates the settings, proves the push token may push to the
// repository (and the pull token may pull) and stores them with the tokens
// sealed. Nothing is stored when a check fails.
func (u *ImageRegistryUseCase) SaveImageRegistrySettings(ctx context.Context, in SaveSettingsInput) (SettingsView, error) {
	if u.cipher == nil {
		return SettingsView{}, imageRegistryModel.ErrSecretsKeyMissing.Err()
	}
	if in.PullToken != "" && in.ClearPullToken {
		return SettingsView{}, imageRegistryModel.ErrConflictingPullToken.Err()
	}
	now := u.now()
	next, err := imageRegistryModel.NewSettings(in.Provider, in.Account, in.Repository, now)
	if err != nil {
		return SettingsView{}, err
	}
	prev, found, err := u.load(ctx)
	if err != nil {
		return SettingsView{}, err
	}
	sameAccount := found && prev.SameAccount(next.Provider, next.Account)

	push, err := u.pushToken(prev, sameAccount, in.PushToken)
	if err != nil {
		return SettingsView{}, err
	}
	if err := u.newClient(next.Provider, next.Repository, next.Account, push).Ping(ctx); err != nil {
		return SettingsView{}, Unavailable(err, "push")
	}
	// The pull token to verify: the new one, else the kept one (it must still
	// read the possibly new repository). A kept one that no longer opens is dropped.
	pull, dropKeptPull := in.PullToken, false
	if pull == "" && !in.ClearPullToken && sameAccount && prev.PullTokenCiphertext != "" {
		pull = u.open(prev.PullTokenCiphertext, pullContext)
		dropKeptPull = pull == ""
	}
	if pull != "" {
		if err := u.newClient(next.Provider, next.Repository, next.Account, pull).PingPull(ctx); err != nil {
			return SettingsView{}, Unavailable(err, "pull")
		}
	}

	if sameAccount {
		next.SetTokens(prev.PushTokenCiphertext, prev.PullTokenCiphertext, now)
	}
	var pushCT, pullCT string
	if in.PushToken != "" {
		if pushCT, err = u.seal(in.PushToken, pushContext); err != nil {
			return SettingsView{}, err
		}
	}
	if in.PullToken != "" {
		if pullCT, err = u.seal(in.PullToken, pullContext); err != nil {
			return SettingsView{}, err
		}
	}
	next.SetTokens(pushCT, pullCT, now)
	if in.ClearPullToken || dropKeptPull {
		next.ClearPullToken(now)
	}

	if err := u.settings.Save(ctx, next); err != nil {
		return SettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save the image registry settings").Err()
	}
	return u.toView(next, true), nil
}

// ListImageRegistryRepositories lists the repositories the token can see on the provider,
// for the admin to pick one.
func (u *ImageRegistryUseCase) ListImageRegistryRepositories(ctx context.Context, in ListRepositoriesInput) ([]string, error) {
	p := imageRegistryModel.Provider(strings.TrimSpace(in.Provider))
	account := strings.TrimSpace(in.Account)
	adapter := u.adapter(p)
	if !p.Valid() || account == "" || adapter == nil {
		return nil, imageRegistryModel.ErrInvalidRepositoryListing.Err()
	}
	var prev imageRegistryModel.Settings
	found := false
	if in.Token == "" {
		var err error
		if prev, found, err = u.load(ctx); err != nil {
			return nil, err
		}
	}
	token, err := u.pushToken(prev, found && prev.SameAccount(p, account), in.Token)
	if err != nil {
		return nil, err
	}
	repos, err := adapter.ListRepositories(ctx, account, token)
	if err != nil {
		return nil, Unavailable(err, "")
	}
	if repos == nil {
		repos = []string{}
	}
	return repos, nil
}

// ClearImageRegistrySettings removes the settings and the stored tokens.
func (u *ImageRegistryUseCase) ClearImageRegistrySettings(ctx context.Context) error {
	if err := u.settings.Delete(ctx); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to clear the image registry settings").Err()
	}
	return nil
}

// pushToken is the token to use: the given one, else the stored push token of
// the same provider account; none: ErrPushTokenRequired.
func (u *ImageRegistryUseCase) pushToken(prev imageRegistryModel.Settings, sameAccount bool, given string) (string, error) {
	if given != "" {
		return given, nil
	}
	if sameAccount {
		if t := u.open(prev.PushTokenCiphertext, pushContext); t != "" {
			return t, nil
		}
	}
	return "", imageRegistryModel.ErrPushTokenRequired.Err()
}
