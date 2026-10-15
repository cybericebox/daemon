package errorJournalUseCase

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmailHTML_EscapesDiagnosticsAndUsesOnlyTheConfiguredJournalForTheButton(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AdminURL = "https://admin.example.test"
	h := newHarness(cfg)
	url := cfg.AdminURL + "/errors/?group=11111111-1111-4111-8111-111111111111"
	text := "[production] job\nWorker <failed>\nSource: queue <default> {{.literal}}\nTotal: 12\n" + url + "\n\n<img src=x onerror=alert(1)>\nStack:\nmain.go:42 {{.literal}}"
	body, err := h.j.emailHTML(text)
	require.NoError(t, err)
	require.Contains(t, body, `href="`+url+`"`)
	require.Contains(t, body, `>Відкрити помилку</a>`)
	require.Equal(t, 1, strings.Count(body, url), "the raw URL is not repeated as visible content")
	require.Contains(t, body, "Worker &lt;failed&gt;")
	require.Contains(t, body, "queue &lt;default&gt; {{.literal}}")
	require.Contains(t, body, `class="cib-btn" bgcolor="#211A52"`)
	require.Contains(t, body, "&lt;img src=x onerror=alert(1)&gt;")
	require.Contains(t, body, "main.go:42 {{.literal}}")
	require.NotContains(t, body, "<img")

	spoof := "https://admin.example.test.evil.test/errors/?group=11111111-1111-4111-8111-111111111111"
	body, err = h.j.emailHTML("Test\n" + spoof)
	require.NoError(t, err)
	require.NotContains(t, body, `href="`+spoof)
	require.Contains(t, body, `href="https://admin.example.test/errors/"`)
	require.Contains(t, body, `>Відкрити журнал</a>`)

	legacy := cfg.AdminURL + "/errors/11111111-1111-4111-8111-111111111111/"
	body, err = h.j.emailHTML("Test\n" + legacy)
	require.NoError(t, err)
	require.Contains(t, body, `href="`+url+`"`)
	require.NotContains(t, body, legacy)
}
