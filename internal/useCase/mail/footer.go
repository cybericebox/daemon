package mailUseCase

import (
	"encoding/json"
	"strings"

	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/render"
)

// footerBox is muted small text under a «—» line (the fold mark of mail
// clients); no shadows.
const footerBox = "margin:0;padding:0;" +
	"font-family:Arial,Helvetica,sans-serif;font-size:12px;line-height:1.5;color:#6b7280"

const footerLinkStyle = "color:#6b7280;text-decoration:underline"

// footerStyling is the email-body styling of the footer paragraphs: the same
// renderer as the body blocks, with the footer's small muted text.
var footerStyling = json.RawMessage(`{"font_family":"Arial,Helvetica,sans-serif","text_color":"#6b7280",` +
	`"text_font_size":"12px","text_line_height":"1.5","paragraph_bottom_margin":"4px",` +
	`"heading_color":"#6b7280","heading_top_margin":"0","heading_bottom_margin":"4px"}`)

// renderFooter renders the footer document (nil: the built-in default) for a
// message with the given Reply-To, through the renderer of the email body
// blocks. An empty result means there is nothing to append. A document the
// renderer rejects (it was validated on save, so only corrupt data) falls back
// to the default footer rather than blocking mail.
func renderFooter(doc []byte, supportEmail, site string) mailModel.Footer {
	if len(doc) == 0 {
		doc = mailModel.DefaultFooterDoc(mailModel.LanguageUK)
	}
	if footer, ok := tryRenderFooter(doc, supportEmail, site); ok {
		return footer
	}
	footer, _ := tryRenderFooter(mailModel.DefaultFooterDoc(mailModel.LanguageUK), supportEmail, site)
	return footer
}

func tryRenderFooter(doc []byte, supportEmail, site string) (mailModel.Footer, bool) {
	resolved, ok := mailModel.ResolveFooterDoc(doc, mailModel.FooterValues(supportEmail, site))
	if !ok {
		return mailModel.Footer{}, resolved != nil
	}
	body, err := json.Marshal([]map[string]any{{"type": "rich_text", "content": json.RawMessage(resolved)}})
	if err != nil {
		return mailModel.Footer{}, false
	}
	rendered, err := render.RenderEmail(body, footerStyling, nil, nil, render.RenderContext{})
	if err != nil {
		return mailModel.Footer{}, false
	}
	// The footer is muted text, not body text: drop the body classes (they
	// carry the dark-mode body colours) and recolour the links.
	html := strings.ReplaceAll(rendered.HTML, ` class="cib-t"`, "")
	html = strings.ReplaceAll(html, ` class="cib-a"`, "")
	html = strings.ReplaceAll(html, `style="color:#3340C4;text-decoration:underline"`, `style="`+footerLinkStyle+`"`)
	return mailModel.Footer{
		HTML: mailModel.KeepBrandHTML(`<div class="cib-muted" style="` + footerBox + `"><p style="margin:0 0 4px 0">—</p>` + html + `</div>`),
		Text: mailModel.KeepBrandText("—\n" + strings.Join(mailModel.FooterPlainLines(resolved), "\n")),
	}, true
}

// RenderPlatformFooter is the built-in footer of platform mail for the given
// Reply-To and site (previews and tests).
func RenderPlatformFooter(supportEmail, site string) mailModel.Footer {
	return renderFooter(nil, supportEmail, site)
}
