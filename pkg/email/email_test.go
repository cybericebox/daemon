package email_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/pkg/email"
)

func writeMessage(t *testing.T, msg email.Message) string {
	t.Helper()
	m := email.BuildMessage(email.Address{Name: "Sender", Email: "from@example.com"}, email.Address{Email: "reply@example.com"}, msg)
	var buf bytes.Buffer
	_, err := m.WriteTo(&buf)
	require.NoError(t, err)
	return buf.String()
}

var logoPart = email.InlinePart{
	ContentID:   "logo@cybericebox",
	ContentType: "image/png",
	Data:        []byte("\x89PNG fake"),
}

func TestBuildMessage_InlinePartIsRelatedNotMixed(t *testing.T) {
	out := writeMessage(t, email.Message{
		To:      "to@example.com",
		Subject: "Hi",
		HTML:    `<img src="cid:logo@cybericebox">`,
		Inline:  []email.InlinePart{logoPart},
	})

	require.Contains(t, out, "multipart/related")
	require.Contains(t, out, "Content-ID: <logo@cybericebox>")
	require.Contains(t, out, "Content-Disposition: inline")
	require.Contains(t, out, "Content-Type: image/png")
	require.NotContains(t, out, "multipart/mixed")
	require.NotContains(t, out, "attachment")
	require.NotContains(t, out, "multipart/alternative", "no text part → no alternative")
	require.Contains(t, out, "Reply-To: reply@example.com")
}

func TestBuildMessage_TextAlternativeInsideRelated(t *testing.T) {
	out := writeMessage(t, email.Message{
		To:      "to@example.com",
		Subject: "Hi",
		HTML:    `<p>Hello</p><img src="cid:logo@cybericebox">`,
		Text:    "Hello",
		Inline:  []email.InlinePart{logoPart},
	})

	related := strings.Index(out, "multipart/related")
	alternative := strings.Index(out, "multipart/alternative")
	require.GreaterOrEqual(t, related, 0)
	require.Greater(t, alternative, related, "alternative must be nested inside related")
	plain := strings.Index(out, "text/plain")
	htmlIdx := strings.Index(out, "text/html")
	require.Greater(t, plain, alternative)
	require.Greater(t, htmlIdx, plain, "text/plain must precede text/html (least preferred first)")
	require.Contains(t, out, "Content-ID: <logo@cybericebox>")
	require.NotContains(t, out, "multipart/mixed")
}

func TestBuildMessage_HTMLOnlyWithoutInline(t *testing.T) {
	out := writeMessage(t, email.Message{To: "to@example.com", Subject: "Hi", HTML: "<p>x</p>"})
	require.Contains(t, out, "Content-Type: text/html")
	require.NotContains(t, out, "multipart/")
}

func TestBuildMessage_EncodesCyrillicSenderName(t *testing.T) {
	m := email.BuildMessage(email.Address{Name: "Кібер Олімпіада", Email: "olymp@mail.example.com"}, email.Address{}, email.Message{To: "to@example.com", Subject: "Hi", HTML: "<p>x</p>"})
	var buf bytes.Buffer
	_, err := m.WriteTo(&buf)
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "<olymp@mail.example.com>")
	require.Contains(t, out, "From: =?UTF-8?")
	require.NotContains(t, out, "Reply-To:")
}
