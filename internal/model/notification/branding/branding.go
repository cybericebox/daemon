// Package branding holds the brand context email rendering resolves colour
// tokens and the logo against. Iteration 1 has only the platform brand; the
// resolver that picks a context per dispatch is the single place an Event
// brand will plug in later.
package branding

import _ "embed"

// Context is the set of brand colours a template's theme:* tokens resolve to.
type Context struct {
	Brand    string
	Accent   string
	OnAccent string
}

// Platform is the CyberICEBox platform brand.
func Platform() Context {
	return Context{Brand: "#211A52", Accent: "#211A52", OnAccent: "#FFFFFF"}
}

//go:embed crest.png
var logoPNG []byte

// LogoContentType is the MIME type of LogoPNG.
const LogoContentType = "image/png"

// LogoPNG returns the platform crest (128×125 PNG). Callers must not mutate it.
func LogoPNG() []byte {
	return logoPNG
}
