package flagpattern_test

import (
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

func TestParse_CardinalityAndLiteralCompatibility(t *testing.T) {
	tests := []struct {
		candidate string
		want      string
	}{
		{`ICE{a[0-9]}`, "1"},
		{`template:ICE{room-[A-C\d]\l}`, "338"},
		{`template:ICE{[A-Cxyz1-3\d]}`, "16"},
		{`template:ICE{\d\l\u}`, "6760"},
		{`template:ICE{[a-a\l]}`, "26"},
		{`template:ICE{[a]}`, "1"},
		{`template:ICE{[\d^13]}`, "8"},
		{`template:ICE{[a-z^aeiou]}`, "21"},
		{`template:ICE{[A-F\d^B-D3]}`, "12"},
	}
	for _, tt := range tests {
		t.Run(tt.candidate, func(t *testing.T) {
			p, err := flagpattern.Parse(tt.candidate)
			if err != nil {
				t.Fatal(err)
			}
			if got := p.Cardinality().String(); got != tt.want {
				t.Fatalf("cardinality %s, want %s", got, tt.want)
			}
		})
	}
}

func TestParse_RejectsMalformedTemplates(t *testing.T) {
	for _, candidate := range []string{
		`template:ICE{fixed}`,
		`template:ICE{[]}`,
		`template:ICE{[Z-A]}`,
		`template:ICE{[A-a]}`,
		`template:ICE{[a-]}`,
		`template:ICE{[!@#]}`,
		`template:ICE{[^13]}`,
		`template:ICE{[\d^]}`,
		`template:ICE{[\d^\d]}`,
		`template:ICE{[\d^a]}`,
		`template:ICE{[\d^1^2]}`,
		`template:ICE{\q}`,
		`template:ICE{[\q]}`,
		`template:ICE{[}`,
		`template:ICE{\d }`,
	} {
		t.Run(candidate, func(t *testing.T) {
			if _, err := flagpattern.Parse(candidate); err == nil {
				t.Fatalf("accepted invalid %q", candidate)
			}
		})
	}
}

type repeatedByte byte

func (b repeatedByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(b)
	}
	return len(p), nil
}

func TestResolve_WeightsByOutputCount(t *testing.T) {
	candidates := []string{`ICE{fixed}`, `template:ICE{\d}`}
	first, err := flagpattern.Resolve(candidates, 20, repeatedByte(0))
	if err != nil || first != `ICE{fixed}` {
		t.Fatalf("slot zero = %q, %v", first, err)
	}
	last, err := flagpattern.Resolve(candidates, 20, repeatedByte(9))
	if err != nil || !strings.HasPrefix(last, "ICE{") || last == `ICE{fixed}` {
		t.Fatalf("slot ten = %q, %v", last, err)
	}
	if _, err := flagpattern.Resolve(nil, 20, io.LimitReader(repeatedByte(3), 20)); err != nil {
		t.Fatal(err)
	}
}

func TestParse_BigCardinalityDoesNotOverflow(t *testing.T) {
	p, err := flagpattern.Parse(`template:ICE{` + strings.Repeat(`\d`, 25) + `}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Cardinality().String(); got != "10000000000000000000000000" {
		t.Fatalf("cardinality = %s", got)
	}
}

func TestParse_FlagTemplateRepeatCount(t *testing.T) {
	p, err := flagpattern.Parse(`template:ICE{[a-f0-9]{16}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Cardinality().String(); got != "18446744073709551616" { // 16^16
		t.Fatalf("cardinality = %s", got)
	}
	value, err := p.Generate(repeatedByte(1))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^ICE\{[a-f0-9]{16}\}$`).MatchString(value) {
		t.Fatalf("generated %q", value)
	}
	if strings.ContainsAny(value[len("ICE{"):len(value)-1], "{}") {
		t.Fatalf("generated flag %q holds a brace inside", value)
	}
	mixed, err := flagpattern.Parse(`template:ICE{room-\d{3}-[A-C]{2}x}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := mixed.Cardinality().String(); got != "9000" { // 10^3 * 3^2
		t.Fatalf("mixed cardinality = %s", got)
	}
}

func TestParse_FlagTemplateRepeatCountRejectsMisuse(t *testing.T) {
	for _, candidate := range []string{
		`template:ICE{[a-f]{0}}`,
		`template:ICE{[a-f]{65}}`,
		`template:ICE{[a-f]{x}}`,
		`template:ICE{[a-f]{3}{2}}`,
		`template:ICE{[a-f]{3}`,
		`template:ICE{{3}[a-f]}`,
		`template:ICE{a{3}[a-f]}`,
		`template:ICE{[a-f]}x}}`,
		`template:ICE{[a-f]\{}`,
		// a fixed flag never holds a brace inside, whatever follows it
		`ICE{a{3}}`,
		`ICE{a}b}`,
	} {
		t.Run(candidate, func(t *testing.T) {
			if _, err := flagpattern.Parse(candidate); err == nil {
				t.Fatalf("accepted invalid %q", candidate)
			}
		})
	}
}
