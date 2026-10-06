package signalOutboxRepo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

type recordingQueries struct {
	params postgres.CreateSignalOutboxParams
}

func (q *recordingQueries) CreateSignalOutbox(_ context.Context, params postgres.CreateSignalOutboxParams) (postgres.SignalOutbox, error) {
	q.params = params
	return postgres.SignalOutbox{}, nil
}

func TestRepositoryCreatePersistsSignalForImmediateDelivery(t *testing.T) {
	t.Parallel()

	queries := &recordingQueries{}
	repo := New(queries)
	now := time.Date(2026, time.September, 23, 10, 0, 0, 0, time.UTC)
	payload := json.RawMessage(`{"scope_event_id":"018e6c90-4100-7000-8000-000000000001"}`)
	signal := signalModel.Signal{
		ID:         uuid.Must(uuid.FromString("018e6c90-4100-7000-8000-000000000002")),
		Type:       "participant.enrolled",
		OccurredAt: now,
		Payload:    payload,
	}

	require.NoError(t, repo.Create(context.Background(), signal))
	require.Equal(t, signal.ID, queries.params.ID)
	require.Equal(t, string(signal.Type), queries.params.SignalType)
	require.Equal(t, signal.OccurredAt, queries.params.OccurredAt)
	require.Equal(t, []byte(signal.Payload), queries.params.Payload)
	require.Equal(t, signal.OccurredAt, queries.params.AvailableAt)
	require.Equal(t, signal.OccurredAt, queries.params.CreatedAt)
}
