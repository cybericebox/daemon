package render

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/gofrs/uuid"
)

// ── Block structs ─────────────────────────────────────────────────────────────

type block struct {
	Type     string          `json:"type"`
	Content  json.RawMessage `json:"content,omitempty"`
	Label    string          `json:"label,omitempty"`
	URL      string          `json:"url,omitempty"`
	Align    string          `json:"align,omitempty"`
	Alt      string          `json:"alt,omitempty"`
	PresetID string          `json:"preset_id,omitempty"`
	Name     string          `json:"name,omitempty"`
	WidthPct int             `json:"width_pct,omitempty"`
	FileID   string          `json:"file_id,omitempty"`
	WidthPx  int             `json:"width_px,omitempty"`
	Items    []factItem      `json:"items,omitempty"`
}

// factItem is one label/value row of a facts card. Label and Value may hold
// {{variables}}; a row whose value ends up empty is left out.
type factItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ── Styling struct (§5.2) ─────────────────────────────────────────────────────

type emailStyling struct {
	FontFamily            string `json:"font_family"`
	TextColor             string `json:"text_color"`
	TextFontSize          string `json:"text_font_size"`
	TextLineHeight        string `json:"text_line_height"`
	HeadingColor          string `json:"heading_color"`
	HeadingLineHeight     string `json:"heading_line_height"`
	ParagraphBottomMargin string `json:"paragraph_bottom_margin"`
	HeadingTopMargin      string `json:"heading_top_margin"`
	HeadingBottomMargin   string `json:"heading_bottom_margin"`
	CtaBgColor            string `json:"cta_bg_color"`
	CtaTextColor          string `json:"cta_text_color"`
	CtaBorderRadius       string `json:"cta_border_radius"`
	CtaFontSize           string `json:"cta_font_size"`
	CtaVerticalPadding    string `json:"cta_vertical_padding"`
	CtaHorizontalPadding  string `json:"cta_horizontal_padding"`
}

// fields lists every styling value, so token resolution covers new keys too.
func (st *emailStyling) fields() []*string {
	return []*string{
		&st.FontFamily, &st.TextColor, &st.TextFontSize, &st.TextLineHeight,
		&st.HeadingColor, &st.HeadingLineHeight, &st.ParagraphBottomMargin,
		&st.HeadingTopMargin, &st.HeadingBottomMargin, &st.CtaBgColor,
		&st.CtaTextColor, &st.CtaBorderRadius, &st.CtaFontSize,
		&st.CtaVerticalPadding, &st.CtaHorizontalPadding,
	}
}

// defaultEmailStyling colours use theme:* tokens so an unstyled template
// follows the brand context it is rendered with.
func defaultEmailStyling() emailStyling {
	return emailStyling{
		FontFamily:            "-apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Helvetica,Arial,sans-serif",
		TextColor:             "#24263A",
		TextFontSize:          "16px",
		TextLineHeight:        "1.6",
		HeadingColor:          "#17182B",
		HeadingLineHeight:     "1.3",
		ParagraphBottomMargin: "16px",
		HeadingTopMargin:      "0",
		HeadingBottomMargin:   "12px",
		CtaBgColor:            "theme:accent",
		CtaTextColor:          "theme:on_accent",
		CtaBorderRadius:       "8px",
		CtaFontSize:           "16px",
		CtaVerticalPadding:    "14px",
		CtaHorizontalPadding:  "28px",
	}
}

func mergeEmailStyling(raw json.RawMessage) (emailStyling, error) {
	st := defaultEmailStyling()
	if len(raw) == 0 || string(raw) == "null" {
		return st, nil
	}
	// Unmarshal into a map so only provided keys override defaults.
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return st, fmt.Errorf("render: invalid styling JSON: %w", err)
	}
	if v, ok := m["font_family"]; ok {
		st.FontFamily = v
	}
	if v, ok := m["text_color"]; ok {
		st.TextColor = v
	}
	if v, ok := m["text_font_size"]; ok {
		st.TextFontSize = v
	}
	if v, ok := m["text_line_height"]; ok {
		st.TextLineHeight = v
	}
	if v, ok := m["heading_color"]; ok {
		st.HeadingColor = v
	}
	if v, ok := m["heading_line_height"]; ok {
		st.HeadingLineHeight = v
	}
	if v, ok := m["paragraph_bottom_margin"]; ok {
		st.ParagraphBottomMargin = v
	}
	if v, ok := m["heading_top_margin"]; ok {
		st.HeadingTopMargin = v
	}
	if v, ok := m["heading_bottom_margin"]; ok {
		st.HeadingBottomMargin = v
	}
	if v, ok := m["cta_bg_color"]; ok {
		st.CtaBgColor = v
	}
	if v, ok := m["cta_text_color"]; ok {
		st.CtaTextColor = v
	}
	if v, ok := m["cta_border_radius"]; ok {
		st.CtaBorderRadius = v
	}
	if v, ok := m["cta_font_size"]; ok {
		st.CtaFontSize = v
	}
	if v, ok := m["cta_vertical_padding"]; ok {
		st.CtaVerticalPadding = v
	}
	if v, ok := m["cta_horizontal_padding"]; ok {
		st.CtaHorizontalPadding = v
	}
	return st, nil
}

// ── Lexical node structs ──────────────────────────────────────────────────────

type lexicalRoot struct {
	Root lexicalNode `json:"root"`
}

type lexicalNode struct {
	Type     string        `json:"type"`
	Tag      string        `json:"tag,omitempty"`
	Text     string        `json:"text,omitempty"`
	Format   lexicalFormat `json:"format,omitempty"`
	URL      string        `json:"url,omitempty"`
	VarName  string        `json:"varName,omitempty"`
	Formats  []string      `json:"formats,omitempty"` // variable nodes only
	ListType string        `json:"listType,omitempty"`
	Children []lexicalNode `json:"children,omitempty"`
}

// Lexical text-format bits.
const (
	formatBold          lexicalFormat = 1
	formatItalic        lexicalFormat = 2
	formatStrikethrough lexicalFormat = 4
	formatUnderline     lexicalFormat = 8
	formatCode          lexicalFormat = 16
)

// formatsMask converts a variable node's format names (the editor stores a
// variable chip's formatting as a list, not a bitmask) into the text bitmask.
func formatsMask(names []string) lexicalFormat {
	var f lexicalFormat
	for _, name := range names {
		switch name {
		case "bold":
			f |= formatBold
		case "italic":
			f |= formatItalic
		case "strikethrough":
			f |= formatStrikethrough
		case "underline":
			f |= formatUnderline
		case "code":
			f |= formatCode
		}
	}
	return f
}

// wrapTextFormat wraps escaped text in the tags of its format bitmask, in a
// fixed order (bold innermost, code outermost).
func wrapTextFormat(s string, f lexicalFormat) string {
	if f&formatBold != 0 {
		s = "<strong>" + s + "</strong>"
	}
	if f&formatItalic != 0 {
		s = "<em>" + s + "</em>"
	}
	if f&formatStrikethrough != 0 {
		s = "<s>" + s + "</s>"
	}
	if f&formatUnderline != 0 {
		s = "<u>" + s + "</u>"
	}
	if f&formatCode != 0 {
		s = "<code>" + s + "</code>"
	}
	return s
}

// lexicalFormat is the text-format bitmask of a Lexical text node. Lexical
// reuses the `format` key on element nodes (paragraph, heading, root) for the
// alignment string ("", "left", "center", ...), which carries no bitmask and
// reads as 0.
type lexicalFormat int

func (f *lexicalFormat) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		*f = 0
		return nil
	}
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*f = lexicalFormat(n)
	return nil
}

// ── Public API ────────────────────────────────────────────────────────────────

// RenderEmail converts a block array (body) + optional styling JSON into an
// HTML string and the assets it references. Pure function; no IO.
//
// body/preset values = JSON array of blocks.
// styling = JSON object merged over server-side defaults (§5.2 keys); theme:*
// colour tokens resolve against rc.Brand.
// presets = map[preset_id]json.RawMessage block array; missing id → empty string.
// vars = substitution map for {{key}} in button URLs and variable nodes.
func RenderEmail(
	body json.RawMessage,
	stylingRaw json.RawMessage,
	presets map[string]json.RawMessage,
	vars map[string]any,
	rc RenderContext,
) (Rendered, error) {
	st, err := mergeEmailStyling(stylingRaw)
	if err != nil {
		return Rendered{}, err
	}
	resolveTokens(&st, rc.Brand)
	r := &renderer{logoAlt: rc.LogoAlt, st: st, presets: presets, vars: vars, assetSrc: rc.AssetSrc, seen: map[Asset]bool{}}
	if r.assetSrc == nil {
		r.assetSrc = func(a Asset) string { return "cid:" + CID(a) }
	}
	out, err := r.renderBlockSlice(body)
	if err != nil {
		return Rendered{}, err
	}
	return Rendered{HTML: out, Assets: r.assets}, nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// renderer holds one RenderEmail call's inputs and collects referenced assets.
type renderer struct {
	st       emailStyling
	logoAlt  string
	presets  map[string]json.RawMessage
	vars     map[string]any
	assetSrc func(Asset) string
	assets   []Asset
	seen     map[Asset]bool
	// depth is how many presets deep the block being rendered is; blocks counts every block rendered.
	depth, blocks int
}

// src records a (unique, first-use ordered) asset and returns its <img src>.
func (r *renderer) src(a Asset) string {
	if !r.seen[a] {
		r.seen[a] = true
		r.assets = append(r.assets, a)
	}
	return r.assetSrc(a)
}

const (
	logoAlt          = "Cyber ICE Box"
	logoDefaultWidth = 64
	logoMinWidth     = 16
	logoMaxWidth     = 600
)

// maxPresetDepth is how many presets may nest; maxRenderedBlocks bounds the blocks of one email, however
// they are nested.
const (
	maxPresetDepth    = 3
	maxRenderedBlocks = 2000
)

func (r *renderer) renderBlockSlice(raw json.RawMessage) (string, error) {
	var blocks []block
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", fmt.Errorf("render: invalid block array: %w", err)
	}
	var sb strings.Builder
	for _, b := range blocks {
		if r.blocks++; r.blocks > maxRenderedBlocks {
			return "", fmt.Errorf("render: more than %d blocks", maxRenderedBlocks)
		}
		s, err := r.renderBlock(b)
		if err != nil {
			return "", err
		}
		sb.WriteString(s)
	}
	return sb.String(), nil
}

func (r *renderer) renderBlock(b block) (string, error) {
	st, vars := r.st, r.vars
	switch b.Type {
	case "divider":
		return `<div class="cib-rule" style="height:1px;line-height:1px;font-size:1px;background-color:#E3E5EC;margin:24px 0">&nbsp;</div>`, nil

	case "logo":
		width := b.WidthPx
		if width == 0 {
			width = logoDefaultWidth
		}
		width = min(max(width, logoMinWidth), logoMaxWidth)
		return fmt.Sprintf(
			`<div style="text-align:%s;margin:0 0 24px"><img src="%s" alt="%s" width="%d" style="width:%dpx;max-width:100%%;height:auto;border:0;display:inline-block"/></div>`,
			safeAlign(b.Align), html.EscapeString(r.src(Asset{Kind: AssetLogo})), html.EscapeString(cmpOr(r.logoAlt, logoAlt)), width, width,
		), nil

	case "image":
		align := safeAlign(b.Align) // allowlist: left|center|right|justify
		width := "100%"
		if b.WidthPct > 0 {
			width = fmt.Sprintf("%d%%", b.WidthPct)
		}
		// An uploaded file (file_id) wins over an external url; an unparsable
		// file_id falls back to the url.
		src := safeURL(b.URL)
		if id, err := uuid.FromString(b.FileID); err == nil && !id.IsNil() {
			src = r.src(Asset{Kind: AssetFile, FileID: id})
		}
		return fmt.Sprintf(
			`<div style="text-align:%s"><img src="%s" alt="%s" style="width:%s"/></div>`,
			align, html.EscapeString(src), html.EscapeString(b.Alt), width,
		), nil

	case "button":
		url := substitute(b.URL, vars)
		if strings.TrimSpace(url) == "" {
			return "", nil // no link, no button
		}
		label := html.EscapeString(b.Label)
		padding := fmt.Sprintf("%s %s", safeCSSValue(st.CtaVerticalPadding), safeCSSValue(st.CtaHorizontalPadding))
		bg, fg := safeCSSValue(st.CtaBgColor), safeCSSValue(st.CtaTextColor)
		linkStyle := fmt.Sprintf(
			"display:inline-block;background-color:%s;color:%s;border-radius:%s;font-family:%s;font-size:%s;font-weight:600;line-height:1.2;padding:%s;text-decoration:none",
			bg, fg, safeCSSValue(st.CtaBorderRadius), safeCSSValue(st.FontFamily), safeCSSValue(st.CtaFontSize), padding,
		)
		align := safeAlign(b.Align) // allowlist: left|center|right|justify
		// A table cell carries the fill, so the button keeps its colour and
		// shape in clients that ignore padding or background on links.
		return fmt.Sprintf(
			`<table role="presentation" width="100%%" cellspacing="0" cellpadding="0" border="0"><tr><td align="%s" style="text-align:%s;padding:8px 0 24px"><table role="presentation" cellspacing="0" cellpadding="0" border="0"><tr><td class="cib-btn" bgcolor="%s" style="background-color:%s;border-radius:%s"><a class="cib-btn" href="%s" style="%s">%s</a></td></tr></table></td></tr></table>`,
			align, align, bg, bg, safeCSSValue(st.CtaBorderRadius), html.EscapeString(safeURL(url)), linkStyle, label,
		), nil

	case "facts":
		return r.renderFacts(b), nil

	case "preset":
		if r.presets == nil {
			return "", nil
		}
		presetRaw, ok := r.presets[b.PresetID]
		if !ok {
			return "", nil // missing preset → render nothing, no error
		}
		// A preset may use another preset, but a loop (A uses itself, A and B use each other) must not take
		// the process down: the nesting is cut at a fixed depth.
		if r.depth >= maxPresetDepth {
			return "", nil
		}
		r.depth++
		out, err := r.renderBlockSlice(presetRaw)
		r.depth--
		return out, err

	case "rich_text":
		return renderLexical(b.Content, st, vars)

	default:
		return "", nil
	}
}

// renderLexical walks a Lexical EditorState content blob and produces HTML.
func renderLexical(content json.RawMessage, st emailStyling, vars map[string]any) (string, error) {
	if len(content) == 0 {
		return "", nil
	}
	var root lexicalRoot
	if err := json.Unmarshal(content, &root); err != nil {
		return "", fmt.Errorf("render: invalid lexical content: %w", err)
	}
	return renderLexicalChildren(root.Root.Children, st, vars)
}

func renderLexicalChildren(children []lexicalNode, st emailStyling, vars map[string]any) (string, error) {
	var sb strings.Builder
	for _, c := range children {
		s, err := renderLexicalNode(c, st, vars)
		if err != nil {
			return "", err
		}
		sb.WriteString(s)
	}
	return sb.String(), nil
}

// usesEmptyVariable reports whether any variable node below nodes has no value.
func usesEmptyVariable(nodes []lexicalNode, vars map[string]any) bool {
	for _, n := range nodes {
		// A link whose address is a variable with no value is a dead link.
		if n.Type == "link" && substitutePat.MatchString(n.URL) && strings.TrimSpace(substitute(n.URL, vars)) == "" {
			return true
		}
		if n.Type == "variable" {
			v, ok := vars[n.VarName]
			if !ok || v == nil || strings.TrimSpace(fmt.Sprint(v)) == "" {
				return true
			}
		}
		if usesEmptyVariable(n.Children, vars) {
			return true
		}
	}
	return false
}

func renderLexicalNode(n lexicalNode, st emailStyling, vars map[string]any) (string, error) {
	switch n.Type {
	case "paragraph":
		// A paragraph that uses a variable with no value is dropped whole, so
		// an optional value never leaves «Вітаємо, !» behind.
		if usesEmptyVariable(n.Children, vars) {
			return "", nil
		}
		inner, err := renderLexicalChildren(n.Children, st, vars)
		if err != nil {
			return "", err
		}
		style := fmt.Sprintf(
			"font-family:%s;color:%s;font-size:%s;line-height:%s;margin:0 0 %s",
			safeCSSValue(st.FontFamily), safeCSSValue(st.TextColor),
			safeCSSValue(st.TextFontSize), safeCSSValue(st.TextLineHeight),
			safeCSSValue(st.ParagraphBottomMargin),
		)
		return fmt.Sprintf(`<p class="cib-t" style="%s">%s</p>`, style, inner), nil

	case "heading":
		tag := safeHeadingTag(n.Tag) // allowlist: h1-h6; unknown → h2
		inner, err := renderLexicalChildren(n.Children, st, vars)
		if err != nil {
			return "", err
		}
		style := fmt.Sprintf(
			"font-family:%s;font-size:%s;font-weight:700;color:%s;line-height:%s;margin:%s 0 %s",
			safeCSSValue(st.FontFamily), headingSize(tag), safeCSSValue(st.HeadingColor), safeCSSValue(st.HeadingLineHeight),
			safeCSSValue(st.HeadingTopMargin), safeCSSValue(st.HeadingBottomMargin),
		)
		return fmt.Sprintf(`<%s class="cib-h" style="%s">%s</%s>`, tag, style, inner, tag), nil

	case "quote":
		inner, err := renderLexicalChildren(n.Children, st, vars)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`<blockquote>%s</blockquote>`, inner), nil

	case "list":
		tag := "ul"
		if n.ListType == "number" {
			tag = "ol"
		}
		inner, err := renderLexicalChildren(n.Children, st, vars)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`<%s>%s</%s>`, tag, inner, tag), nil

	case "listitem":
		inner, err := renderLexicalChildren(n.Children, st, vars)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`<li>%s</li>`, inner), nil

	case "linebreak":
		return `<br/>`, nil

	case "link":
		inner, err := renderLexicalChildren(n.Children, st, vars)
		if err != nil {
			return "", err
		}
		// Substitute {{Var}} in the href (parity with button URLs) BEFORE the
		// scheme allowlist, so an inline link can carry a dynamic URL and a
		// dangerous substituted value is still sanitized to "#".
		url := substitute(n.URL, vars)
		return fmt.Sprintf(`<a class="cib-a" href="%s" style="color:#3340C4;text-decoration:underline">%s</a>`, html.EscapeString(safeURL(url)), inner), nil

	case "text":
		return wrapTextFormat(html.EscapeString(n.Text), n.Format), nil

	case "variable":
		val := ""
		if vars != nil {
			if v, ok := vars[n.VarName]; ok {
				val = fmt.Sprint(v)
			}
		}
		return wrapTextFormat(html.EscapeString(val), formatsMask(n.Formats)), nil

	default:
		// Unknown node type — recurse into children if present.
		return renderLexicalChildren(n.Children, st, vars)
	}
}

// ── Security helpers ──────────────────────────────────────────────────────────

// headingSize is the type scale of headings: 24 / 20 / 17 px, then body size.
func headingSize(tag string) string {
	switch tag {
	case "h1":
		return "24px"
	case "h2":
		return "20px"
	case "h3":
		return "17px"
	default:
		return "16px"
	}
}

// renderFacts renders a key-facts card: stacked label/value pairs on a tinted
// panel. Rows with an empty value are dropped, and a card with no rows
// disappears.
func (r *renderer) renderFacts(b block) string {
	st := r.st
	var rows strings.Builder
	for _, it := range b.Items {
		value := strings.TrimSpace(substitute(it.Value, r.vars))
		if value == "" {
			continue
		}
		label := strings.TrimSpace(substitute(it.Label, r.vars))
		gap := "14px"
		if rows.Len() == 0 {
			gap = "0"
		}
		fmt.Fprintf(&rows,
			`<div style="margin:%s 0 0"><div class="cib-muted" style="font-family:%s;font-size:13px;line-height:1.4;color:#6B7080">%s</div><div class="cib-t" style="font-family:%s;font-size:16px;line-height:1.5;font-weight:600;color:%s;word-break:break-word">%s</div></div>`,
			gap, safeCSSValue(st.FontFamily), html.EscapeString(label),
			safeCSSValue(st.FontFamily), safeCSSValue(st.TextColor), html.EscapeString(value))
	}
	if rows.Len() == 0 {
		return ""
	}
	return `<table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" style="margin:0 0 24px"><tr><td class="cib-fact" bgcolor="#F6F7FB" style="background-color:#F6F7FB;border:1px solid #E3E5EC;border-radius:10px;padding:16px 20px">` +
		rows.String() + `</td></tr></table>`
}

// safeHeadingTag returns tag if it is one of the permitted heading elements
// (h1-h6), else "h2".
func safeHeadingTag(tag string) string {
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return tag
	default:
		return "h2"
	}
}

// safeURL allowlists URL schemes. Only http, https, mailto, root-relative (/),
// and fragment (#) links are permitted. Everything else (e.g. javascript:,
// data:, vbscript:, protocol-relative) is replaced with "#".
func safeURL(u string) string {
	s := strings.TrimSpace(u)
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") ||
		strings.HasPrefix(low, "mailto:") || (strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//")) || strings.HasPrefix(s, "#") {
		return s
	}
	return "#"
}

// safeAlign returns align if it is a safe CSS text-align keyword, else "left".
func safeAlign(align string) string {
	switch align {
	case "left", "center", "right", "justify":
		return align
	default:
		return "left"
	}
}

// safeCSSPat matches any character that is NOT allowed in a CSS property value.
// Allowed: A-Z a-z 0-9 space # . , ( ) % -
// Stripped: " ; : < > and everything else that could break out of an attribute.
var safeCSSPat = regexp.MustCompile(`[^-A-Za-z0-9 #.,()%]`)

// safeCSSValue strips characters outside the CSS-safe allowlist from s.
func safeCSSValue(s string) string {
	return safeCSSPat.ReplaceAllString(s, "")
}

// substitute replaces {{key}}, {{ key }} and the Go-template-style {{.key}} in s
// with fmt.Sprint(vars[key]). The leading dot is optional (authors reach for
// {{.Var}} by habit). If a key is absent from vars, it is replaced with an empty
// string.
var substitutePat = regexp.MustCompile(`\{\{\s*\.?(\w+)\s*\}\}`)

func substitute(s string, vars map[string]any) string {
	return substitutePat.ReplaceAllStringFunc(s, func(match string) string {
		subs := substitutePat.FindStringSubmatch(match)
		if len(subs) < 2 {
			return match
		}
		key := subs[1]
		if vars == nil {
			return ""
		}
		v, ok := vars[key]
		if !ok {
			return ""
		}
		return fmt.Sprint(v)
	})
}

func cmpOr(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// ContainsPreset reports whether a block array uses a preset block anywhere.
func ContainsPreset(raw json.RawMessage) bool {
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "preset" {
			return true
		}
	}
	return false
}
