package mailModel

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var testVars = FooterValues("org@uni.edu", "https://cybericebox.com")

func resolved(t *testing.T, doc json.RawMessage, vars map[string]string) (json.RawMessage, bool) {
	t.Helper()
	return ResolveFooterDoc(doc, vars)
}

func TestDefaultFooterDoc_ResolvesToTheOldText(t *testing.T) {
	doc, ok := resolved(t, DefaultFooterDoc(LanguageUK), testVars)
	require.True(t, ok)
	require.Equal(t, []string{
		"Цей лист надіслано автоматично. З питань відповідайте на нього або пишіть на org@uni.edu.",
		"Cyber ICE Box · cybericebox.com · Політика конфіденційності: cybericebox.com/privacy",
	}, FooterPlainLines(doc))
}

func TestDefaultFooterDoc_English(t *testing.T) {
	doc, ok := resolved(t, DefaultFooterDoc(LanguageEN), FooterValues("support@cybericebox.com", "https://cybericebox.com"))
	require.True(t, ok)
	require.Equal(t, []string{
		"This email was sent automatically. Reply to it or write to support@cybericebox.com.",
		"Cyber ICE Box · cybericebox.com · Privacy Policy: cybericebox.com/privacy",
	}, FooterPlainLines(doc))
}

func TestResolveFooterDoc_UrlVariablesBecomeLinks(t *testing.T) {
	doc, _ := resolved(t, DefaultFooterDoc(LanguageUK), testVars)
	s := string(doc)
	require.Contains(t, s, `"url":"mailto:org@uni.edu"`)
	require.Contains(t, s, `"url":"https://cybericebox.com"`)
	require.Contains(t, s, `"url":"https://cybericebox.com/privacy"`)
	require.NotContains(t, s, `"variable"`)
}

func TestResolveFooterDoc_ParagraphWithEmptyVariableIsLeftOut(t *testing.T) {
	vars := FooterValues("", "https://cybericebox.com")
	doc, ok := resolved(t, DefaultFooterDoc(LanguageUK), vars)
	require.True(t, ok)
	require.Len(t, FooterPlainLines(doc), 1, "the {reply_to} paragraph is dropped")

	_, ok = resolved(t, DefaultFooterDoc(LanguageUK), FooterValues("", ""))
	require.False(t, ok, "no site and no support: nothing but the name is left, and it shares a paragraph with the site")
}

func TestResolveFooterDoc_VariableFormatsBecomeTextFormat(t *testing.T) {
	src := json.RawMessage(`{"root":{"type":"root","children":[{"type":"paragraph","children":[` +
		`{"type":"variable","varName":"platform_name","formats":["bold","italic"]}]}]}}`)
	doc, ok := resolved(t, src, testVars)
	require.True(t, ok)
	require.Contains(t, string(doc), `"format":3`)
	require.Contains(t, string(doc), `"text":"Cyber ICE Box"`)
}

func TestFooterPlainLines(t *testing.T) {
	src := json.RawMessage(`{"root":{"type":"root","children":[{"type":"paragraph","children":[` +
		`{"type":"text","text":"a "},{"type":"link","url":"https://x.y","children":[{"type":"text","text":"b"}]},` +
		`{"type":"linebreak"},{"type":"link","url":"mailto:c@d.e","children":[{"type":"text","text":"c@d.e"}]}]}]}}`)
	require.Equal(t, []string{"a b (https://x.y)\nc@d.e"}, FooterPlainLines(src))
}

func TestLegacyFooterDoc(t *testing.T) {
	doc := LegacyFooterDoc("{platform_name}: [Write us](mailto:{reply_to}) {nope}\n\n[click](javascript:alert(1))")
	out, ok := resolved(t, doc, testVars)
	require.True(t, ok)
	require.Equal(t, []string{"Cyber ICE Box: Write us (mailto:org@uni.edu) {nope}", "click)"}, FooterPlainLines(out))
	require.NotContains(t, string(out), "javascript")
}

func TestStoredFooterDoc_TolerantOfBothFormats(t *testing.T) {
	require.Nil(t, StoredFooterDoc(nil, "  "))
	doc := StoredFooterDoc(nil, "{site_url}")
	require.Contains(t, string(doc), `"varName":"site_url"`)
	saved := json.RawMessage(`{"root":{"type":"root","children":[]}}`)
	require.JSONEq(t, string(saved), string(StoredFooterDoc(saved, "{site_url}")), "the document wins over the legacy text")
}

func TestNormalizeFooterDoc(t *testing.T) {
	valid := json.RawMessage(`{"root":{"type":"root","children":[{"type":"paragraph","children":[` +
		`{"type":"text","text":"Hello "},{"type":"variable","varName":"site_url"},` +
		`{"type":"link","url":"{{privacy_url}}","children":[{"type":"text","text":"Privacy"}]}]}]}}`)
	got, err := NormalizeFooterDoc(valid)
	require.NoError(t, err)
	require.JSONEq(t, string(valid), string(got))

	for name, raw := range map[string]string{
		"not json":         `hello`,
		"no root":          `{"x":1}`,
		"unknown variable": `{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"variable","varName":"nope"}]}]}}`,
		"unknown node":     `{"root":{"type":"root","children":[{"type":"code","children":[]}]}}`,
		"script link":      `{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"link","url":"javascript:alert(1)","children":[]}]}]}}`,
		"unknown token":    `{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"link","url":"{{nope}}","children":[]}]}]}}`,
		"too long":         `{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"` + strings.Repeat("я", MaxFooterBytes) + `"}]}]}}`,
	} {
		_, err := NormalizeFooterDoc(json.RawMessage(raw))
		require.ErrorIs(t, err, ErrFooterInvalid.Err(), name)
	}
}

func TestNormalizeFooterDoc_EmptyAndDefaultAreStoredAsNil(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"nothing": nil, "null": json.RawMessage(`null`),
		"no blocks":       json.RawMessage(`{"root":{"type":"root","children":[]}}`),
		"blank paragraph": json.RawMessage(`{"root":{"type":"root","children":[{"type":"paragraph","children":[]}]}}`),
		"uk default":      DefaultFooterDoc(LanguageUK),
		"en default":      DefaultFooterDoc(LanguageEN),
	} {
		got, err := NormalizeFooterDoc(raw)
		require.NoError(t, err, name)
		require.Nil(t, got, name)
	}
}

func TestDefaultFooterDocIsValid(t *testing.T) {
	for _, lang := range []Language{LanguageUK, LanguageEN} {
		doc, reason := parseFooterDoc(DefaultFooterDoc(lang))
		require.Empty(t, reason, lang)
		count := 0
		require.Empty(t, checkFooterNodes(doc["children"], 0, &count), lang)
	}
}

func TestFooter_Append(t *testing.T) {
	f := Footer{HTML: "<div>f</div>", Text: "—\nf"}
	html, text := f.Append("<p>body</p>", "body")
	require.Equal(t, "<p>body</p><div>f</div>", html)
	require.Equal(t, "body\n\n—\nf", text)

	_, text = f.Append("<p></p>", "")
	require.Equal(t, "—\nf", text, "an empty body still gets the text part")
}

func TestPlatformDefaults(t *testing.T) {
	id := Identity{FromAddress: "notifications@mail.cybericebox.com"}.WithPlatformDefaults("cybericebox.com")
	require.Equal(t, Identity{FromName: "Cyber ICE Box", FromAddress: "notifications@mail.cybericebox.com", ReplyToAddress: "support@cybericebox.com"}, id)

	kept := Identity{FromName: "CIB", FromAddress: "n@mail.x.y", ReplyToAddress: "help@x.y"}.WithPlatformDefaults("cybericebox.com")
	require.Equal(t, Identity{FromName: "CIB", FromAddress: "n@mail.x.y", ReplyToAddress: "help@x.y"}, kept)

	require.Empty(t, Identity{}.WithPlatformDefaults("").ReplyToAddress, "no domain → no support fallback")
}

func TestSupportAddress(t *testing.T) {
	require.Equal(t, "support@cybericebox.com", SupportAddress("cybericebox.com"))
	require.Empty(t, SupportAddress(""))
}

func TestPlatformSite(t *testing.T) {
	require.Equal(t, "https://cybericebox.com", PlatformSite("cybericebox.com"))
	require.Empty(t, PlatformSite(""))
}

func TestFooter_AppendEmptyFooterKeepsBody(t *testing.T) {
	html, text := Footer{}.Append("<p>b</p>", "b")
	require.Equal(t, "<p>b</p>", html)
	require.Equal(t, "b", text)
}

// The built-in footers follow the owner's wording: the automatic-mail line,
// the platform line with the bare site and privacy address, no banned spelling.
func TestDefaultFooters_Wording(t *testing.T) {
	banned := []string{"noreply", "Питання?", "подія", "імейл", "кукі"}
	for lang, text := range defaultFooterTexts {
		for _, word := range banned {
			require.NotContains(t, strings.ToLower(text), strings.ToLower(word), string(lang))
		}
		require.Contains(t, text, "{reply_to}")
		require.Contains(t, text, "{privacy_url}")
	}
}
