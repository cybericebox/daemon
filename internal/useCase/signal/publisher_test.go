package signalUseCase

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	signalModel "github.com/cybericebox/daemon/internal/model/signal"
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
