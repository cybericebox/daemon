package errorJournal

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Limits on what one sample keeps.
const (
	MaxMessageBytes = 1500
	MaxStackBytes   = 6000
	MaxTitleBytes   = 240
	maxDetailBytes  = 200
)

const (
	redacted = "[redacted]"
	token    = "[token]"
)

var (
	rePEM      = regexp.MustCompile(`-----BEGIN [A-Z ]+-----[\s\S]*?(?:-----END [A-Z ]+-----|$)`)
	reJWT      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`)
	reUserInfo = regexp.MustCompile(`(://)[^/\s:@]+:[^/\s@]+@`)
	reBearer   = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	reKeyValue = regexp.MustCompile(`(?i)(\b[\w.-]*(?:pass(?:word|wd)?|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|authorization|cookie|credential|signature|otp)[\w.-]*["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;&)}\]]+)`)
	reQuery    = regexp.MustCompile(`(?i)([?&](?:token|code|key|sig|state|secret|password|access_token|id_token|refresh_token)=)[^&\s"']+`)
	reEmail    = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)
	reBotToken = regexp.MustCompile(`\b\d{6,}:[A-Za-z0-9_-]{30,}\b`)
	reAWSKey   = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)
	reIPv4     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	reIPv6Full = regexp.MustCompile(`\b(?:[0-9A-Fa-f]{1,4}:){7}[0-9A-Fa-f]{1,4}\b`)
	reIPv6Zero = regexp.MustCompile(`(?:[0-9A-Fa-f]{1,4}:){1,7}:(?:[0-9A-Fa-f]{1,4})?|::1\b`)
	reLong     = regexp.MustCompile(`[A-Za-z0-9_+/=-]{32,}`)
	reUUID     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// Scrub removes what must not reach the journal: secrets, passwords, tokens, e-mail addresses and IP addresses.
// It keeps the rest of the text, so the message still says what broke.
func Scrub(s string) string {
	if s == "" {
		return s
	}
	s = rePEM.ReplaceAllString(s, redacted)
	s = reJWT.ReplaceAllString(s, token)
	s = reBotToken.ReplaceAllString(s, token)
	s = reAWSKey.ReplaceAllString(s, token)
	s = reUserInfo.ReplaceAllString(s, "${1}"+redacted+"@")
	s = reBearer.ReplaceAllString(s, "${1} "+token)
	s = reKeyValue.ReplaceAllString(s, "${1}"+redacted)
	s = reQuery.ReplaceAllString(s, "${1}"+redacted)
	s = reEmail.ReplaceAllString(s, "[email]")
	s = reIPv4.ReplaceAllString(s, "[ip]")
	s = reIPv6Full.ReplaceAllString(s, "[ip]")
	s = reIPv6Zero.ReplaceAllString(s, "[ip]")
	s = reLong.ReplaceAllStringFunc(s, func(m string) string {
		if reUUID.MatchString(m) || !looksSecret(m) {
			return m
		}
		return token
	})
	return s
}

// looksSecret tells a random-looking string from a long word or a path segment: it needs a digit and a letter.
func looksSecret(s string) bool {
	var letter, digit bool
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			letter = true
		}
	}
	return letter && digit
}

// Truncate cuts s to at most max bytes on a rune boundary, marking the cut.
func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// Clean is Scrub then Truncate.
func Clean(s string, max int) string {
	return Truncate(strings.TrimSpace(Scrub(s)), max)
}

// CleanDetails scrubs the values of the details of an event.
func CleanDetails(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[Truncate(k, 60)] = Clean(v, maxDetailBytes)
	}
	return out
}
