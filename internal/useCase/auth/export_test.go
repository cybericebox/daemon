package auth

import (
	"context"
	"encoding/json"
)

// ExportConsumeTemporalCode exposes consumeTemporalCode to external tests.
func ExportConsumeTemporalCode(u *AuthUseCase, ctx context.Context, code string, expectedType int32) (json.RawMessage, error) {
	return u.consumeTemporalCode(ctx, code, expectedType)
}
