package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	"github.com/cybericebox/daemon/pkg/tools"
)

// temporalCodeLength is the length of a raw code: 32 random bytes, hex-encoded.
const temporalCodeLength = 64

// hashTemporalCode is the stored form of a code: a database leak (or a read
// access to the table) must not hand out working reset / confirmation links.
// The code is 256 bits of randomness, so an unsalted SHA-256 is enough.
func hashTemporalCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// validTemporalCode reports whether code has the exact shape createTemporalCode
// issues (lower-case hex). Anything else — including a NUL byte Postgres cannot
// store in text — is a bad code, never a database error.
func validTemporalCode(code string) bool {
	if len(code) != temporalCodeLength {
		return false
	}
	for i := 0; i < len(code); i++ {
		c := code[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

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
		tools.NewUUIDv7(), hashTemporalCode(code), codeType, dataBytes, time.Now().Add(u.cfg.TemporalCodeTTL),
	)); err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to create temporal code").Err()
	}
	return code, nil
}

// consumeTemporalCode fetches a code by value, validates type + expiry, deletes
// it (single-use), and returns its JSON data. The delete is the claim: of two
// concurrent requests with the same code only the one that removes the row wins.
func (u *AuthUseCase) consumeTemporalCode(ctx context.Context, code string, expectedType int32) (json.RawMessage, error) {
	if !validTemporalCode(code) {
		return nil, temporalCodeModel.ErrTemporalCodeInvalidCode.WithError(errors.New("consume: malformed code")).Err()
	}
	c, err := u.codes.GetByCode(ctx, hashTemporalCode(code))
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
	claimed, err := u.codes.Delete(ctx, c.ID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to delete temporal code").Err()
	}
	if claimed == 0 {
		return nil, temporalCodeModel.ErrTemporalCodeInvalidCode.WithError(errors.New("consume: code claimed by a concurrent request")).Err()
	}
	return c.Data, nil
}
