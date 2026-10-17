package flagpattern_test

import (
	"bytes"
	"crypto/rand"
	"math/big"
	"testing"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

func TestPatternAt_MixedRadixLastPositionFastest(t *testing.T) {
	p, err := flagpattern.Parse(`template:ICE{x[ab]-[0-2]}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ICE{xa-0}", "ICE{xa-1}", "ICE{xa-2}", "ICE{xb-0}", "ICE{xb-1}", "ICE{xb-2}"}
	for i, w := range want {
		got, err := p.At(big.NewInt(int64(i)))
		if err != nil || got != w {
			t.Fatalf("At(%d) = %q, %v; want %q", i, got, err, w)
		}
	}
	if _, err := p.At(big.NewInt(6)); err == nil {
		t.Fatal("offset past the end accepted")
	}
	fixed, _ := flagpattern.Parse(`ICE{fixed}`)
	if got, err := fixed.At(big.NewInt(0)); err != nil || got != "ICE{fixed}" {
		t.Fatalf("fixed At(0) = %q, %v", got, err)
	}
}

func TestValueAt_EnumeratesEveryOutputOnce(t *testing.T) {
	candidates := []string{`ICE{a}`, `template:ICE{[ab][0-2]}`}
	set, err := flagpattern.ParseSet(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if set.Size().Int64() != 7 {
		t.Fatalf("N = %s, want 7", set.Size())
	}
	want := []string{"ICE{a}", "ICE{a0}", "ICE{a1}", "ICE{a2}", "ICE{b0}", "ICE{b1}", "ICE{b2}"}
	for g, w := range want {
		got, err := set.At(big.NewInt(int64(g)))
		if err != nil || got != w {
			t.Fatalf("At(%d) = %q, %v; want %q", g, got, err, w)
		}
	}
	if _, err := set.At(big.NewInt(7)); err == nil {
		t.Fatal("index past N accepted")
	}
}

func TestResolve_RandomSlotHitsEveryValue(t *testing.T) {
	candidates := []string{`ICE{a}`, `template:ICE{[ab][0-2]}`}
	seen := map[string]bool{}
	for b := range byte(7) {
		got, err := flagpattern.Resolve(candidates, 20, bytes.NewReader(bytes.Repeat([]byte{b}, 64)))
		if err != nil {
			t.Fatal(err)
		}
		seen[got] = true
	}
	if len(seen) != 7 {
		t.Fatalf("random slots reached %d of 7 values: %v", len(seen), seen)
	}
	if _, err := flagpattern.Resolve(candidates, 20, rand.Reader); err != nil {
		t.Fatal(err)
	}
}

func TestSetIndices_InvertsAtWithOverlaps(t *testing.T) {
	set, err := flagpattern.ParseSet([]string{`ICE{a0}`, `template:ICE{[ab][0-2]}`, `ICE{zz}`})
	if err != nil {
		t.Fatal(err)
	}
	for g := range set.Size().Int64() {
		v, atErr := set.At(big.NewInt(g))
		if atErr != nil {
			t.Fatal(atErr)
		}
		found := false
		for _, i := range set.Indices(v) {
			if i.Int64() == g {
				found = true
			}
		}
		if !found {
			t.Fatalf("Indices(%q) misses %d", v, g)
		}
	}
	if got := set.Indices("ICE{a0}"); len(got) != 2 || got[0].Int64() != 0 || got[1].Int64() != 1 {
		t.Fatalf("overlap indices = %v, want [0 1]", got)
	}
	for _, miss := range []string{"ICE{c0}", "ICE{a}", "ICE{a00}", "ICE{}", "x"} {
		if got := set.Indices(miss); len(got) != 0 {
			t.Fatalf("Indices(%q) = %v, want none", miss, got)
		}
	}
}
