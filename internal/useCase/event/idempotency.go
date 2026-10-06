package event

import (
	"context"
	"time"

	"github.com/cybericebox/daemon/internal/model"
)

// CleanupExpiredRequestIdempotency removes only completed, short-lived replay
// records. In-flight records belong to their surrounding transaction and are
// never collected out from under a request.
func (u *EventUseCase) CleanupExpiredRequestIdempotency(ctx context.Context) error {
	if err := u.idempotency.CleanupExpired(ctx, time.Now()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to clean expired idempotency records").Err()
	}
	return nil
}
