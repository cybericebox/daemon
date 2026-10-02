package auth

import (
	"context"
	"encoding/json"
)

// ExportHashTemporalCode exposes the stored form of a raw code to external tests.
func ExportHashTemporalCode(code string) string { return hashTemporalCode(code) }

// ExportConsumeTemporalCode exposes consumeTemporalCode to external tests.
func ExportConsumeTemporalCode(u *AuthUseCase, ctx context.Context, code string, expectedType int32) (json.RawMessage, error) {
	return u.consumeTemporalCode(ctx, code, expectedType)
}
