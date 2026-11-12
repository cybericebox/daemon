package flagpattern

import (
	"crypto/rand"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode"
)

// Free text has no ICE{} edges. It is used by the values of a text answer and by the templates of the
// infrastructure variables. The rules of a flag pattern hold, with one addition: a class or a shorthand may be
// followed by a repeat count `{n}` (1..MaxRepeat), so `[a-z0-9]{16}` is sixteen random characters.

const (
	// MaxRepeat bounds one repeat count.
	MaxRepeat = 64
	// MaxSlots bounds the random positions of one free-text template.
	MaxSlots = 256
)

// ParseText parses a value of a text answer: `template:<pattern>` is a template, anything else is the literal text
// itself. A literal must be non-empty and have no control characters.
func ParseText(candidate string) (Pattern, error) {
	if body, isTemplate := strings.CutPrefix(candidate, TemplatePrefix); isTemplate {
		return ParseVariable(body)
	}
	if candidate == "" || strings.ContainsFunc(candidate, unicode.IsControl) {
		return Pattern{}, ErrInvalidPattern
	}
	return Pattern{literal: candidate, bare: true, count: big.NewInt(1)}, nil
}

// ParseVariable parses a bare template pattern (no `template:` prefix, no ICE{} edges), for example
// `[a-z0-9]{16}`. It needs at least one random position.
func ParseVariable(body string) (Pattern, error) {
	p := Pattern{template: true, bare: true, count: big.NewInt(1)}
	slots := 0
	var literal strings.Builder
	flushLiteral := func() {
		if literal.Len() > 0 {
			p.segments = append(p.segments, segment{literal: literal.String()})
			literal.Reset()
		}
	}
	lastSlot := false // the previous token was a random position, so a repeat count may follow
	var lastChoices []byte
	addSlot := func(choices []byte) bool {
		flushLiteral()
		p.addChoices(choices)
		lastSlot, lastChoices = true, choices
		slots++
		return slots <= MaxSlots
	}
	for i := 0; i < len(body); {
		c := body[i]
		if c < 0x80 && (unicode.IsSpace(rune(c)) || unicode.IsControl(rune(c))) {
			return Pattern{}, ErrInvalidPattern
		}
		switch c {
		case '\\':
			if i+1 >= len(body) {
				return Pattern{}, ErrInvalidPattern
			}
			next := body[i+1]
			if next == '[' || next == ']' || next == '{' || next == '}' || next == '\\' {
				literal.WriteByte(next)
				lastSlot = false
				i += 2
				continue
			}
			choices := shorthand(next)
			if choices == nil || !addSlot(choices) {
				return Pattern{}, ErrInvalidPattern
			}
			i += 2
		case '[':
			choices, next, err := parseClass(body, i+1)
			if err != nil || !addSlot(choices) {
				return Pattern{}, ErrInvalidPattern
			}
			i = next
		case '{':
			if !lastSlot {
				return Pattern{}, ErrInvalidPattern
			}
			end := strings.IndexByte(body[i:], '}')
			if end < 0 {
				return Pattern{}, ErrInvalidPattern
			}
			n, err := strconv.Atoi(body[i+1 : i+end])
			if err != nil || n < 1 || n > MaxRepeat {
				return Pattern{}, ErrInvalidPattern
			}
			for range n - 1 {
				if !addSlot(lastChoices) {
					return Pattern{}, ErrInvalidPattern
				}
			}
			lastSlot = false // one count per position
			i += end + 1
		case ']', '}':
			return Pattern{}, ErrInvalidPattern
		default:
			literal.WriteByte(c)
			lastSlot = false
			i++
		}
	}
	flushLiteral()
	if slots == 0 {
		return Pattern{}, ErrInvalidPattern
	}
	return p, nil
}

// ResolveText picks one value of a text answer at random (a template is generated). It needs at least one value.
func ResolveText(candidates []string, source io.Reader) (string, error) {
	if len(candidates) == 0 {
		return "", ErrInvalidPattern
	}
	set, err := ParseTextSet(candidates)
	if err != nil {
		return "", err
	}
	g, err := rand.Int(source, set.total)
	if err != nil {
		return "", err
	}
	return set.At(g)
}

// Generate returns one random output of the pattern.
func (p Pattern) Generate(source io.Reader) (string, error) {
	g, err := rand.Int(source, p.count)
	if err != nil {
		return "", err
	}
	return p.At(g)
}
