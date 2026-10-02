package render

import (
	"encoding/json"
	"strings"
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

// The reported PoC: a loop that multiplies into gigabytes. A template is variable substitution only.
func TestTemplatesAreVariableSubstitutionOnly(t *testing.T) {
	vars := map[string]any{"Name": "Bob", "n": 3}
	for name, tmpl := range map[string]string{
		"nested range":   `{{range 100000}}{{range 100000}}{{range 100}}xxxxxxxx{{end}}{{end}}{{end}}`,
		"range":          `{{range 2000}}x{{end}}`,
		"with":           `{{with .Name}}{{.}}{{end}}`,
		"if":             `{{if .Name}}yes{{end}}`,
		"printf":         `{{printf "%s" .Name}}`,
		"pipe":           `{{.Name | printf "%s"}}`,
		"define":         `{{define "x"}}a{{end}}{{template "x"}}`,
		"template":       `{{template "t"}}`,
		"call":           `{{call .Name}}`,
		"block":          `{{block "b" .}}x{{end}}`,
		"comment":        `{{/* c */}}`,
		"trim marker":    `{{- .Name -}}`,
		"field of field": `{{.Name.Len}}`,
		"unclosed":       `{{.Name`,
		"two args":       `{{.Name .n}}`,
	} {
		_, err := RenderText(tmpl, vars)
		assert.Error(t, err, "text: %s", name)
		_, err = RenderHTML(tmpl, vars)
		assert.Error(t, err, "html: %s", name)
	}
}

func TestPlainVariablesStillRenderWithAndWithoutTheDot(t *testing.T) {
	vars := map[string]any{"name": "<b>Bob</b>", "n": 3}
	out, err := RenderText("Hi {{.name}} / {{ name }} / {{.n}}", vars)
	require.NoError(t, err)
	assert.Equal(t, "Hi <b>Bob</b> / <b>Bob</b> / 3", out)
	html, err := RenderHTML(`<p title="{{.name}}">{{ name }}</p>`, vars)
	require.NoError(t, err)
	assert.NotContains(t, html, "<b>Bob</b>", "html output stays contextually escaped")
}

func TestOversizedTemplatesAndOutputAreRefused(t *testing.T) {
	_, err := RenderText(strings.Repeat("x", 65<<10), nil)
	assert.Error(t, err, "a template over 64 KiB")
	huge := strings.Repeat("y", 200<<10)
	_, err = RenderText("{{.a}}{{.a}}", map[string]any{"a": huge})
	assert.Error(t, err, "output over 256 KiB")
	_, err = RenderText("{{.a}}", map[string]any{"a": huge})
	assert.NoError(t, err)
}

func TestARecursivePresetCannotTakeTheProcessDown(t *testing.T) {
	a, b := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	loop := func(id string) json.RawMessage {
		return json.RawMessage(`[{"type":"preset","preset_id":"` + id + `"},{"type":"preset","preset_id":"` + id + `"}]`)
	}
	presets := map[string]json.RawMessage{a: loop(a)}
	_, err := RenderEmail(json.RawMessage(`[{"type":"preset","preset_id":"`+a+`"}]`), nil, presets, nil, RenderContext{})
	assert.NoError(t, err, "a preset that uses itself ends at the depth limit")
	presets = map[string]json.RawMessage{a: loop(b), b: loop(a)}
	_, err = RenderEmail(json.RawMessage(`[{"type":"preset","preset_id":"`+a+`"}]`), nil, presets, nil, RenderContext{})
	assert.NoError(t, err, "a pair of presets that use each other ends at the depth limit")
	assert.True(t, ContainsPreset(loop(a)))
	assert.False(t, ContainsPreset(json.RawMessage(`[{"type":"image"}]`)))
}
