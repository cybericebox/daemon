package mailUseCase

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/pkg/email"
)

func TestDeliver_MIMEBodiesKeepContentAndFooter(t *testing.T) {
	for _, tc := range []struct{ name, html string }{
		{name: "text-only"},
		{name: "html-and-text", html: "<p>Помилка &amp; деталі</p>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			htmlBody := tc.html
			uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
			repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
			expectPlatformIdentity(repo, &platformIdentityRow)
			text := "Помилка & деталі\nStack: main.go:42"
			require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "ops@example.org", Subject: "Помилка", Text: text, HTML: htmlBody}))
			require.Len(t, smtp.sends, 1)
			sent := smtp.sends[0].msg
			var raw bytes.Buffer
			_, err := email.BuildMessage(sent.From, sent.ReplyTo, sent).WriteTo(&raw)
			require.NoError(t, err)
			parsed, err := mail.ReadMessage(&raw)
			require.NoError(t, err)
			kind, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
			require.NoError(t, err)
			parts := map[string]string{}
			if htmlBody == "" {
				require.Equal(t, "text/plain", kind, "HTML clients must see the complete text message, not only the footer")
				body, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
				require.NoError(t, err)
				parts[kind] = string(body)
			} else {
				require.Equal(t, "multipart/alternative", kind)
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
					parts[partKind] = string(body)
				}
				require.Len(t, parts, 2)
				require.True(t, strings.HasPrefix(parts["text/html"], htmlBody))
				require.Contains(t, parts["text/html"], `href="mailto:support@cybericebox.com"`)
			}
			plain := strings.ReplaceAll(parts["text/plain"], "\r\n", "\n")
			require.True(t, strings.HasPrefix(plain, text+"\n\nЦей лист надіслано автоматично."), plain)
			require.Contains(t, plain, "Цей лист надіслано автоматично.")
			require.Equal(t, 1, strings.Count(plain, "Політика конфіденційності"), "the footer is appended once")
		})
	}
}
