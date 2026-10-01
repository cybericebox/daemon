package render

import (
	"html"
	"regexp"
	"strings"
)

var (
	// Elements whose content is never visible text.
	invisibleElementRe = regexp.MustCompile(`(?is)<(head|style|script)\b[^>]*>.*?</(head|style|script)\s*>`)
	// An anchor with an href (double- or single-quoted) and its inner HTML.
	anchorRe = regexp.MustCompile(`(?is)<a\b[^>]*?\bhref\s*=\s*(?:"([^"]*)"|'([^']*)')[^>]*>(.*?)</a\s*>`)
	tagRe    = regexp.MustCompile(`(?s)<[^>]*>`)
	// A line break inside a block, and the end of a block (a paragraph break).
	lineBreakRe = regexp.MustCompile(`(?i)<br\s*/?>`)
	blockEndRe  = regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|blockquote|ul|ol)\s*>|<hr\b[^>]*>`)
)

const (
	lineBreakMark  = "\x01"
	blockBreakMark = "\x02"
)

// PlainText derives the text/plain alternative of a rendered email body:
// invisible elements dropped, links rendered as "label (url)" (just the url
// when the label is empty or the url itself), tags stripped (each becomes a
// word break), entities unescaped and whitespace collapsed. A <br> becomes a
// line break and the end of a block a blank line, so a signature keeps its
// lines.
func PlainText(htmlBody string) string {
	s := invisibleElementRe.ReplaceAllString(htmlBody, " ")
	s = anchorRe.ReplaceAllStringFunc(s, linkText)
	s = lineBreakRe.ReplaceAllString(s, lineBreakMark)
	s = blockEndRe.ReplaceAllString(s, blockBreakMark)
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	var blocks []string
	for _, block := range strings.Split(s, blockBreakMark) {
		var lines []string
		for _, line := range strings.Split(block, lineBreakMark) {
			if line = strings.Join(strings.Fields(line), " "); line != "" {
				lines = append(lines, line)
			}
		}
		if len(lines) > 0 {
			blocks = append(blocks, strings.Join(lines, "\n"))
		}
	}
	return strings.Join(blocks, "\n\n")
}

// linkText renders one anchor as text. The result is re-escaped because
// PlainText unescapes the whole body once afterwards.
func linkText(anchor string) string {
	m := anchorRe.FindStringSubmatch(anchor)
	href := m[1]
	if href == "" {
		href = m[2]
	}
	url := strings.TrimSpace(html.UnescapeString(href))
	label := strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(m[3], " "))), " ")
	var text string
	switch {
	case url == "":
		text = label
	case label == "" || label == url:
		text = url
	default:
		text = label + " (" + url + ")"
	}
	return " " + html.EscapeString(text) + " "
}
