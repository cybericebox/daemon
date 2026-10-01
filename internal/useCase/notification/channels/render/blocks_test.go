package render_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/model/notification/branding"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/render"
)

// cidContext is the dispatch-style render context: platform brand, cid: srcs.
func cidContext(brand branding.Context) render.RenderContext {
	return render.RenderContext{
		Brand:    brand,
		AssetSrc: func(a render.Asset) string { return "cid:" + render.CID(a) },
	}
}

// renderBlocks renders with the platform brand and returns only the HTML.
func renderBlocks(body, styling json.RawMessage, presets map[string]json.RawMessage, vars map[string]any) (string, error) {
	r, err := render.RenderEmail(body, styling, presets, vars, cidContext(branding.Platform()))
	return r.HTML, err
}

// ── Brief-mandated tests ──────────────────────────────────────────────────────

func TestRenderBlocks_RichTextWithVariable(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[
	    {"type":"text","text":"Hi ","format":0},
	    {"type":"variable","varName":"name"},
	    {"type":"text","text":"!","format":0}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, map[string]any{"name": "Ann"})
	require.NoError(t, err)
	assert.Contains(t, out, "Hi Ann!")
	assert.Contains(t, out, "<p")
}

func TestRenderBlocks_Button(t *testing.T) {
	body := json.RawMessage(`[{"type":"button","label":"Go","url":"{{cta}}","align":"center"}]`)
	out, err := renderBlocks(body, json.RawMessage(`{"cta_bg_color":"#000"}`), nil, map[string]any{"cta": "https://x"})
	require.NoError(t, err)
	assert.Contains(t, out, `href="https://x"`)
	assert.Contains(t, out, "Go")
}

func TestRenderBlocks_PresetExpansion(t *testing.T) {
	body := json.RawMessage(`[{"type":"preset","preset_id":"p1","name":"footer"}]`)
	presets := map[string]json.RawMessage{"p1": json.RawMessage(`[{"type":"divider"}]`)}
	out, err := renderBlocks(body, json.RawMessage(`{}`), presets, nil)
	require.NoError(t, err)
	assert.Contains(t, out, "cib-rule")
}

func TestRenderBlocks_EscapesVariableValue(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[{"type":"variable","varName":"x"}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, map[string]any{"x": "<script>"})
	require.NoError(t, err)
	assert.NotContains(t, out, "<script>")
	assert.Contains(t, out, "&lt;script&gt;")
}

// ── Extra coverage tests ──────────────────────────────────────────────────────

func TestRenderBlocks_Divider(t *testing.T) {
	body := json.RawMessage(`[{"type":"divider"}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.Contains(t, out, "cib-rule")
}

func TestRenderBlocks_Heading(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"heading","tag":"h1","children":[{"type":"text","text":"Title","format":0}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.Contains(t, out, "<h1")
	assert.Contains(t, out, "Title")
	assert.Contains(t, out, "</h1>")
}

func TestRenderBlocks_TextFormats(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[
	    {"type":"text","text":"bold","format":1},
	    {"type":"text","text":"italic","format":2},
	    {"type":"text","text":"strike","format":4},
	    {"type":"text","text":"under","format":8},
	    {"type":"text","text":"code","format":16}
	  ]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.Contains(t, out, "<strong>bold</strong>")
	assert.Contains(t, out, "<em>italic</em>")
	assert.Contains(t, out, "<s>strike</s>")
	assert.Contains(t, out, "<u>under</u>")
	assert.Contains(t, out, "<code>code</code>")
}

func TestRenderBlocks_MissingPreset_NoError(t *testing.T) {
	body := json.RawMessage(`[{"type":"preset","preset_id":"missing","name":"x"}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), map[string]json.RawMessage{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "", out)
}

func TestRenderBlocks_NilPresets_NoError(t *testing.T) {
	body := json.RawMessage(`[{"type":"preset","preset_id":"p1","name":"x"}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "", out)
}

func TestRenderBlocks_ButtonUrlSubstitution(t *testing.T) {
	body := json.RawMessage(`[{"type":"button","label":"Click","url":"https://example.com/{{ token }}","align":"left"}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, map[string]any{"token": "abc123"})
	require.NoError(t, err)
	assert.Contains(t, out, `href="https://example.com/abc123"`)
}

func TestRenderBlocks_List(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"list","listType":"number","children":[
	    {"type":"listitem","children":[{"type":"text","text":"one","format":0}]},
	    {"type":"listitem","children":[{"type":"text","text":"two","format":0}]}
	  ]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.Contains(t, out, "<ol>")
	assert.Contains(t, out, "<li>")
	assert.Contains(t, out, "one")
	assert.Contains(t, out, "two")
}

// ── Security regression tests (XSS vectors) ───────────────────────────────────

// TestRenderBlocks_HeadingTagAllowlist verifies that a malicious tag name in a
// heading node is rejected and replaced with the safe default "h2".
func TestRenderBlocks_HeadingTagAllowlist(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"heading","tag":"h2><script>alert(1)<\/script><h2","children":[{"type":"text","text":"XSS","format":0}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.NotContains(t, out, "<script>")
	assert.NotContains(t, out, "alert(1)")
	assert.Contains(t, out, "<h2")
	assert.Contains(t, out, "</h2>")
}

// TestRenderBlocks_StylingValueSanitized verifies that a breakout payload in a
// styling field does not reach the output as raw HTML/attribute syntax.
// The attack vector is: close the style attribute with `"`, inject arbitrary
// attributes/tags. safeCSSValue strips `"`, `;`, `<`, `>` so neither the
// attribute-breakout nor the raw <script> tag survives.
func TestRenderBlocks_StylingValueSanitized(t *testing.T) {
	// text_color tries to break out of the style attribute and inject a script tag.
	styling := json.RawMessage(`{"text_color":"red\";x=\"<script>xss<\/script>"}`)
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[{"type":"text","text":"hello","format":0}]}]}}}]`)
	out, err := renderBlocks(body, styling, nil, nil)
	require.NoError(t, err)
	// Raw <script> tag must not appear.
	assert.NotContains(t, out, "<script>")
	// The style attribute must not have been broken out of (no standalone x= attribute).
	assert.NotContains(t, out, `" x=`)
	assert.NotContains(t, out, `";x=`)
	// The <p style="..."> element must still be present.
	assert.Contains(t, out, `<p class="cib-t" style=`)
}

// ── Wave-2 security regression tests (URL scheme + heading tighten) ──────────

// TestRenderBlocks_LinkJavascriptScheme verifies that a javascript: URL in a
// rich_text link node is blocked and replaced with "#".
func TestRenderBlocks_LinkJavascriptScheme(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[
	    {"type":"link","url":"javascript:alert(1)","children":[{"type":"text","text":"click","format":0}]}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.NotContains(t, out, "javascript:")
	assert.Contains(t, out, `href="#"`)
}

// TestRenderBlocks_ButtonJavascriptScheme verifies that a javascript: URL
// injected via a template variable in a button block is blocked.
func TestRenderBlocks_ButtonJavascriptScheme(t *testing.T) {
	body := json.RawMessage(`[{"type":"button","label":"Go","url":"{{cta}}","align":"left"}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, map[string]any{"cta": "javascript:alert(1)"})
	require.NoError(t, err)
	assert.NotContains(t, out, "javascript:")
	assert.Contains(t, out, `href="#"`)
}

// TestRenderBlocks_ImageJavascriptScheme verifies that a javascript: URL in an
// image block src is blocked and replaced with "#".
func TestRenderBlocks_ImageJavascriptScheme(t *testing.T) {
	body := json.RawMessage(`[{"type":"image","url":"javascript:alert(1)","alt":"x","align":"left"}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.NotContains(t, out, "javascript:")
	assert.Contains(t, out, `src="#"`)
}

// TestRenderBlocks_LinkHttpsPreserved verifies that a legitimate https URL is
// not altered by the safeURL allowlist.
func TestRenderBlocks_LinkHttpsPreserved(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[
	    {"type":"link","url":"https://example.com/x","children":[{"type":"text","text":"link","format":0}]}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.Contains(t, out, `href="https://example.com/x"`)
}

// TestRenderBlocks_HeadingLevelsPreserved verifies that every HTML heading
// level pasted from Markdown survives the allowlist.
func TestRenderBlocks_HeadingLevelsPreserved(t *testing.T) {
	for _, tag := range []string{"h1", "h2", "h3", "h4", "h5", "h6"} {
		body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
		  {"type":"heading","tag":"` + tag + `","children":[{"type":"text","text":"Sub","format":0}]}]}}}]`)
		out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
		require.NoError(t, err)
		assert.Contains(t, out, "<"+tag)
		assert.Contains(t, out, "</"+tag+">")
	}
}

// TestRenderBlocks_UnknownHeadingFallsBackToH2 verifies that a tag outside
// the allowlist is replaced with the safe default h2.
func TestRenderBlocks_UnknownHeadingFallsBackToH2(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"heading","tag":"h7","children":[{"type":"text","text":"Deep","format":0}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.NotContains(t, out, "<h7")
	assert.Contains(t, out, "<h2")
	assert.Contains(t, out, "</h2>")
}

// TestRenderBlocks_AlignAllowlist verifies that a malicious align value in both
// image and button blocks is rejected and replaced with the safe default "left".
func TestRenderBlocks_AlignAllowlist(t *testing.T) {
	// Image block with malicious align value.
	imgBody := json.RawMessage(`[{"type":"image","url":"https://example.com/img.png","alt":"x","align":"x\"><script>alert(1)<\/script>"}]`)
	out, err := renderBlocks(imgBody, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.NotContains(t, out, "<script>")
	assert.NotContains(t, out, "alert(1)")
	assert.Contains(t, out, "text-align:left")

	// Button block with malicious align value.
	btnBody := json.RawMessage(`[{"type":"button","label":"Click","url":"https://example.com","align":"x\"><script>alert(1)<\/script>"}]`)
	out, err = renderBlocks(btnBody, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.NotContains(t, out, "<script>")
	assert.NotContains(t, out, "alert(1)")
	assert.Contains(t, out, "text-align:left")
}

// ── Wave-3 security regression tests (protocol-relative URL block) ────────────

// TestRenderBlocks_ProtocolRelativeBlocked verifies that a protocol-relative URL
// (//attacker.com) in a rich_text link node is blocked and replaced with "#".
func TestRenderBlocks_ProtocolRelativeBlocked(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[
	    {"type":"link","url":"//evil.com","children":[{"type":"text","text":"click","format":0}]}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.NotContains(t, out, "//evil.com")
	assert.Contains(t, out, `href="#"`)
}

// TestRenderBlocks_RootRelativeAllowed verifies that a root-relative URL (/path)
// in a rich_text link node is preserved and not confused with protocol-relative.
func TestRenderBlocks_RootRelativeAllowed(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[
	    {"type":"link","url":"/path/x","children":[{"type":"text","text":"link","format":0}]}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.Contains(t, out, `href="/path/x"`)
}

// TestRenderBlocks_MailtoAllowed verifies that a mailto: URL in a rich_text
// link node is preserved.
func TestRenderBlocks_MailtoAllowed(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[
	    {"type":"link","url":"mailto:a@b.com","children":[{"type":"text","text":"email","format":0}]}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.Contains(t, out, `href="mailto:a@b.com"`)
}

// TestRenderBlocks_FragmentAllowed verifies that a fragment URL (#section) in
// a rich_text link node is preserved.
func TestRenderBlocks_FragmentAllowed(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[
	    {"type":"link","url":"#sec","children":[{"type":"text","text":"anchor","format":0}]}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.Contains(t, out, `href="#sec"`)
}

// TestRenderBlocks_HttpAllowed verifies that an http: URL in a rich_text
// link node is preserved.
func TestRenderBlocks_HttpAllowed(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[
	    {"type":"link","url":"http://x.com","children":[{"type":"text","text":"link","format":0}]}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.Contains(t, out, `href="http://x.com"`)
}

// TestRenderBlocks_LinkUrlSubstitution verifies that a rich_text link node's URL
// is variable-substituted just like a button's — an inline "click here" link
// with a {{Var}} href must resolve, not render the placeholder verbatim.
func TestRenderBlocks_LinkUrlSubstitution(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[
	    {"type":"link","url":"{{RegistrationURL}}","children":[{"type":"text","text":"here","format":0}]}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil,
		map[string]any{"RegistrationURL": "https://id.example.org/setup?token=abc"})
	require.NoError(t, err)
	assert.Contains(t, out, `href="https://id.example.org/setup?token=abc"`)
	assert.NotContains(t, out, "{{")
}

// TestRenderBlocks_DotVarSyntax verifies the substitution accepts the optional
// Go-template leading dot ({{.Var}}) as well as the bare {{Var}} form, in both
// link and button URLs — authors reach for {{.Var}} by habit.
func TestRenderBlocks_DotVarSyntax(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[
	    {"type":"link","url":"{{.RegistrationURL}}","children":[{"type":"text","text":"here","format":0}]}]}]}}},
	  {"type":"button","label":"Go","url":"{{ .RegistrationURL }}","align":"left"}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil,
		map[string]any{"RegistrationURL": "https://id.example.org/setup?token=abc"})
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(out, `href="https://id.example.org/setup?token=abc"`))
	assert.NotContains(t, out, "{{")
}

// TestRenderBlocks_SeededTemplates_0018 renders the exact block bodies migration
// 0018 writes for the seeded templates, guarding that the JSON is valid and the
// placeholders resolve (link URL substituted, Name variable node filled, no raw
// {{ }} or escaped HTML left behind).
func TestRenderBlocks_SeededTemplates_0018(t *testing.T) {
	continueReg := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Hello,","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Click ","format":0},{"type":"link","url":"{{RegistrationURL}}","children":[{"type":"text","text":"here","format":0}]},{"type":"text","text":" to finish creating your account.","format":0}]}]}}}]`)
	out, err := renderBlocks(continueReg, json.RawMessage(`{}`), nil,
		map[string]any{"RegistrationURL": "https://id.example.org/setup?token=abc", "Name": "Ann"})
	require.NoError(t, err)
	assert.Contains(t, out, `href="https://id.example.org/setup?token=abc"`)
	assert.Contains(t, out, "to finish creating your account.")
	assert.NotContains(t, out, "{{")
	assert.NotContains(t, out, "&lt;p&gt;")

	passwordReset := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Hi ","format":0},{"type":"variable","varName":"Name"},{"type":"text","text":",","format":0}]},{"type":"paragraph","children":[{"type":"text","text":"Click ","format":0},{"type":"link","url":"{{ResetURL}}","children":[{"type":"text","text":"here","format":0}]},{"type":"text","text":" to reset your password. If you did not request this, ignore this email.","format":0}]}]}}}]`)
	out, err = renderBlocks(passwordReset, json.RawMessage(`{}`), nil,
		map[string]any{"ResetURL": "https://id.example.org/reset?token=xyz", "Name": "Ann"})
	require.NoError(t, err)
	assert.Contains(t, out, "Hi Ann,")
	assert.Contains(t, out, `href="https://id.example.org/reset?token=xyz"`)
	assert.NotContains(t, out, "{{")
}

// TestRenderBlocks_LinkSubstitutionThenSanitized verifies safeURL runs AFTER
// substitution, so a substituted value with a dangerous scheme is still blocked.
func TestRenderBlocks_LinkSubstitutionThenSanitized(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[
	    {"type":"link","url":"{{evil}}","children":[{"type":"text","text":"x","format":0}]}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil,
		map[string]any{"evil": "javascript:alert(1)"})
	require.NoError(t, err)
	assert.NotContains(t, out, "javascript:")
	assert.Contains(t, out, `href="#"`)
}

// ── Branding: tokens, logo, uploaded images ───────────────────────────────────

var testBrand = branding.Context{Brand: "#123456", Accent: "#ABCDEF", OnAccent: "#FEDCBA"}

const headingBody = `[{"type":"rich_text","content":{"root":{"type":"root","children":[
  {"type":"heading","tag":"h1","children":[{"type":"text","text":"Title","format":0}]}]}}}]`

func TestRenderEmail_ThemeTokenInStyling(t *testing.T) {
	body := json.RawMessage(`[{"type":"button","label":"Go","url":"https://x"}]`)
	r, err := render.RenderEmail(body, json.RawMessage(`{"cta_bg_color":"theme:brand"}`), nil, nil, cidContext(testBrand))
	require.NoError(t, err)
	assert.Contains(t, r.HTML, "background-color:#123456")
}

func TestRenderEmail_DefaultsUseBrandTokens(t *testing.T) {
	body := json.RawMessage(`[{"type":"button","label":"Go","url":"https://x"},` + headingBody[1:])
	r, err := render.RenderEmail(body, nil, nil, nil, cidContext(testBrand))
	require.NoError(t, err)
	assert.Contains(t, r.HTML, "background-color:"+testBrand.Accent)
	assert.Contains(t, r.HTML, "color:"+testBrand.OnAccent)
	assert.Contains(t, r.HTML, `<h1 class="cib-h"`)
	assert.NotContains(t, r.HTML, "theme")
}

func TestRenderEmail_ExplicitHexStillWins(t *testing.T) {
	body := json.RawMessage(`[{"type":"button","label":"Go","url":"https://x"}]`)
	r, err := render.RenderEmail(body, json.RawMessage(`{"cta_bg_color":"#000000"}`), nil, nil, cidContext(testBrand))
	require.NoError(t, err)
	assert.Contains(t, r.HTML, "background-color:#000000")
}

func TestRenderEmail_LogoBlock(t *testing.T) {
	body := json.RawMessage(`[{"type":"logo","align":"center","width_px":64}]`)
	r, err := render.RenderEmail(body, nil, nil, nil, cidContext(testBrand))
	require.NoError(t, err)
	assert.Contains(t, r.HTML, `<div style="text-align:center;margin:0 0 24px"><img src="cid:logo@cybericebox" alt="Cyber ICE Box" width="64"`)
	assert.Equal(t, []render.Asset{{Kind: render.AssetLogo}}, r.Assets)
}

func TestRenderEmail_LogoWidthDefaultAndClamp(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{`[{"type":"logo"}]`, `width="64"`},
		{`[{"type":"logo","width_px":2}]`, `width="16"`},
		{`[{"type":"logo","width_px":5000}]`, `width="600"`},
	} {
		r, err := render.RenderEmail(json.RawMessage(tc.in), nil, nil, nil, cidContext(testBrand))
		require.NoError(t, err)
		assert.Contains(t, r.HTML, tc.want, tc.in)
		assert.Contains(t, r.HTML, `text-align:left`, tc.in)
	}
}

func TestRenderEmail_ImageWithFileIDUsesAssetOnce(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	img := `{"type":"image","file_id":"` + id.String() + `","alt":"pic"}`
	body := json.RawMessage(`[` + img + `,{"type":"logo"},` + img + `]`)
	r, err := render.RenderEmail(body, nil, nil, nil, cidContext(testBrand))
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(r.HTML, `src="cid:`+id.String()+`@cybericebox"`))
	assert.Equal(t, []render.Asset{{Kind: render.AssetFile, FileID: id}, {Kind: render.AssetLogo}}, r.Assets)
}

func TestRenderEmail_ImageURLOnlyHasNoAsset(t *testing.T) {
	body := json.RawMessage(`[{"type":"image","url":"https://cdn.example.com/a.png"}]`)
	r, err := render.RenderEmail(body, nil, nil, nil, cidContext(testBrand))
	require.NoError(t, err)
	assert.Contains(t, r.HTML, `src="https://cdn.example.com/a.png"`)
	assert.Empty(t, r.Assets)
}

func TestRenderEmail_AssetInsidePresetCollected(t *testing.T) {
	body := json.RawMessage(`[{"type":"preset","preset_id":"p1"}]`)
	presets := map[string]json.RawMessage{"p1": json.RawMessage(`[{"type":"logo"}]`)}
	r, err := render.RenderEmail(body, nil, presets, nil, cidContext(testBrand))
	require.NoError(t, err)
	assert.Equal(t, []render.Asset{{Kind: render.AssetLogo}}, r.Assets)
}

func TestRenderEmail_PreviewAssetSrc(t *testing.T) {
	rc := render.RenderContext{Brand: testBrand, AssetSrc: func(a render.Asset) string {
		return "https://api.example.com/logo?x=1&y=2"
	}}
	r, err := render.RenderEmail(json.RawMessage(`[{"type":"logo"}]`), nil, nil, nil, rc)
	require.NoError(t, err)
	assert.Contains(t, r.HTML, `src="https://api.example.com/logo?x=1&amp;y=2"`)
}

func TestCID(t *testing.T) {
	id := uuid.Must(uuid.FromString("0190a3c4-0000-7000-8000-000000000001"))
	assert.Equal(t, "logo@cybericebox", render.CID(render.Asset{Kind: render.AssetLogo}))
	assert.Equal(t, "0190a3c4-0000-7000-8000-000000000001@cybericebox", render.CID(render.Asset{Kind: render.AssetFile, FileID: id}))
}

func TestValidateStyling(t *testing.T) {
	assert.Error(t, render.ValidateStyling(json.RawMessage(`{"cta_bg_color":"theme:nope"}`)))
	assert.Error(t, render.ValidateStyling(json.RawMessage(`not json`)))
	assert.NoError(t, render.ValidateStyling(json.RawMessage(`{"cta_bg_color":"#fff"}`)))
	assert.NoError(t, render.ValidateStyling(json.RawMessage(`{"cta_bg_color":"theme:accent","text_color":"theme:on_accent","heading_color":"theme:brand"}`)))
	assert.NoError(t, render.ValidateStyling(nil))
	assert.NoError(t, render.ValidateStyling(json.RawMessage(`null`)))
	assert.NoError(t, render.ValidateStyling(json.RawMessage(`{}`)))
}

// The admin editor (Lexical) serialises element nodes with a string `format`
// (text alignment: "", "left", "center", ...) and text nodes with a numeric
// bitmask. Both shapes must render.
func TestRenderBlocks_LexicalElementFormatString(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","format":"","indent":0,"version":1,"direction":null,"children":[` +
		`{"type":"paragraph","format":"center","indent":0,"version":1,"direction":"ltr","textFormat":0,"children":[` +
		`{"type":"text","text":"Hello","format":1,"detail":0,"mode":"normal","style":"","version":1}]},` +
		`{"type":"heading","tag":"h2","format":"","children":[{"type":"text","text":"Title","format":0}]}]}}}]`)
	html, err := renderBlocks(body, json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.Contains(t, html, "<strong>Hello</strong>")
	assert.Contains(t, html, "Title")
}

// The editor stores a variable chip's formatting as a string list
// (`formats`), not the text bitmask; it must wrap the substituted value the
// same way a formatted text node is wrapped.
func TestRenderBlocks_VariableFormats(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","format":"","children":[
	    {"type":"variable","varName":"event_name","formats":["bold","italic"]},
	    {"type":"text","text":" / "},
	    {"type":"variable","varName":"event_name"}
	  ]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, map[string]any{"event_name": "CTF <1>"})
	require.NoError(t, err)
	assert.Contains(t, out, "<em><strong>CTF &lt;1&gt;</strong></em> / CTF &lt;1&gt;")
}

func TestRenderBlocks_Facts(t *testing.T) {
	body := json.RawMessage(`[{"type":"facts","items":[
	  {"label":"Захід","value":"{{event_name}}"},{"label":"Команда","value":"{{team}}"}]}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, map[string]any{"event_name": "CTF <1>", "team": ""})
	require.NoError(t, err)
	assert.Contains(t, out, "Захід")
	assert.Contains(t, out, "CTF &lt;1&gt;")
	assert.NotContains(t, out, "Команда", "a row with an empty value is dropped")

	out, err = renderBlocks(body, json.RawMessage(`{}`), nil, map[string]any{})
	require.NoError(t, err)
	assert.Empty(t, out, "a card with no rows disappears")
}

func TestRenderBlocks_EmptyVariableDropsParagraph(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
	  {"type":"paragraph","children":[{"type":"text","text":"Вітаємо, ","format":0},{"type":"variable","varName":"Name"}]},
	  {"type":"paragraph","children":[{"type":"link","url":"{{url}}","children":[{"type":"text","text":"ЛІНК","format":0}]}]},
	  {"type":"paragraph","children":[{"type":"text","text":"Кінець","format":0}]}]}}}]`)
	out, err := renderBlocks(body, json.RawMessage(`{}`), nil, map[string]any{"Name": "", "url": ""})
	require.NoError(t, err)
	assert.NotContains(t, out, "Вітаємо")
	assert.NotContains(t, out, "ЛІНК")
	assert.Contains(t, out, "Кінець")
}

func TestRenderBlocks_ButtonWithoutURLIsSkipped(t *testing.T) {
	out, err := renderBlocks(json.RawMessage(`[{"type":"button","label":"Go","url":"{{u}}"}]`), json.RawMessage(`{}`), nil, map[string]any{})
	require.NoError(t, err)
	assert.Empty(t, out)
}
