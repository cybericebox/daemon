package signalUseCase

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	"github.com/cybericebox/daemon/pkg/tools"
)

// Outbox is deliberately small so a use case can use its transaction-bound
// repository without depending on PostgreSQL or sqlc types.
type Outbox interface {
	Create(context.Context, signalModel.Signal) error
}

type Publisher struct {
	outbox Outbox
	now    func() time.Time
}

func NewPublisher(outbox Outbox, now func() time.Time) *Publisher {
	return &Publisher{outbox: outbox, now: now}
}

func (p *Publisher) Publish(ctx context.Context, typ signalModel.Type, payload signalModel.Payload) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("signal: marshal %s: %w", typ, err)
	}
	return p.outbox.Create(ctx, signalModel.Signal{
		ID:         tools.NewUUIDv7(),
		Type:       typ,
		OccurredAt: p.now(),
		Payload:    raw,
	})
}
