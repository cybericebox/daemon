package mailModel

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// PlatformName is the product name in the platform sender and the footer.
const PlatformName = "Cyber ICE Box"

// Language picks the footer text. There is no per-recipient language yet, so
// every email uses LanguageUK; unknown values fall back to it as well.
type Language string

const (
	LanguageUK Language = "uk"
	LanguageEN Language = "en"
)

// PlatformSite is the main site URL of the platform.
func PlatformSite(domain string) string {
	if domain == "" {
		return ""
	}
	return "https://" + domain
}

// WithPlatformDefaults fills what the platform sender leaves empty: the
// product name and the support Reply-To (SUPPORT_EMAIL).
func (id Identity) WithPlatformDefaults(supportEmail string) Identity {
	if id.FromName == "" {
		id.FromName = PlatformName
	}
	if id.ReplyToAddress == "" {
		id.ReplyToAddress = supportEmail
	}
	return id
}

// Footer is the text every email ends with, in both MIME parts.
type Footer struct {
	HTML string
	Text string
}

// footerMarker is the slot of the footer in a shelled email document
// (render.FooterMarker; the model cannot import the renderer).
const footerMarker = "<!--cib-footer-->"

// Append adds the footer to the end of both parts of a message body, then
// keeps the product name unbreakable in both (KeepBrandHTML / KeepBrandText).
func (f Footer) Append(htmlBody, textBody string) (string, string) {
	htmlBody, textBody = f.append(htmlBody, textBody)
	return KeepBrandHTML(htmlBody), KeepBrandText(textBody)
}

func (f Footer) append(htmlBody, textBody string) (string, string) {
	if f.HTML == "" && f.Text == "" {
		return strings.Replace(htmlBody, footerMarker, "", 1), textBody
	}
	text := f.Text
	if strings.TrimSpace(textBody) != "" {
		text = textBody + "\n\n" + f.Text
	}
	if i := strings.Index(htmlBody, footerMarker); i >= 0 {
		return htmlBody[:i] + f.HTML + htmlBody[i+len(footerMarker):], text
	}
	return htmlBody + f.HTML, text
}

// The footer is a Lexical document, the same format as the rich_text blocks of
// an email body: text with formatting, links, and variable nodes
// ({"type":"variable","varName":"site_url"}). A variable with no value drops
// its whole paragraph. Footers saved before rich text are markdown-like text
// ({name} variables, [label](target) links); they are read through
// LegacyFooterDoc, so no stored footer needs converting.
const (
	FooterVarPlatformName = "platform_name"
	FooterVarSiteURL      = "site_url"
	FooterVarPrivacyURL   = "privacy_url"
	FooterVarReplyTo      = "reply_to"
)

// FooterVariables lists the variables a footer may use.
var FooterVariables = []string{FooterVarPlatformName, FooterVarSiteURL, FooterVarPrivacyURL, FooterVarReplyTo}

// MaxFooterBytes bounds the stored footer document.
const MaxFooterBytes = 20000

const (
	maxFooterDepth = 8
	maxFooterNodes = 500
)

// defaultFooterTexts are the built-in footers in the legacy text form; the
// document is derived from them.
var defaultFooterTexts = map[Language]string{
	LanguageUK: "Цей лист надіслано автоматично. З питань відповідайте на нього або пишіть на {reply_to}.\n" +
		"{platform_name} · {site_url} · Політика конфіденційності: {privacy_url}",
	LanguageEN: "This email was sent automatically. Reply to it or write to {reply_to}.\n" +
		"{platform_name} · {site_url} · Privacy Policy: {privacy_url}",
}

// DefaultFooterDoc is the built-in footer document for lang (unknown values
// fall back to Ukrainian).
func DefaultFooterDoc(lang Language) json.RawMessage {
	text, ok := defaultFooterTexts[lang]
	if !ok {
		text = defaultFooterTexts[LanguageUK]
	}
	return LegacyFooterDoc(text)
}

// FooterValues are the variable values of a message: the platform site and the
// message Reply-To (support address). The site and privacy values are shown
// without the scheme (cybericebox.com/privacy); a link made of them gets
// https:// back.
func FooterValues(supportEmail, site string) map[string]string {
	site = strings.TrimPrefix(strings.TrimPrefix(site, "https://"), "http://")
	vars := map[string]string{
		FooterVarPlatformName: PlatformName,
		FooterVarSiteURL:      site,
		FooterVarReplyTo:      supportEmail,
	}
	if site != "" {
		vars[FooterVarPrivacyURL] = site + "/privacy"
	}
	return vars
}

// footerLinkTarget is the link address of a URL variable's value: an e-mail
// gets mailto:, a bare address gets https://.
func footerLinkTarget(name, value string) string {
	switch {
	case name == FooterVarReplyTo:
		return "mailto:" + value
	case value == "", strings.Contains(value, "://"):
		return value
	}
	return "https://" + value
}

// StoredFooterDoc is the footer the admin saved: the document, else the legacy
// text converted; nil when none is saved (the default applies).
func StoredFooterDoc(content []byte, legacyText string) json.RawMessage {
	if len(bytes.TrimSpace(content)) > 0 {
		return content
	}
	if strings.TrimSpace(legacyText) != "" {
		return LegacyFooterDoc(legacyText)
	}
	return nil
}

// --- building nodes ---

type node = map[string]any

func textNode(text string, format int) node {
	return node{"type": "text", "version": 1, "text": text, "format": format, "detail": 0, "mode": "normal", "style": ""}
}

func variableNode(name string) node {
	return node{"type": "variable", "version": 1, "varName": name}
}

func linkNode(url string, children []any) node {
	return node{
		"type": "link", "version": 1, "url": url, "rel": "noopener", "target": nil, "title": nil,
		"direction": "ltr", "format": "", "indent": 0, "children": children,
	}
}

func paragraphNode(children []any) node {
	return node{
		"type": "paragraph", "version": 1, "direction": "ltr", "format": "", "indent": 0,
		"textFormat": 0, "textStyle": "", "children": children,
	}
}

func rootDoc(children []any) json.RawMessage {
	raw, _ := json.Marshal(node{"root": node{
		"type": "root", "version": 1, "direction": "ltr", "format": "", "indent": 0, "children": children,
	}})
	return raw
}

var (
	footerVarPattern  = regexp.MustCompile(`\{([a-z_]+)\}`)
	footerLinkPattern = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+)\)`)
	// tokenPattern is a {{variable}} reference inside a link URL.
	tokenPattern = regexp.MustCompile(`\{\{\s*\.?(\w+)\s*\}\}`)
)

// LegacyFooterDoc converts a footer in the text form ({name} variables,
// [label](target) links, one paragraph per line) into a document.
func LegacyFooterDoc(text string) json.RawMessage {
	var blocks []any
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		blocks = append(blocks, paragraphNode(legacyInline(line)))
	}
	if blocks == nil {
		blocks = []any{}
	}
	return rootDoc(blocks)
}

func legacyInline(line string) []any {
	out := []any{}
	last := 0
	for _, loc := range footerLinkPattern.FindAllStringSubmatchIndex(line, -1) {
		out = append(out, legacyText(line[last:loc[0]])...)
		label := legacyText(line[loc[2]:loc[3]])
		target := footerVarPattern.ReplaceAllString(line[loc[4]:loc[5]], "{{$1}}")
		if safeFooterTarget(target) || startsWithKnownToken(target) {
			out = append(out, linkNode(target, label))
		} else {
			out = append(out, label...)
		}
		last = loc[1]
	}
	return append(out, legacyText(line[last:])...)
}

// legacyText splits text at its {name} variables; an unknown name stays text.
func legacyText(s string) []any {
	var out []any
	last := 0
	for _, loc := range footerVarPattern.FindAllStringSubmatchIndex(s, -1) {
		name := s[loc[2]:loc[3]]
		if !slicesContains(FooterVariables, name) {
			continue
		}
		if loc[0] > last {
			out = append(out, textNode(s[last:loc[0]], 0))
		}
		out = append(out, variableNode(name))
		last = loc[1]
	}
	if last < len(s) {
		out = append(out, textNode(s[last:], 0))
	}
	return out
}

func slicesContains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func safeFooterTarget(target string) bool {
	l := strings.ToLower(target)
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "mailto:")
}

func startsWithKnownToken(target string) bool {
	loc := tokenPattern.FindStringSubmatchIndex(target)
	return loc != nil && loc[0] == 0 && slicesContains(FooterVariables, target[loc[2]:loc[3]])
}

// --- validation ---

var footerNodeTypes = map[string]bool{
	"paragraph": true, "heading": true, "quote": true, "list": true, "listitem": true,
	"linebreak": true, "link": true, "text": true, "variable": true,
}

// NormalizeFooterDoc validates a footer document and returns its canonical
// form. Known node types and variables only, links to http(s), mailto or a
// variable. A document with no content, or the same content as the built-in
// default, is stored as nil so the default keeps following the platform.
func NormalizeFooterDoc(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	doc, reason := parseFooterDoc(raw)
	if reason == "" {
		count := 0
		reason = checkFooterNodes(doc["children"], 0, &count)
	}
	if reason != "" {
		return nil, ErrFooterInvalid.WithMessage(reason).Err()
	}
	sig := strings.TrimSpace(footerSignature(doc["children"]))
	if sig == "" || sig == strings.TrimSpace(footerSignature(rootChildren(DefaultFooterDoc(LanguageUK)))) ||
		sig == strings.TrimSpace(footerSignature(rootChildren(DefaultFooterDoc(LanguageEN)))) {
		return nil, nil
	}
	canonical, err := json.Marshal(node{"root": doc})
	if err != nil {
		return nil, ErrFooterInvalid.WithMessage("Footer is not a valid document").Err()
	}
	return canonical, nil
}

func parseFooterDoc(raw []byte) (node, string) {
	if len(raw) > MaxFooterBytes {
		return nil, "Footer is too long"
	}
	var doc node
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, "Footer is not a valid document"
	}
	root, ok := doc["root"].(map[string]any)
	if !ok || root["type"] != "root" {
		return nil, "Footer is not a valid document"
	}
	return root, ""
}

func rootChildren(raw []byte) any {
	root, _ := parseFooterDoc(raw)
	return root["children"]
}

// checkFooterNodes describes what is wrong with a node list, or "".
func checkFooterNodes(children any, depth int, count *int) string {
	list, ok := children.([]any)
	if !ok {
		if children == nil {
			return ""
		}
		return "Footer is not a valid document"
	}
	if depth > maxFooterDepth {
		return "Footer is nested too deeply"
	}
	for _, item := range list {
		n, ok := item.(map[string]any)
		if !ok {
			return "Footer is not a valid document"
		}
		if *count++; *count > maxFooterNodes {
			return "Footer is too long"
		}
		typ, _ := n["type"].(string)
		if !footerNodeTypes[typ] {
			return "Footer uses an unsupported element " + typ
		}
		switch typ {
		case "variable":
			if name, _ := n["varName"].(string); !slicesContains(FooterVariables, name) {
				return "Footer uses an unknown variable {" + name + "}"
			}
		case "link":
			url, _ := n["url"].(string)
			if reason := footerLinkProblem(url); reason != "" {
				return reason
			}
		}
		if reason := checkFooterNodes(n["children"], depth+1, count); reason != "" {
			return reason
		}
	}
	return ""
}

func footerLinkProblem(url string) string {
	for _, m := range tokenPattern.FindAllStringSubmatch(url, -1) {
		if !slicesContains(FooterVariables, m[1]) {
			return "Footer uses an unknown variable {" + m[1] + "}"
		}
	}
	if !safeFooterTarget(url) && !startsWithKnownToken(url) {
		return "Footer link must start with https://, http:// or mailto:"
	}
	return ""
}

// footerSignature is the content of a node list without editor bookkeeping:
// two documents with the same signature read the same.
func footerSignature(children any) string {
	var b strings.Builder
	list, _ := children.([]any)
	for _, item := range list {
		n, _ := item.(map[string]any)
		switch typ, _ := n["type"].(string); typ {
		case "text":
			text, _ := n["text"].(string)
			b.WriteString(text)
		case "variable":
			name, _ := n["varName"].(string)
			b.WriteString("{" + name + "}")
		case "linebreak":
			b.WriteString("\n")
		case "link":
			url, _ := n["url"].(string)
			b.WriteString("[" + url + "|" + footerSignature(n["children"]) + "]")
		default:
			b.WriteString(footerSignature(n["children"]) + "\n")
		}
	}
	return b.String()
}

// --- resolving ---

// Lexical text-format bits.
var formatBits = map[string]int{"bold": 1, "italic": 2, "strikethrough": 4, "underline": 8, "code": 16}

// urlVariables are shown as links when they stand outside a link.
var urlVariables = map[string]bool{FooterVarSiteURL: true, FooterVarPrivacyURL: true, FooterVarReplyTo: true}

// ResolveFooterDoc substitutes the variables of a footer document: variable
// nodes become text, {{name}} in link URLs is replaced, and a URL or e-mail
// variable outside a link becomes a link. A top-level block that uses a
// variable with no value is left out. ok is false when nothing is left to show.
func ResolveFooterDoc(doc []byte, vars map[string]string) (resolved json.RawMessage, ok bool) {
	root, reason := parseFooterDoc(doc)
	if reason != "" {
		return nil, false
	}
	list, _ := root["children"].([]any)
	blocks := []any{}
	for _, item := range list {
		block, isNode := item.(map[string]any)
		if !isNode || usesEmptyVariable(block, vars) {
			continue
		}
		out := resolveNode(block, vars, false)
		if len(out) == 1 && strings.TrimSpace(footerSignature(out)) == "" {
			continue
		}
		blocks = append(blocks, out...)
	}
	return rootDoc(blocks), len(blocks) > 0
}

func usesEmptyVariable(n node, vars map[string]string) bool {
	switch typ, _ := n["type"].(string); typ {
	case "variable":
		name, _ := n["varName"].(string)
		return vars[name] == ""
	case "link":
		url, _ := n["url"].(string)
		for _, m := range tokenPattern.FindAllStringSubmatch(url, -1) {
			if vars[m[1]] == "" {
				return true
			}
		}
	}
	children, _ := n["children"].([]any)
	for _, c := range children {
		if child, ok := c.(map[string]any); ok && usesEmptyVariable(child, vars) {
			return true
		}
	}
	return false
}

func resolveNode(n node, vars map[string]string, inLink bool) []any {
	typ, _ := n["type"].(string)
	if typ == "variable" {
		name, _ := n["varName"].(string)
		format := 0
		formats, _ := n["formats"].([]any)
		for _, f := range formats {
			if s, isStr := f.(string); isStr {
				format |= formatBits[s]
			}
		}
		text := textNode(vars[name], format)
		if inLink || !urlVariables[name] {
			return []any{text}
		}
		url := footerLinkTarget(name, vars[name])
		return []any{linkNode(url, []any{text})}
	}
	out := node{}
	for k, v := range n {
		out[k] = v
	}
	if typ == "link" {
		url, _ := n["url"].(string)
		out["url"] = tokenPattern.ReplaceAllStringFunc(url, func(m string) string {
			name := tokenPattern.FindStringSubmatch(m)[1]
			if urlVariables[name] && m == url {
				// The whole address is the variable: make it a full link.
				return footerLinkTarget(name, vars[name])
			}
			return vars[name]
		})
		inLink = true
	}
	if children, isList := n["children"].([]any); isList {
		resolved := []any{}
		for _, c := range children {
			if child, ok := c.(map[string]any); ok {
				resolved = append(resolved, resolveNode(child, vars, inLink)...)
			}
		}
		out["children"] = resolved
	}
	return []any{out}
}

// FooterPlainLines is the text/plain form of a resolved footer document, one
// line per paragraph; a link shows its address after the label.
func FooterPlainLines(resolved []byte) []string {
	root, reason := parseFooterDoc(resolved)
	if reason != "" {
		return nil
	}
	var lines []string
	list, _ := root["children"].([]any)
	for _, item := range list {
		if n, ok := item.(map[string]any); ok {
			if line := strings.TrimSpace(plainOf(n)); line != "" {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func plainOf(n node) string {
	typ, _ := n["type"].(string)
	switch typ {
	case "text":
		text, _ := n["text"].(string)
		return text
	case "linebreak":
		return "\n"
	}
	children, _ := n["children"].([]any)
	parts := make([]string, 0, len(children))
	for _, c := range children {
		if child, ok := c.(map[string]any); ok {
			parts = append(parts, plainOf(child))
		}
	}
	switch typ {
	case "list":
		return strings.Join(parts, "\n")
	case "listitem":
		return "- " + strings.Join(parts, "")
	case "link":
		label := strings.Join(parts, "")
		url, _ := n["url"].(string)
		switch {
		case label == "":
			return strings.TrimPrefix(url, "mailto:")
		case label == url || "mailto:"+label == url || "https://"+label == url || "http://"+label == url:
			return label
		}
		return label + " (" + url + ")"
	}
	return strings.Join(parts, "")
}
