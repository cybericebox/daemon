package signalUseCase

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	secretModel "github.com/cybericebox/daemon/internal/model/secret"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	secretUseCase "github.com/cybericebox/daemon/internal/useCase/secret"
	pkgSecret "github.com/cybericebox/daemon/pkg/secret"
)

type publisherPayload struct {
	Value string `json:"value"`
}

func (publisherPayload) Routing() signalModel.Routing { return signalModel.Routing{} }

type recordingOutbox struct{ saved signalModel.Signal }

func (o *recordingOutbox) Create(ctx context.Context, s signalModel.Signal) error {
	o.saved = s
	return nil
}

func TestPublisherPersistsSerializedPayload(t *testing.T) {
	outbox := &recordingOutbox{}
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	publisher := NewPublisher(outbox, func() time.Time { return now })

	err := publisher.Publish(context.Background(), "participant.open_registration.completed", publisherPayload{Value: "fixed"})
	require.NoError(t, err)
	require.Equal(t, signalModel.Type("participant.open_registration.completed"), outbox.saved.Type)
	require.Equal(t, now, outbox.saved.OccurredAt)
	var got publisherPayload
	require.NoError(t, json.Unmarshal(outbox.saved.Payload, &got))
	require.Equal(t, "fixed", got.Value)
	require.NotEqual(t, uuid.Nil, outbox.saved.ID)
}

type secretPublisherPayload struct {
	ScopeEventID uuid.UUID         `json:"scope_event_id"`
	Token        secretModel.Value `json:"token" secret:"true"`
}

func (p secretPublisherPayload) Routing() signalModel.Routing {
	return signalModel.Routing{ScopeEventID: p.ScopeEventID}
}

type memorySecretRepo struct{ envelope secretModel.Envelope }

func (r *memorySecretRepo) Create(_ context.Context, envelope secretModel.Envelope) error {
	r.envelope = envelope
	return nil
}
func (r *memorySecretRepo) Get(_ context.Context, _ uuid.UUID) (secretModel.Envelope, error) {
	return r.envelope, nil
}

func TestPublisherReplacesSecretTaggedValueWithOpaqueReference(t *testing.T) {
	t.Parallel()

	master, err := pkgSecret.New("a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1")
	require.NoError(t, err)
	secretRepo := &memorySecretRepo{}
	secrets := secretUseCase.NewStore(secretRepo, secretUseCase.StaticKeyring{secretModel.PurposeNotification: master})
	outbox := &recordingOutbox{}
	publisher := NewPublisherWithSecrets(outbox, secrets, func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) })
	secretValue := "never-persist-plaintext"

	err = publisher.Publish(context.Background(), "participant.invitation.sent", secretPublisherPayload{
		ScopeEventID: uuid.Must(uuid.NewV7()), Token: secretModel.NewValue(secretValue),
	})
	require.NoError(t, err)
	require.NotContains(t, string(outbox.saved.Payload), secretValue)
	require.NotContains(t, secretRepo.envelope.Ciphertext, secretValue)
	var persisted secretPublisherPayload
	require.NoError(t, json.Unmarshal(outbox.saved.Payload, &persisted))
	require.NotNil(t, persisted.Token.Reference())
}
