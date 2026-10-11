package auth

import (
	"context"
	"encoding/json"
	"net/http"
)

// ExportHashTemporalCode exposes the stored form of a raw code to external tests.
func ExportHashTemporalCode(code string) string { return hashTemporalCode(code) }

// ExportConsumeTemporalCode exposes consumeTemporalCode to external tests.
func ExportConsumeTemporalCode(u *AuthUseCase, ctx context.Context, code string, expectedType int32) (json.RawMessage, error) {
	return u.consumeTemporalCode(ctx, code, expectedType)
}

// ExportIsProviderAvatarURL exposes isProviderAvatarURL to external tests.
func ExportIsProviderAvatarURL(raw string) bool { return isProviderAvatarURL(raw) }

// ExportSetAvatarDownloadClient confines provider-avatar downloads to a test transport.
func ExportSetAvatarDownloadClient(client *http.Client) func() {
	previous := avatarDownloadClient
	avatarDownloadClient = client
	return func() { avatarDownloadClient = previous }
}
