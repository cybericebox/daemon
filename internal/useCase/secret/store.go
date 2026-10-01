// Package secretUseCase owns envelope persistence and the narrow
// decrypt-and-use boundary. Plaintext is never returned to callers for later
// storage; it is available only within WithPlaintext's callback.
package secretUseCase

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	secretModel "github.com/cybericebox/daemon/internal/model/secret"
	pkgSecret "github.com/cybericebox/daemon/pkg/secret"
	"github.com/cybericebox/daemon/pkg/tools"
)

var (
	ErrPurposeNotConfigured = errors.New("secret: purpose key is not configured")
	ErrExpired              = errors.New("secret: envelope has expired")
)

type Repository interface {
	Create(context.Context, secretModel.Envelope) error
	Get(context.Context, uuid.UUID) (secretModel.Envelope, error)
}

// Keyring supplies distinct master sealers for each data class.
type Keyring interface {
	Sealer(secretModel.KeyPurpose) (pkgSecret.Sealer, bool)
}

// StaticKeyring is the production-wiring-friendly implementation; key
// rotation can replace it with a version-aware keyring without changing Store.
type StaticKeyring map[secretModel.KeyPurpose]pkgSecret.Sealer

func (r StaticKeyring) Sealer(purpose secretModel.KeyPurpose) (pkgSecret.Sealer, bool) {
	sealer, ok := r[purpose]
	return sealer, ok
}

type SealInput struct {
	Purpose         secretModel.KeyPurpose
	SignalType      string
	FieldPath       string
	ScopeEventID    uuid.UUID
	RecipientUserID *uuid.UUID
	ExpiresAt       *time.Time
	Plaintext       []byte
}

type Store struct {
	repo    Repository
	keyring Keyring
	now     func() time.Time
}

func NewStore(repo Repository, keyring Keyring) *Store {
	return &Store{repo: repo, keyring: keyring, now: time.Now}
}

// Seal encrypts with a fresh AES-256 data key. The key itself is encrypted by
// the purpose-specific master sealer using identical authenticated context.
func (s *Store) Seal(ctx context.Context, in SealInput) (secretModel.Reference, error) {
	master, ok := s.keyring.Sealer(in.Purpose)
	if !ok {
		return secretModel.Reference{}, fmt.Errorf("%w: %s", ErrPurposeNotConfigured, in.Purpose)
	}
	if in.ScopeEventID == uuid.Nil || in.FieldPath == "" || in.SignalType == "" {
		return secretModel.Reference{}, errors.New("secret: signal type, field path, and scope event are required")
	}
	now := s.now().UTC()
	envelope := secretModel.Envelope{
		ID: tools.NewUUIDv7(), Purpose: in.Purpose, KeyVersion: 1,
		SignalType: in.SignalType, FieldPath: in.FieldPath, ScopeEventID: in.ScopeEventID,
		RecipientUserID: in.RecipientUserID, ExpiresAt: in.ExpiresAt, CreatedAt: now,
	}
	contextBytes, err := envelope.Context()
	if err != nil {
		return secretModel.Reference{}, fmt.Errorf("secret: encode context: %w", err)
	}
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		return secretModel.Reference{}, fmt.Errorf("secret: generate data key: %w", err)
	}
	defer zero(dataKey)
	dataCipher, err := pkgSecret.NewFromBytes(dataKey)
	if err != nil {
		return secretModel.Reference{}, err
	}
	defer dataCipher.Destroy()
	envelope.Ciphertext, err = dataCipher.EncryptWithContext(in.Plaintext, contextBytes)
	if err != nil {
		return secretModel.Reference{}, fmt.Errorf("secret: encrypt envelope: %w", err)
	}
	envelope.WrappedDataKey, err = master.EncryptWithContext(dataKey, contextBytes)
	if err != nil {
		return secretModel.Reference{}, fmt.Errorf("secret: wrap data key: %w", err)
	}
	if err := s.repo.Create(ctx, envelope); err != nil {
		return secretModel.Reference{}, err
	}
	return envelope.Reference(), nil
}

// WithPlaintext resolves an envelope only for the duration of use and then
// zeroes the recovered data key and plaintext buffer.
func (s *Store) WithPlaintext(ctx context.Context, ref secretModel.Reference, use func([]byte) error) error {
	envelope, err := s.repo.Get(ctx, ref.ID)
	if err != nil {
		return err
	}
	if envelope.Purpose != ref.Purpose || envelope.FieldPath != ref.FieldPath {
		return errors.New("secret: reference does not match envelope")
	}
	if envelope.ExpiresAt != nil && !s.now().UTC().Before(*envelope.ExpiresAt) {
		return ErrExpired
	}
	master, ok := s.keyring.Sealer(envelope.Purpose)
	if !ok {
		return fmt.Errorf("%w: %s", ErrPurposeNotConfigured, envelope.Purpose)
	}
	contextBytes, err := envelope.Context()
	if err != nil {
		return fmt.Errorf("secret: encode context: %w", err)
	}
	dataKey, err := master.DecryptWithContext(envelope.WrappedDataKey, contextBytes)
	if err != nil {
		return fmt.Errorf("secret: unwrap data key: %w", err)
	}
	defer zero(dataKey)
	dataCipher, err := pkgSecret.NewFromBytes(dataKey)
	if err != nil {
		return err
	}
	defer dataCipher.Destroy()
	plaintext, err := dataCipher.DecryptWithContext(envelope.Ciphertext, contextBytes)
	if err != nil {
		return fmt.Errorf("secret: decrypt envelope: %w", err)
	}
	defer zero(plaintext)
	return use(plaintext)
}

func zero(bytes []byte) {
	for i := range bytes {
		bytes[i] = 0
	}
}
