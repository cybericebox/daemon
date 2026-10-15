package errorJournalUseCase

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
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
	cfg.AdminURL = "https://admin.example.test"
	h := newHarness(cfg)
	event := fiveXX("database write failed: " + strings.Repeat("operation ", 70) + "tail-marker password=hidden")
	event.Stack = "handler.go:42\nservice.go:19 token=hidden"
	event.Details = map[string]string{"queue": "notifications", "attempt": "2", "credentials": "token=hidden"}
	_, err := h.j.Record(ctx, event)
	require.NoError(t, err)
	require.Len(t, h.mail.sent, 1)
	msg := h.mail.sent[0]
	require.Equal(t, "[production] "+string(errorJournal.KindHTTP5xx), msg.Subject)
	require.Contains(t, msg.Text, "https://admin.example.test/errors/?group=")
	msg.HTML, msg.Text = mailUseCase.RenderPlatformFooter("support@example.test", "https://platform.example").Append(msg.HTML, msg.Text)
	var raw bytes.Buffer
	_, err = email.BuildMessage(email.Address{Email: "from@example.test"}, email.Address{}, msg).WriteTo(&raw)
	require.NoError(t, err)
	parsed, err := mail.ReadMessage(&raw)
	require.NoError(t, err)
	kind, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/alternative", kind)
	parts := map[string]string{}
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		partKind, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		require.NoError(t, err)
		body, err := io.ReadAll(quotedprintable.NewReader(part))
		require.NoError(t, err)
		parts[partKind] = strings.ReplaceAll(string(body), "\r\n", "\n")
	}
	require.Len(t, parts, 2)
	text := parts["text/plain"]
	for _, detail := range []string{"database write failed:", "tail-marker", "queue: notifications", "attempt: 2", "handler.go:42", "service.go:19", "Total: 1", "Цей лист надіслано автоматично."} {
		require.Contains(t, text, detail)
		require.Contains(t, parts["text/html"], detail)
	}
	require.Contains(t, parts["text/html"], `class="cib-card"`)
	require.Contains(t, parts["text/html"], `class="cib-muted"`)
	require.Equal(t, 1, strings.Count(parts["text/html"], `href="https://platform.example/privacy"`))
	require.NotContains(t, parts["text/html"], "<!--cib-footer-->")
	require.NotContains(t, parts["text/html"], "password=hidden")
	require.NotContains(t, parts["text/html"], "token=hidden")
	require.NotContains(t, text, "hidden")
	require.Contains(t, text, "[redacted]")
	require.NotContains(t, h.tg.last("100"), "handler.go:42", "Telegram keeps the existing concise notification")
}
