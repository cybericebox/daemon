package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/temporalCodeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	"github.com/cybericebox/daemon/pkg/tools"
)

// ISetupTokenSigner is the part of the token client the setup-link store needs.
type ISetupTokenSigner interface {
	IssueSetupToken(userID uuid.UUID, ttl time.Duration) (signed, id string, expiresAt time.Time, err error)
	ParseSetupTokenID(tokenStr string) (uuid.UUID, string, error)
}

// SetupTokenStore makes setup links (sign-up, Google registration, invitations) single-use and
// revocable without a table of their own: the signed token carries an id, and the hash of that id
// is kept as a temporal code of its own type. A link works only while its row exists; issuing a
// link for a user deletes the user's earlier ones (a re-invite revokes the old link), and
// finishing the setup deletes the row.
type SetupTokenStore struct {
	signer ISetupTokenSigner
	codes  *temporalCodeRepo.Repository
}

func NewSetupTokenStore(codes temporalCodeRepo.Queries, signer ISetupTokenSigner) *SetupTokenStore {
	return &SetupTokenStore{signer: signer, codes: temporalCodeRepo.New(codes)}
}

// GenerateSetupToken issues a setup link token for userID living ttl (<= 0: the default).
func (s *SetupTokenStore) GenerateSetupToken(ctx context.Context, userID uuid.UUID, ttl time.Duration) (string, error) {
	signed, id, expiresAt, err := s.signer.IssueSetupToken(userID, ttl)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to generate setup token").Err()
	}
	if err = s.revoke(ctx, userID); err != nil {
		return "", err
	}
	data, err := json.Marshal(temporalCodeModel.TemporalSetupLinkCodeData{UserID: userID})
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to marshal setup link data").Err()
	}
	if err = s.codes.Create(ctx, temporalCodeModel.NewCode(tools.NewUUIDv7(), hashTemporalCode(id), temporalCodeModel.SetupLinkCodeType, data, expiresAt)); err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to store the setup link").Err()
	}
	return signed, nil
}

// Verify resolves a setup token to its user: the signature and expiry must hold AND the link must
// still be live (not replaced, not used).
func (s *SetupTokenStore) Verify(ctx context.Context, token string) (uuid.UUID, error) {
	userID, id, err := s.signer.ParseSetupTokenID(token)
	if err != nil {
		return uuid.Nil, authModel.ErrInvalidToken.WithError(fmt.Errorf("setup token: %w", err)).Err()
	}
	code, err := s.codes.GetByCode(ctx, hashTemporalCode(id))
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return uuid.Nil, authModel.ErrInvalidToken.WithError(errors.New("setup token: link revoked, used or never issued")).Err()
		}
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to read the setup link").Err()
	}
	if code.Type != temporalCodeModel.SetupLinkCodeType || time.Now().After(code.ExpiresAt) {
		return uuid.Nil, authModel.ErrInvalidToken.WithError(errors.New("setup token: not a live setup link")).Err()
	}
	return userID, nil
}

// Revoke kills every live setup link of the user (the setup is finished, or a new link replaces them).
func (s *SetupTokenStore) Revoke(ctx context.Context, userID uuid.UUID) error {
	return s.revoke(ctx, userID)
}

func (s *SetupTokenStore) revoke(ctx context.Context, userID uuid.UUID) error {
	if _, err := s.codes.DeleteForUser(ctx, temporalCodeModel.SetupLinkCodeType, userID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to revoke the setup links").Err()
	}
	return nil
}
