package mailModel

import "regexp"

// The product name «Cyber ICE Box» never breaks across lines (see the
// CyberICEBox CLAUDE.md, «Brand name never wraps»). Stored and admin-edited
// templates keep ordinary spaces, so the words are joined with no-break
// spaces when a message is rendered.
const (
	nbsp     = " "
	nbspHTML = "&nbsp;"
)

var brandPattern = regexp.MustCompile(`Cyber[\s\x{00a0}]+(ICE|Ice)[\s\x{00a0}]+Box`)

// KeepBrandText joins the words of the product name with U+00A0 (text parts).
func KeepBrandText(s string) string {
	return brandPattern.ReplaceAllString(s, "Cyber"+nbsp+"${1}"+nbsp+"Box")
}

// KeepBrandHTML joins the words of the product name with &nbsp; (HTML parts;
// the entity survives a wrong charset guess, unlike a raw U+00A0).
func KeepBrandHTML(s string) string {
	return brandPattern.ReplaceAllString(s, "Cyber"+nbspHTML+"${1}"+nbspHTML+"Box")
}
