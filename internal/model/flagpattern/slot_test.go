package flagpattern_test

import (
	"bytes"
	"crypto/rand"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

var (
	keyA = []byte("fixed-test-key-a")
	keyB = []byte("fixed-test-key-b")
)

func TestPermute_IsABijectionOnTheDomain(t *testing.T) {
	for _, n := range []int64{1, 2, 3, 7, 26, 100, 1000, 4097} {
		size := big.NewInt(n)
		seen := make(map[int64]bool, n)
		for x := range n {
			y, err := flagpattern.Permute(keyA, size, big.NewInt(x))
			if err != nil {
				t.Fatal(err)
			}
			if y.Sign() < 0 || y.Cmp(size) >= 0 {
				t.Fatalf("N=%d: P(%d) = %s out of range", n, x, y)
			}
			if seen[y.Int64()] {
				t.Fatalf("N=%d: P(%d) = %s repeats", n, x, y)
			}
			seen[y.Int64()] = true
		}
	}
}

func TestPermute_RejectsBadInput(t *testing.T) {
	if _, err := flagpattern.Permute(keyA, big.NewInt(0), big.NewInt(0)); err == nil {
		t.Fatal("N=0 accepted")
	}
	if _, err := flagpattern.Permute(keyA, big.NewInt(5), big.NewInt(5)); err == nil {
		t.Fatal("x=N accepted")
	}
}

func TestPermute_DeterministicAndKeyed(t *testing.T) {
	size := big.NewInt(1000)
	differs := false
	for x := range int64(1000) {
		a1, _ := flagpattern.Permute(keyA, size, big.NewInt(x))
		a2, _ := flagpattern.Permute(keyA, size, big.NewInt(x))
		b, _ := flagpattern.Permute(keyB, size, big.NewInt(x))
		if a1.Cmp(a2) != 0 {
			t.Fatalf("P(%d) not deterministic: %s vs %s", x, a1, a2)
		}
		if a1.Cmp(b) != 0 {
			differs = true
		}
	}
	if !differs {
		t.Fatal("two keys gave the same permutation")
	}
}

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

func TestResolveAt_SlotsGiveDistinctFlagsAndWrap(t *testing.T) {
	candidates := []string{`ICE{a}`, `ICE{b}`, `template:ICE{[ab][0-2]}`}
	const n = 8
	seen := map[string]bool{}
	first := ""
	for slot := range int64(n) {
		got, err := flagpattern.ResolveAt(candidates, big.NewInt(slot), keyA)
		if err != nil {
			t.Fatal(err)
		}
		if seen[got] {
			t.Fatalf("slot %d repeats %q", slot, got)
		}
		seen[got] = true
		if slot == 0 {
			first = got
		}
	}
	wrapped, err := flagpattern.ResolveAt(candidates, big.NewInt(n), keyA)
	if err != nil || wrapped != first {
		t.Fatalf("slot N = %q, %v; want slot 0's %q", wrapped, err, first)
	}
	again, _ := flagpattern.ResolveAt(candidates, big.NewInt(3), keyA)
	twice, _ := flagpattern.ResolveAt(candidates, big.NewInt(3), keyA)
	if again != twice {
		t.Fatalf("same key and slot differ: %q vs %q", again, twice)
	}
	if _, err := flagpattern.ResolveAt(nil, big.NewInt(0), keyA); err == nil {
		t.Fatal("no candidates must not resolve by slot")
	}
}

func TestResolveAt_SingleCandidate(t *testing.T) {
	for slot := range int64(3) {
		got, err := flagpattern.ResolveAt([]string{`ICE{only}`}, big.NewInt(slot), keyA)
		if err != nil || got != "ICE{only}" {
			t.Fatalf("slot %d = %q, %v", slot, got, err)
		}
	}
}

func TestResolveAt_HugeTemplateIsFast(t *testing.T) {
	candidates := []string{`template:ICE{` + strings.Repeat(`[a-z0-9]`, 32) + `}`}
	start := time.Now()
	seen := map[string]bool{}
	for slot := range int64(200) {
		got, err := flagpattern.ResolveAt(candidates, big.NewInt(slot), keyA)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len("ICE{}")+32 {
			t.Fatalf("flag %q has the wrong length", got)
		}
		if seen[got] {
			t.Fatalf("slot %d repeats %q", slot, got)
		}
		seen[got] = true
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("200 huge-template flags took %s", elapsed)
	}
}

// A domain wider than one HMAC output (h > 256 bits per half) still permutes.
func TestPermute_VeryWideDomain(t *testing.T) {
	size := new(big.Int).Lsh(big.NewInt(1), 700)
	size.Sub(size, big.NewInt(12345))
	seen := map[string]bool{}
	for x := range int64(50) {
		y, err := flagpattern.Permute(keyA, size, big.NewInt(x))
		if err != nil {
			t.Fatal(err)
		}
		if y.Sign() < 0 || y.Cmp(size) >= 0 || seen[y.String()] {
			t.Fatalf("P(%d) = %s out of range or repeated", x, y)
		}
		seen[y.String()] = true
	}
}

// The random-slot mode (no event team) still reaches every value. A fixed
// byte stream walks every slot, so the test needs no luck.
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
