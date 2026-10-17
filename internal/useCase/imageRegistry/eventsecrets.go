package imageRegistryUseCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
)

// eventSecretContext binds a sealed token to its secret: a ciphertext copied
// to another secret does not open.
func eventSecretContext(id uuid.UUID) string { return "event-registry:" + id.String() }

type (
	// EventSecretView is an organizer registry secret as «Панель заходу» sees
	// it. It never carries a token.
	EventSecretView struct {
		ID               string
		Host             string
		RepositoryPrefix string
		Username         string
		UpdatedAt        time.Time
	}

	// EventSecretInput is the secret form. On update an empty Token keeps the
	// stored one.
	EventSecretInput struct {
		Host, RepositoryPrefix, Username, Token string
	}
)

func toEventSecretView(s imageRegistryModel.EventSecret) EventSecretView {
	return EventSecretView{
		ID: s.ID.String(), Host: s.Host, RepositoryPrefix: s.RepositoryPrefix, Username: s.Username, UpdatedAt: s.UpdatedAt,
	}
}

// duplicateEventSecret maps the (event, host, prefix) unique violation to its
// one domain error (a single call site for both create and update).
func duplicateEventSecret(err error) (error, bool) {
	if _, ok := repositoryTools.UniqueViolationError(err, imageRegistryModel.ErrDuplicateEventSecret); ok {
		return imageRegistryModel.ErrDuplicateEventSecret.Err(), true
	}
	return nil, false
}

// ListEventRegistrySecrets lists the event's secrets without tokens.
func (u *ImageRegistryUseCase) ListEventRegistrySecrets(ctx context.Context, eventID uuid.UUID) ([]EventSecretView, error) {
	secrets, err := u.secrets.List(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list the event registry secrets").Err()
	}
	views := make([]EventSecretView, 0, len(secrets))
	for _, s := range secrets {
		views = append(views, toEventSecretView(s))
	}
	return views, nil
}

// CreateEventRegistrySecret validates and stores a secret with its token
// sealed. The token is required; without the platform cipher it is refused.
func (u *ImageRegistryUseCase) CreateEventRegistrySecret(ctx context.Context, eventID uuid.UUID, in EventSecretInput) (EventSecretView, error) {
	if u.cipher == nil {
		return EventSecretView{}, imageRegistryModel.ErrEventSecretKeyMissing.Err()
	}
	s, err := imageRegistryModel.NewEventSecret(eventID.String(), in.Host, in.RepositoryPrefix, in.Username, u.now())
	if err != nil {
		return EventSecretView{}, err
	}
	if in.Token == "" {
		return EventSecretView{}, imageRegistryModel.ErrEventSecretTokenMissing.Err()
	}
	ct, err := u.seal(in.Token, eventSecretContext(s.ID))
	if err != nil {
		return EventSecretView{}, err
	}
	s.SetToken(ct, s.CreatedAt)
	if err = u.secrets.Create(ctx, s); err != nil {
		if dup, ok := duplicateEventSecret(err); ok {
			return EventSecretView{}, dup
		}
		return EventSecretView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save the event registry secret").Err()
	}
	return toEventSecretView(s), nil
}

// UpdateEventRegistrySecret edits a secret of this event. An empty Token keeps
// the stored one (no cipher needed); a new one is sealed again.
func (u *ImageRegistryUseCase) UpdateEventRegistrySecret(ctx context.Context, eventID, secretID uuid.UUID, in EventSecretInput) (EventSecretView, error) {
	if in.Token != "" && u.cipher == nil {
		return EventSecretView{}, imageRegistryModel.ErrEventSecretTokenKeyMissing.Err()
	}
	s, err := u.secrets.Get(ctx, eventID, secretID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventSecretView{}, imageRegistryModel.ErrEventSecretNotFound.Err()
		}
		return EventSecretView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load the event registry secret").Err()
	}
	now := u.now()
	oldHost := s.Host
	if err = s.Edit(in.Host, in.RepositoryPrefix, in.Username, now); err != nil {
		return EventSecretView{}, err
	}
	if s.Host != oldHost && in.Token == "" {
		// A stored token must not be repointed at another registry host.
		return EventSecretView{}, imageRegistryModel.ErrEventSecretTokenRequired.Err()
	}
	if in.Token != "" {
		ct, sealErr := u.seal(in.Token, eventSecretContext(s.ID))
		if sealErr != nil {
			return EventSecretView{}, sealErr
		}
		s.SetToken(ct, now)
	}
	found, err := u.secrets.Update(ctx, s)
	if err != nil {
		if dup, ok := duplicateEventSecret(err); ok {
			return EventSecretView{}, dup
		}
		return EventSecretView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save the event registry secret").Err()
	}
	if !found {
		return EventSecretView{}, imageRegistryModel.ErrEventSecretNotFound.Err()
	}
	return toEventSecretView(s), nil
}

// DeleteEventRegistrySecret removes a secret of this event.
func (u *ImageRegistryUseCase) DeleteEventRegistrySecret(ctx context.Context, eventID, secretID uuid.UUID) error {
	found, err := u.secrets.Delete(ctx, eventID, secretID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete the event registry secret").Err()
	}
	if !found {
		return imageRegistryModel.ErrEventSecretNotFound.Err()
	}
	return nil
}

// EventRegistrySecretsForDeploy is the internal accessor of the deploy path:
// the event's secrets and their plaintext tokens keyed by secret id. A secret
// whose token cannot be opened (no cipher, rotated key, copied value) is left
// out of both results. The tokens must never leave the deploy path.
func (u *ImageRegistryUseCase) EventRegistrySecretsForDeploy(ctx context.Context, eventID uuid.UUID) ([]imageRegistryModel.EventSecret, map[uuid.UUID]string, error) {
	all, err := u.secrets.List(ctx, eventID)
	if err != nil {
		return nil, nil, model.ErrPlatform.WithError(err).WithMessage("Failed to load the event registry secrets").Err()
	}
	secrets := make([]imageRegistryModel.EventSecret, 0, len(all))
	tokens := make(map[uuid.UUID]string, len(all))
	for _, s := range all {
		token := u.open(s.TokenCiphertext, eventSecretContext(s.ID))
		if token == "" {
			continue
		}
		secrets = append(secrets, s)
		tokens[s.ID] = token
	}
	return secrets, tokens, nil
}
