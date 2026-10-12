package email_test

import (
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/pkg/email"
)

func TestBuildMessage_TextOnlyHasNoEmptyHTMLAlternative(t *testing.T) {
	text := "Помилка запиту\nDetails: status=500\nStack: main.go:42"
	parsed, err := mail.ReadMessage(strings.NewReader(writeMessage(t, email.Message{
		To: "ops@example.test", Subject: "Помилка запиту", Text: text,
	})))
	require.NoError(t, err)
	kind, _, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "text/plain", kind, "an empty HTML alternative must not hide the text body")
	body, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	require.NoError(t, err)
	require.Equal(t, text, strings.ReplaceAll(string(body), "\r\n", "\n"))
	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	require.NoError(t, err)
	require.Equal(t, "Помилка запиту", subject)
}
