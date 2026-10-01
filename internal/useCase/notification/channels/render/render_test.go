package render

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderText_Substitutes(t *testing.T) {
	out, err := RenderText("Hi {{.Name}}", map[string]any{"Name": "Bob"})
	require.NoError(t, err)
	assert.Equal(t, "Hi Bob", out)
}

func TestRenderText_MissingKey_Errors(t *testing.T) {
	_, err := RenderText("Hi {{.Missing}}", map[string]any{})
	assert.Error(t, err)
}

func TestRenderHTML_EscapesValues(t *testing.T) {
	out, err := RenderHTML("<p>{{.X}}</p>", map[string]any{"X": "<script>"})
	require.NoError(t, err)
	assert.NotContains(t, out, "<script>")
}

func TestRenderHTML_PreservesInAppFormattingAroundVariable(t *testing.T) {
	out, err := RenderHTML(`<strong>Привіт, {{.Name}}</strong>`, map[string]any{"Name": "Олена & команда"})
	require.NoError(t, err)
	assert.Equal(t, `<strong>Привіт, Олена &amp; команда</strong>`, out)
}
