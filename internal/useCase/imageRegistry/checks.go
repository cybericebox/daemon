package imageRegistryUseCase

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
	"github.com/cybericebox/daemon/pkg/ociregistry"
)

// Alert keys of the platform tokens (an organizer secret's is "event_secret:<id>").
const (
	keyPlatformPush = "platform_push"
	keyPlatformPull = "platform_pull"
	kindEventSecret = "event_secret"
)

func eventSecretKey(id uuid.UUID) string { return kindEventSecret + ":" + id.String() }

// CheckNotifier tells the people who can fix a credential. The check calls it only on a transition: once when a
// credential breaks, once when it works again, and quietly when it ceases to exist. A nil notifier is allowed.
type CheckNotifier interface {
	RegistryCredentialBroken(ctx context.Context, a inboxUseCase.RegistryCredentialAlert) error
	RegistryCredentialRestored(ctx context.Context, a inboxUseCase.RegistryCredentialAlert) error
	RegistryCredentialClosed(ctx context.Context, key string) error
}

// SetCheckNotifier wires the notifier (built after this use case).
func (u *ImageRegistryUseCase) SetCheckNotifier(n CheckNotifier) { u.notifier = n }

type (
	// CheckView is the last check result of a credential as the panels see it: reason codes and times, never a token.
	CheckView struct {
		// State is "ok", "broken" or "" (never checked).
		State string
		// CheckedAt is when State was concluded.
		CheckedAt *time.Time
		// Reason says why a broken credential is broken.
		Reason string
		// AttemptedAt is the last attempt; Failure is why it gave no verdict (it will be retried), "" when it did.
		AttemptedAt *time.Time
		Failure     string
	}
)

func toCheckView(c imageRegistryModel.Check) CheckView {
	v := CheckView{State: string(c.State), Reason: string(c.Reason), Failure: string(c.Failure)}
	if !c.CheckedAt.IsZero() {
		at := c.CheckedAt
		v.CheckedAt = &at
	}
	if !c.AttemptedAt.IsZero() {
		at := c.AttemptedAt
		v.AttemptedAt = &at
	}
	return v
}

// outcomeOf reads a ping error. Only an auth refusal (401/403) is a verdict on the credential; a provider
// error, throttling, a timeout or a network failure says nothing about it and is retried.
func outcomeOf(err error) imageRegistryModel.Outcome {
	switch {
	case err == nil:
		return imageRegistryModel.Working()
	case errors.Is(err, ociregistry.ErrUnauthorized):
		return imageRegistryModel.Refused(imageRegistryModel.ReasonUnauthorized)
	case errors.Is(err, ociregistry.ErrNotFound):
		// a missing repository is a permanent configuration fault, not a passing trouble
		return imageRegistryModel.Refused(imageRegistryModel.ReasonNotFound)
	}
	return imageRegistryModel.Inconclusive(ReasonOf(err))
}

// ping runs one registry call under the per-call timeout.
func (u *ImageRegistryUseCase) ping(ctx context.Context, call func(context.Context) error) imageRegistryModel.Outcome {
	ctx, cancel := context.WithTimeout(ctx, u.callTimeout)
	defer cancel()
	return outcomeOf(call(ctx))
}

// notify sends the alert of a transition. A failure is logged, never raised: the new state is already stored.
func (u *ImageRegistryUseCase) notify(ctx context.Context, t imageRegistryModel.Transition, a inboxUseCase.RegistryCredentialAlert) {
	if u.notifier == nil || t == imageRegistryModel.TransitionNone {
		return
	}
	var err error
	if t == imageRegistryModel.TransitionBroke {
		err = u.notifier.RegistryCredentialBroken(ctx, a)
	} else {
		err = u.notifier.RegistryCredentialRestored(ctx, a)
	}
	if err != nil {
		log.Warn().Err(err).Str("credential", a.Key).Msg("Failed to notify about the registry credential check")
	}
}

func (u *ImageRegistryUseCase) closeAlert(ctx context.Context, key string) {
	if u.notifier == nil {
		return
	}
	if err := u.notifier.RegistryCredentialClosed(ctx, key); err != nil {
		log.Warn().Err(err).Str("credential", key).Msg("Failed to close the registry credential alert")
	}
}

// CheckPlatformRegistry checks the platform push token (Ping) and pull token (PingPull) now, stores the results
// and tells the platform admins about a change. Not configured: ErrRegistryNotConfigured.
func (u *ImageRegistryUseCase) CheckPlatformRegistry(ctx context.Context) (SettingsView, error) {
	s, found, err := u.load(ctx)
	if err != nil {
		return SettingsView{}, err
	}
	if !found || !s.Configured() {
		return SettingsView{}, imageRegistryModel.ErrRegistryNotConfigured.Err()
	}
	if u.cipher == nil {
		return SettingsView{}, imageRegistryModel.ErrSecretsKeyMissing.Err()
	}
	s = u.checkPlatform(ctx, s)
	return u.toView(s, true), nil
}

// checkPlatform runs both token checks on the loaded settings and returns them with the new results (kept as
// they were when the results could not be stored).
func (u *ImageRegistryUseCase) checkPlatform(ctx context.Context, s imageRegistryModel.Settings) imageRegistryModel.Settings {
	now := u.now()
	label := s.Provider.RefHost() + "/" + s.Repository
	next := s
	var pushT, pullT imageRegistryModel.Transition

	if token := u.open(s.PushTokenCiphertext, pushContext); token == "" {
		pushT = next.PushCheck.Apply(imageRegistryModel.Inconclusive(imageRegistryModel.ReasonNotConfigured), now)
	} else {
		client := u.newClient(s.Provider, s.Repository, s.Account, token)
		pushT = next.PushCheck.Apply(u.ping(ctx, client.Ping), now)
	}
	// without a pull token the push token pulls: there is nothing separate to check
	next.PullCheck = imageRegistryModel.Check{}
	if s.PullTokenCiphertext != "" {
		next.PullCheck = s.PullCheck
		if token := u.open(s.PullTokenCiphertext, pullContext); token == "" {
			pullT = next.PullCheck.Apply(imageRegistryModel.Inconclusive(imageRegistryModel.ReasonNotConfigured), now)
		} else {
			client := u.newClient(s.Provider, s.Repository, s.Account, token)
			pullT = next.PullCheck.Apply(u.ping(ctx, client.PingPull), now)
		}
	}

	stored, err := u.settings.SaveChecks(ctx, s.UpdatedAt, next.PushCheck, next.PullCheck)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to store the image registry check results")
		return s
	}
	if !stored {
		// the settings were replaced while the check ran: the result belongs to tokens that are gone
		return s
	}
	alert := func(key string, c imageRegistryModel.Check) inboxUseCase.RegistryCredentialAlert {
		return inboxUseCase.RegistryCredentialAlert{Key: key, Kind: key, Label: label, Reason: string(c.Reason), At: now}
	}
	u.notify(ctx, pushT, alert(keyPlatformPush, next.PushCheck))
	u.notify(ctx, pullT, alert(keyPlatformPull, next.PullCheck))
	return next
}

// CheckEventRegistrySecret checks one organizer secret of the event now (the registry accepts its account),
// stores the result and tells the event's managers about a change.
func (u *ImageRegistryUseCase) CheckEventRegistrySecret(ctx context.Context, eventID, secretID uuid.UUID) (EventSecretView, error) {
	s, err := u.secrets.Get(ctx, eventID, secretID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventSecretView{}, imageRegistryModel.ErrEventSecretNotFound.Err()
		}
		return EventSecretView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load the event registry secret").Err()
	}
	tag, name, err := u.secrets.EventLabel(ctx, eventID)
	if err != nil {
		return EventSecretView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load the event").Err()
	}
	return toEventSecretView(u.checkSecret(ctx, s, tag, name)), nil
}

// checkSecret checks one secret and returns it with the new result (as it was when the result could not be stored).
func (u *ImageRegistryUseCase) checkSecret(ctx context.Context, s imageRegistryModel.EventSecret, eventTag, eventName string) imageRegistryModel.EventSecret {
	now := u.now()
	next := s
	var t imageRegistryModel.Transition
	if token := u.open(s.TokenCiphertext, eventSecretContext(s.ID)); token == "" {
		t = next.Check.Apply(imageRegistryModel.Inconclusive(imageRegistryModel.ReasonNotConfigured), now)
	} else {
		client := u.external(s.Host, s.RepositoryPrefix, s.Username, token)
		t = next.Check.Apply(u.ping(ctx, client.PingCredentials), now)
	}
	stored, err := u.secrets.SaveCheck(ctx, next)
	if err != nil {
		log.Warn().Err(err).Str("secret_id", s.ID.String()).Msg("Failed to store the registry secret check result")
		return s
	}
	if !stored {
		// the secret was edited or removed while the check ran
		return s
	}
	label := s.Host
	if s.RepositoryPrefix != "" {
		label += "/" + s.RepositoryPrefix
	}
	u.notify(ctx, t, inboxUseCase.RegistryCredentialAlert{
		Key: eventSecretKey(s.ID), Kind: kindEventSecret, Label: label, EventID: s.EventID, EventName: eventName, EventTag: eventTag,
		Reason: string(next.Check.Reason), At: now,
	})
	return next
}

// RunRegistryChecks is the periodic pass: the platform tokens and every organizer secret of the events that are
// not finished or archived. It is cheap (authentication only, no image is read). One credential that cannot be
// checked or stored never stops the others; the next pass tries again.
func (u *ImageRegistryUseCase) RunRegistryChecks(ctx context.Context) error {
	if u.cipher == nil {
		return nil // nothing sealed can be opened
	}
	if s, found, err := u.load(ctx); err != nil {
		log.Warn().Err(err).Msg("Failed to load the image registry settings for the credential check")
	} else if found && s.Configured() {
		u.checkPlatform(ctx, s)
	}
	u.closeEndedAlerts(ctx)
	secrets, err := u.secrets.ListToCheck(ctx, u.now())
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list the event registry secrets to check").Err()
	}
	for _, item := range secrets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		u.checkSecret(ctx, item.Secret, item.EventTag, item.EventName)
	}
	return nil
}

// closeEndedAlerts closes, without a note, the open alerts of secrets whose event has finished or been archived,
// and forgets their broken state so the pass does it once.
func (u *ImageRegistryUseCase) closeEndedAlerts(ctx context.Context) {
	ended, err := u.secrets.ListEndedBroken(ctx, u.now())
	if err != nil {
		log.Warn().Err(err).Msg("Failed to list the registry secrets of ended events")
		return
	}
	for _, s := range ended {
		s.Check = imageRegistryModel.Check{}
		stored, err := u.secrets.SaveCheck(ctx, s)
		if err != nil || !stored {
			if err != nil {
				log.Warn().Err(err).Str("secret_id", s.ID.String()).Msg("Failed to clear the registry secret check of an ended event")
			}
			continue
		}
		u.closeAlert(ctx, eventSecretKey(s.ID))
	}
}
