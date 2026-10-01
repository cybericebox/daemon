package render

import (
	"html"
	"strings"
)

// FooterMarker is where the standard footer is inserted into a shelled
// document (below the content card); a body without it gets the footer
// appended.
const FooterMarker = "<!--cib-footer-->"

// shellStyle holds the dark-mode palette. Everything else is inline, so
// clients that drop <style> still show the light design. No shadows, no
// decorative accents.
const shellStyle = `body{margin:0;padding:0}img{border:0;outline:none}a{text-decoration:underline}` +
	`@media (max-width:620px){.cib-card{padding:24px 20px !important}}` +
	`@media (prefers-color-scheme:dark){` +
	`.cib-page{background-color:#0F1020 !important}` +
	`.cib-card{background-color:#181A2E !important;border-color:#2A2D45 !important}` +
	`.cib-t{color:#E8E9F2 !important}.cib-h{color:#FFFFFF !important}` +
	`.cib-muted,.cib-muted p,.cib-muted a{color:#A0A4B8 !important}` +
	`.cib-a{color:#9DA8FF !important}` +
	`.cib-fact{background-color:#20233B !important;border-color:#2A2D45 !important}` +
	`.cib-rule{background-color:#2A2D45 !important}` +
	`td.cib-btn{background-color:#FFFFFF !important}a.cib-btn{background-color:#FFFFFF !important;color:#211A52 !important}}`

// Shell wraps rendered body blocks in the email document: page background, one
// centred column of at most 600px holding the content card, and the footer
// slot below it. Tables and inline styles keep it stable in Gmail, Outlook and
// Apple Mail. The preheader is a hidden line for the inbox preview.
func Shell(lang, preheader, body string) string {
	if lang == "" {
		lang = "uk"
	}
	var sb strings.Builder
	sb.WriteString(`<!doctype html><html lang="` + html.EscapeString(lang) + `"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<meta name="color-scheme" content="light dark"><meta name="supported-color-schemes" content="light dark">` +
		`<title></title><style>` + shellStyle + `</style></head>` +
		`<body class="cib-page" style="margin:0;padding:0;background-color:#F3F4F8">`)
	if preheader != "" {
		sb.WriteString(`<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent">` + html.EscapeString(preheader) + `</div>`)
	}
	sb.WriteString(`<table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" class="cib-page" bgcolor="#F3F4F8" style="background-color:#F3F4F8">` +
		`<tr><td align="center" style="padding:24px 12px">` +
		`<table role="presentation" width="600" cellspacing="0" cellpadding="0" border="0" style="width:100%;max-width:600px">` +
		`<tr><td class="cib-card" bgcolor="#FFFFFF" style="background-color:#FFFFFF;border:1px solid #E3E5EC;border-radius:12px;padding:32px">`)
	sb.WriteString(body)
	sb.WriteString(`</td></tr><tr><td style="padding:16px 8px 0">` + FooterMarker + `</td></tr></table></td></tr></table></body></html>`)
	return sb.String()
}
