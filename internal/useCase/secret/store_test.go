package secretUseCase

import (
	"context"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	secretModel "github.com/cybericebox/daemon/internal/model/secret"
	pkgSecret "github.com/cybericebox/daemon/pkg/secret"
)

type memoryEnvelopeRepository struct{ envelope secretModel.Envelope }

func (r *memoryEnvelopeRepository) Create(_ context.Context, envelope secretModel.Envelope) error {
	r.envelope = envelope
	return nil
}
func (r *memoryEnvelopeRepository) Get(_ context.Context, _ uuid.UUID) (secretModel.Envelope, error) {
	return r.envelope, nil
}

func TestStoreSealsPerRecordKeyAndBindsEnvelopeContext(t *testing.T) {
	t.Parallel()

	master, err := pkgSecret.New(strings.Repeat("a1", 32))
	require.NoError(t, err)
	repo := &memoryEnvelopeRepository{}
	store := NewStore(repo, StaticKeyring{secretModel.PurposeNotification: master})
	plain := []byte("one-time-invitation-token")
	ref, err := store.Seal(context.Background(), SealInput{
		Purpose: secretModel.PurposeNotification, SignalType: "participant.invitation.sent",
		FieldPath: "invitation.token", ScopeEventID: uuid.Must(uuid.NewV7()), Plaintext: plain,
	})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, ref.ID)
	require.NotContains(t, repo.envelope.Ciphertext, string(plain))
	require.NotContains(t, repo.envelope.WrappedDataKey, string(plain))

	var got []byte
	require.NoError(t, store.WithPlaintext(context.Background(), ref, func(value []byte) error {
		got = append([]byte(nil), value...)
		return nil
	}))
	require.Equal(t, plain, got)

	repo.envelope.FieldPath = "invitation.other"
	require.Error(t, store.WithPlaintext(context.Background(), ref, func([]byte) error { return nil }))
}
