package eventConfigModel_test

import (
	"encoding/json"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestWebsiteFooterSignature(t *testing.T) {
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	actor := uuid.Must(uuid.NewV7())
	cfg := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), now)
	doc := json.RawMessage(`{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"link","url":"https://partner.example/","children":[{"type":"text","text":"Partner <script>"}]}]}]}}`)
	require.NoError(t, cfg.SetFooterSignature(doc, now.Add(time.Minute), actor))
	require.JSONEq(t, string(doc), string(cfg.FooterSignature))
	require.Equal(t, now.Add(time.Minute), cfg.UpdatedAt)
	doc[0] = 'x'
	require.Equal(t, byte('{'), cfg.FooterSignature[0], "saved state must not alias input")
	for _, raw := range []string{`{"root":{"type":"root","children":[]}}`, `{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"  "}]}]}}`, `null`} {
		require.NoError(t, cfg.SetFooterSignature(json.RawMessage(raw), now, actor))
		require.Nil(t, cfg.FooterSignature, "empty signatures use the platform default")
	}
}

func TestWebsiteFooterRejectsUnsafeContent(t *testing.T) {
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	for _, node := range []string{`{"type":"link","url":"javascript:alert(1)","children":[{"type":"text","text":"bad"}]}`, `{"type":"image","src":"https://tracker.example/"}`, `{"type":"variable","varName":"event.name"}`, `{"type":"text","text":"bad","format":1024}`} {
		t.Run(node, func(t *testing.T) {
			cfg := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), now)
			err := cfg.SetFooterSignature(json.RawMessage(`{"root":{"type":"root","children":[`+node+`]}}`), now.Add(time.Hour), uuid.Nil)
			require.Error(t, err)
			require.Nil(t, cfg.FooterSignature)
			require.Equal(t, now, cfg.UpdatedAt)
		})
	}
}

func TestWebsiteFooterSignatureHasABoundedSize(t *testing.T) {
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	cfg := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), now)
	raw := json.RawMessage(`{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"` + strings.Repeat("x", 20000) + `"}]}]}}`)
	require.Error(t, cfg.SetFooterSignature(raw, now, uuid.Nil))
	require.Nil(t, cfg.FooterSignature)
}
