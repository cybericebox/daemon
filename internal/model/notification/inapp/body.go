package inAppModel

import (
	"regexp"

	"github.com/microcosm-cc/bluemonday"
)

// bodyPolicy is the small format an in-app body may have: the same as the editor produces and the inbox shows
// (bold, italic, line breaks, paragraphs, and a span with one of the three fonts). Everything else a template or
// a variable could carry (links, images, forms, scripts, other attributes) is dropped on the server, whatever the
// browser's own sanitizer does.
var bodyPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("strong", "b", "em", "i", "br", "p", "div", "span")
	p.AllowAttrs("style").Matching(regexp.MustCompile(`^font-family:\s*(Arial|Georgia|Verdana)\s*;?$`)).OnElements("span")
	return p
}()

// SanitizeBody returns the body reduced to the allowed in-app format.
func SanitizeBody(html string) string {
	return bodyPolicy.Sanitize(html)
}
