package errorJournal

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

var (
	reNormUUID = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reNormHex  = regexp.MustCompile(`\b[0-9a-fA-F]{8,}\b`)
	reNormNum  = regexp.MustCompile(`\d+`)
	reNormWS   = regexp.MustCompile(`\s+`)
)

// Normalize turns a message into the stable part: ids, hashes and numbers (counts, line numbers, durations,
// ports) vary between occurrences of one fault, so they are replaced. The message is scrubbed first.
func Normalize(message string) string {
	s := Scrub(message)
	s = reNormUUID.ReplaceAllString(s, "[id]")
	s = reNormHex.ReplaceAllString(s, "[hex]")
	s = reNormNum.ReplaceAllString(s, "#")
	s = reNormWS.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// Fingerprint groups events. The parts that name the fault are kind, source and the normalized message; for a
// refusal (403) it is the route, the role and the permission, for a rate limit (429) the route and the limiter.
func Fingerprint(e Event) string {
	var parts []string
	switch e.Kind {
	case KindHTTP403:
		parts = []string{string(e.Kind), e.Route, e.Role, e.Permission}
	case KindHTTP429:
		parts = []string{string(e.Kind), e.Route, e.Limiter}
	default:
		parts = []string{string(e.Kind), e.Source, Normalize(e.Message)}
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:16])
}

// Title is the one line that names a group in the list.
func Title(e Event) string {
	switch e.Kind {
	case KindHTTP403:
		return Truncate("403 "+e.Method+" "+e.Route+" · "+e.Role+" · "+e.Permission, MaxTitleBytes)
	case KindHTTP429:
		return Truncate("429 "+e.Method+" "+e.Route+" · "+e.Limiter, MaxTitleBytes)
	}
	return Truncate(Normalize(e.Message), MaxTitleBytes)
}
