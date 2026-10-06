// Package flagpattern parses catalog flag candidates and resolves one at
// materialization time. A template must carry the explicit "template:" marker;
// old ICE{...} values are always literal, even when they contain metacharacters.
package flagpattern

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"strings"
	"unicode"
)

const TemplatePrefix = "template:"

var ErrInvalidPattern = errors.New("invalid flag pattern")

type segment struct {
	literal string
	choices []byte
}

type Pattern struct {
	literal  string
	segments []segment
	template bool
	count    *big.Int
}

func (p Pattern) IsTemplate() bool { return p.template }

func (p Pattern) Cardinality() *big.Int { return new(big.Int).Set(p.count) }

func (p Pattern) Generate(source io.Reader) (string, error) {
	if !p.template {
		return p.literal, nil
	}
	var out strings.Builder
	out.WriteString("ICE{")
	for _, part := range p.segments {
		if len(part.choices) == 0 {
			out.WriteString(part.literal)
			continue
		}
		index, err := rand.Int(source, big.NewInt(int64(len(part.choices))))
		if err != nil {
			return "", err
		}
		out.WriteByte(part.choices[index.Int64()])
	}
	out.WriteByte('}')
	return out.String(), nil
}

func Parse(candidate string) (Pattern, error) {
	isTemplate := strings.HasPrefix(candidate, TemplatePrefix)
	value := candidate
	if isTemplate {
		value = strings.TrimPrefix(candidate, TemplatePrefix)
	}
	if !strings.HasPrefix(value, "ICE{") || !strings.HasSuffix(value, "}") || len(value) <= len("ICE{}") {
		return Pattern{}, ErrInvalidPattern
	}
	body := value[len("ICE{") : len(value)-1]
	for _, r := range body {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '{' || r == '}' {
			return Pattern{}, ErrInvalidPattern
		}
	}
	if !isTemplate {
		return Pattern{literal: value, count: big.NewInt(1)}, nil
	}
	p := Pattern{template: true, count: big.NewInt(1)}
	hasRandomSlot := false
	var literal strings.Builder
	flushLiteral := func() {
		if literal.Len() > 0 {
			p.segments = append(p.segments, segment{literal: literal.String()})
			literal.Reset()
		}
	}
	for i := 0; i < len(body); {
		switch body[i] {
		case '\\':
			if i+1 >= len(body) {
				return Pattern{}, ErrInvalidPattern
			}
			next := body[i+1]
			if next == '[' || next == ']' || next == '\\' {
				literal.WriteByte(next)
				i += 2
				continue
			}
			choices := shorthand(next)
			if choices == nil {
				return Pattern{}, ErrInvalidPattern
			}
			flushLiteral()
			p.addChoices(choices)
			hasRandomSlot = true
			i += 2
		case '[':
			choices, next, err := parseClass(body, i+1)
			if err != nil {
				return Pattern{}, err
			}
			flushLiteral()
			p.addChoices(choices)
			hasRandomSlot = true
			i = next
		case ']':
			return Pattern{}, ErrInvalidPattern
		default:
			literal.WriteByte(body[i])
			i++
		}
	}
	flushLiteral()
	if !hasRandomSlot {
		return Pattern{}, ErrInvalidPattern
	}
	return p, nil
}

func (p *Pattern) addChoices(choices []byte) {
	p.segments = append(p.segments, segment{choices: choices})
	p.count.Mul(p.count, big.NewInt(int64(len(choices))))
}

func shorthand(code byte) []byte {
	switch code {
	case 'd':
		return []byte("0123456789")
	case 'l':
		return []byte("abcdefghijklmnopqrstuvwxyz")
	case 'u':
		return []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	}
	return nil
}

func classMember(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func sameRangeGroup(a, b byte) bool {
	return (a >= '0' && b <= '9') || (a >= 'a' && b <= 'z') || (a >= 'A' && b <= 'Z')
}

func parseClass(body string, start int) ([]byte, int, error) {
	var choices []byte
	var exclusions []byte
	var present [256]bool
	var excluded [256]bool
	subtracting := false
	add := func(b byte) {
		if subtracting {
			if !excluded[b] {
				excluded[b] = true
				exclusions = append(exclusions, b)
			}
			return
		}
		if !present[b] {
			present[b] = true
			choices = append(choices, b)
		}
	}
	for i := start; i < len(body); {
		if body[i] == ']' {
			if len(choices) == 0 || (subtracting && len(exclusions) == 0) {
				return nil, 0, ErrInvalidPattern
			}
			for _, b := range exclusions {
				if !present[b] {
					return nil, 0, ErrInvalidPattern
				}
			}
			remaining := make([]byte, 0, len(choices))
			for _, b := range choices {
				if !excluded[b] {
					remaining = append(remaining, b)
				}
			}
			if len(remaining) == 0 {
				return nil, 0, ErrInvalidPattern
			}
			return remaining, i + 1, nil
		}
		if body[i] == '^' {
			if subtracting || len(choices) == 0 {
				return nil, 0, ErrInvalidPattern
			}
			subtracting = true
			i++
			continue
		}
		if body[i] == '\\' {
			if i+1 >= len(body) {
				return nil, 0, ErrInvalidPattern
			}
			set := shorthand(body[i+1])
			if set == nil {
				return nil, 0, ErrInvalidPattern
			}
			for _, b := range set {
				add(b)
			}
			i += 2
			continue
		}
		if !classMember(body[i]) {
			return nil, 0, ErrInvalidPattern
		}
		if i+1 < len(body) && body[i+1] == '-' {
			if i+2 >= len(body) || !classMember(body[i+2]) || !sameRangeGroup(body[i], body[i+2]) || body[i] > body[i+2] {
				return nil, 0, ErrInvalidPattern
			}
			for b := body[i]; b <= body[i+2]; b++ {
				add(b)
			}
			i += 3
			continue
		}
		add(body[i])
		i++
	}
	return nil, 0, ErrInvalidPattern
}

// Resolve chooses one candidate with weight equal to its number of outputs.
// Overlapping candidates deliberately retain their independent probability.
func Resolve(candidates []string, randomBytes int, source io.Reader) (string, error) {
	if randomBytes <= 0 {
		return "", ErrInvalidPattern
	}
	if len(candidates) == 0 {
		bytes := make([]byte, randomBytes)
		if _, err := io.ReadFull(source, bytes); err != nil {
			return "", err
		}
		return "ICE{" + hex.EncodeToString(bytes) + "}", nil
	}
	patterns := make([]Pattern, 0, len(candidates))
	total := big.NewInt(0)
	for _, candidate := range candidates {
		p, err := Parse(candidate)
		if err != nil {
			return "", err
		}
		patterns = append(patterns, p)
		total.Add(total, p.count)
	}
	slot, err := rand.Int(source, total)
	if err != nil {
		return "", err
	}
	for _, p := range patterns {
		if slot.Cmp(p.count) < 0 {
			return p.Generate(source)
		}
		slot.Sub(slot, p.count)
	}
	return "", ErrInvalidPattern
}
