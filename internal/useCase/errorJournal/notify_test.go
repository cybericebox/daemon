package errorJournalUseCase

import (
	"bytes"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	mailUseCase "github.com/cybericebox/daemon/internal/useCase/mail"
	"github.com/cybericebox/daemon/pkg/email"
)

func TestErrorEmail_HasShortSubjectAndFullScrubbedBody(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Environment = "production"
	h := newHarness(cfg)
	event := fiveXX("database write failed: " + strings.Repeat("operation ", 70) + "tail-marker password=hidden")
	event.Stack = "handler.go:42\nservice.go:19 token=hidden"
	event.Details = map[string]string{"queue": "notifications", "attempt": "2", "credentials": "token=hidden"}
	_, err := h.j.Record(ctx, event)
	require.NoError(t, err)
	require.Len(t, h.mail.sent, 1)
	msg := h.mail.sent[0]
	require.Equal(t, "[production] "+string(errorJournal.KindHTTP5xx), msg.Subject)
	msg.HTML, msg.Text = mailUseCase.RenderPlatformFooter("support@example.test", "https://platform.example").Append(msg.HTML, msg.Text)
	var raw bytes.Buffer
	_, err = email.BuildMessage(email.Address{Email: "from@example.test"}, email.Address{}, msg).WriteTo(&raw)
	require.NoError(t, err)
	parsed, err := mail.ReadMessage(&raw)
	require.NoError(t, err)
	kind, _, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "text/plain", kind)
	body, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	require.NoError(t, err)
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	for _, detail := range []string{"database write failed:", "tail-marker", "queue: notifications", "attempt: 2", "handler.go:42", "service.go:19", "Total: 1", "Цей лист надіслано автоматично."} {
		require.Contains(t, text, detail)
	}
	require.NotContains(t, text, "hidden")
	require.Contains(t, text, "[redacted]")
	require.NotContains(t, h.tg.last("100"), "handler.go:42", "Telegram keeps the existing concise notification")
}
