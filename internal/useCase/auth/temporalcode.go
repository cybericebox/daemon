package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	"github.com/cybericebox/daemon/pkg/tools"
)

// createTemporalCode generates a random opaque code, stores it with the typed
// JSON data and a TTL, and returns the raw code.
func (u *AuthUseCase) createTemporalCode(ctx context.Context, codeType int32, data any) (string, error) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to marshal temporal code data").Err()
	}

	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to generate temporal code").Err()
	}
	code := hex.EncodeToString(buf)

	if err = u.codes.Create(ctx, temporalCodeModel.NewCode(
		tools.NewUUIDv7(), code, codeType, dataBytes, time.Now().Add(u.cfg.TemporalCodeTTL),
	)); err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to create temporal code").Err()
	}
	return code, nil
}

// consumeTemporalCode fetches a code by value, validates type + expiry, deletes
// it (single-use), and returns its JSON data.
func (u *AuthUseCase) consumeTemporalCode(ctx context.Context, code string, expectedType int32) (json.RawMessage, error) {
	c, err := u.codes.GetByCode(ctx, code)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			// Category A: used-or-never-existed must be indistinguishable from
			// any other invalid code — a 404 would leak whether a code was
			// ever issued. The reason goes to server logs only.
			return nil, temporalCodeModel.ErrTemporalCodeInvalidCode.
				WithError(errors.New("consume: code not found (used or never existed)")).Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get temporal code").Err()
	}
	if c.Type != expectedType {
		return nil, temporalCodeModel.ErrTemporalCodeInvalidCode.WithError(errors.New("consume: code type mismatch")).Err()
	}
	if time.Now().After(c.ExpiresAt) {
		return nil, temporalCodeModel.ErrTemporalCodeExpired.Err()
	}
	if _, err = u.codes.Delete(ctx, c.ID); err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to delete temporal code").Err()
	}
	return c.Data, nil
}
