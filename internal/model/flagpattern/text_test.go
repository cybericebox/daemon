package flagpattern_test

import (
	"math/big"
	"regexp"
	"testing"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

// constantReader never runs dry: every byte is b.
type constantReader byte

func (c constantReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(c)
	}
	return len(p), nil
}

func TestParseVariable(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		valid   bool
		count   int64
	}{
		{`[a-z0-9]{16}`, true, 0}, // 36^16 does not fit; checked below
		{`[ab]{3}`, true, 8},
		{`pw-[a-c]\d`, true, 30},
		{`\d{2}x\l`, true, 2600},
		{`a\{b[xy]`, true, 2},
		{`[ab]{0}`, false, 0},
		{`[ab]{65}`, false, 0},
		{`[ab]{2`, false, 0},
		{`{3}`, false, 0},
		{`x{3}`, false, 0},
		{`[ab]{2}{2}`, false, 0},
		{`fixed`, false, 0},
		{`has space[ab]`, false, 0},
		{`[Z-A]`, false, 0},
		{`a}[ab]`, false, 0},
		{``, false, 0},
	} {
		p, err := flagpattern.ParseVariable(tc.pattern)
		if (err == nil) != tc.valid {
			t.Fatalf("%q: err = %v, want valid=%v", tc.pattern, err, tc.valid)
		}
		if err == nil && tc.count > 0 && p.Cardinality().Cmp(big.NewInt(tc.count)) != 0 {
			t.Errorf("%q: cardinality %v, want %d", tc.pattern, p.Cardinality(), tc.count)
		}
	}
}

func TestVariableOutputHasNoICEEdgesAndFollowsThePattern(t *testing.T) {
	p, err := flagpattern.ParseVariable(`[a-z0-9]{16}`)
	if err != nil {
		t.Fatal(err)
	}
	// A fixed byte source: the output is the same every time, and has the shape of the pattern.
	got, err := p.Generate(constantReader(3))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[a-z0-9]{16}$`).MatchString(got) {
		t.Fatalf("generated %q", got)
	}
	// At and Offset are inverse.
	offset, ok := p.Offset(got)
	if !ok {
		t.Fatalf("%q is not an output of its own pattern", got)
	}
	back, err := p.At(offset)
	if err != nil || back != got {
		t.Fatalf("At(Offset(%q)) = %q, %v", got, back, err)
	}
}

func TestParseTextAndResolveText(t *testing.T) {
	if _, err := flagpattern.ParseText(""); err == nil {
		t.Fatal("an empty text value must be refused")
	}
	if _, err := flagpattern.ParseText("two\nlines"); err == nil {
		t.Fatal("a control character must be refused")
	}
	literal, err := flagpattern.ParseText("OSPF area 0")
	if err != nil || literal.IsTemplate() {
		t.Fatalf("a literal text value: %v %v", literal, err)
	}
	value, err := flagpattern.ResolveText([]string{"OSPF area 0"}, constantReader(1))
	if err != nil || value != "OSPF area 0" {
		t.Fatalf("ResolveText of one literal = %q, %v", value, err)
	}
	value, err = flagpattern.ResolveText([]string{"template:id-[0-9]{4}"}, constantReader(3))
	if err != nil || !regexp.MustCompile(`^id-[0-9]{4}$`).MatchString(value) {
		t.Fatalf("ResolveText of a template = %q, %v", value, err)
	}
	if _, err = flagpattern.ResolveText(nil, constantReader(0)); err == nil {
		t.Fatal("a text answer needs a value")
	}
	// The flag rules are not touched: a flag template keeps its ICE{} edges and refuses the repeat count.
	if _, err = flagpattern.Parse(`template:ICE{[a-f0-9]{16}}`); err == nil {
		t.Fatal("a flag template does not take a repeat count")
	}
	if _, err = flagpattern.Parse(`template:ICE{[a-f0-9][a-f0-9]}`); err != nil {
		t.Fatalf("a flag template: %v", err)
	}
}

func TestTextSetNumbersLikeAFlagSet(t *testing.T) {
	set, err := flagpattern.ParseTextSet([]string{"fixed", "template:[ab][0-2]"})
	if err != nil {
		t.Fatal(err)
	}
	if set.Size().Int64() != 7 {
		t.Fatalf("size = %v, want 1 + 6", set.Size())
	}
	for i, want := range []string{"fixed", "a0", "a1", "a2", "b0", "b1", "b2"} {
		got, atErr := set.At(big.NewInt(int64(i)))
		if atErr != nil || got != want {
			t.Fatalf("At(%d) = %q, %v, want %q", i, got, atErr, want)
		}
		if indices := set.Indices(want); len(indices) != 1 || indices[0].Int64() != int64(i) {
			t.Fatalf("Indices(%q) = %v", want, indices)
		}
	}
}
