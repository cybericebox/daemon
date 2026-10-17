package flagfilter_test

import (
	"bytes"
	"crypto/rand"
	mrand "math/rand/v2"
	"testing"

	"github.com/cybericebox/daemon/internal/model/flagfilter"
	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

func seeded(seed byte) *mrand.ChaCha8 {
	var s [32]byte
	s[0] = seed
	return mrand.NewChaCha8(s)
}

func mustSet(t *testing.T, candidates ...string) flagpattern.Set {
	t.Helper()
	set, err := flagpattern.ParseSet(candidates)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestCapacity_DefaultsToOneHundred(t *testing.T) {
	limit := int32(42)
	if got := flagfilter.Capacity(nil); got != 100 {
		t.Fatalf("nil limit = %d, want 100", got)
	}
	if got := flagfilter.Capacity(&limit); got != 42 {
		t.Fatalf("limit 42 = %d", got)
	}
	huge := int32(2_000_000_000)
	if got := flagfilter.Capacity(&huge); got != flagfilter.MaxFilterCapacity {
		t.Fatalf("huge limit = %d, want the cap %d", got, flagfilter.MaxFilterCapacity)
	}
}

// No caller can make a filter larger than the cap: New and Grow clamp it.
func TestCapacity_FilterSizeIsBounded(t *testing.T) {
	set := mustSet(t, `template:ICE{\l\l\l\l\l\l\l\l}`)
	f, err := flagfilter.New(set, 1<<30, nil, seeded(20))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := flagfilter.BloomSize(flagfilter.MaxFilterCapacity)
	if f.Capacity != flagfilter.MaxFilterCapacity || f.Size != m || len(f.Bits) != int(m/8) {
		t.Fatalf("capacity %d size %d bytes %d", f.Capacity, f.Size, len(f.Bits))
	}
	if got := flagfilter.GrownCapacity(flagfilter.MaxFilterCapacity); got != flagfilter.MaxFilterCapacity {
		t.Fatalf("grown past the cap: %d", got)
	}
	if got := flagfilter.GrownCapacity(100); got != 200 {
		t.Fatalf("grown 100 = %d", got)
	}
	if f.Full() {
		t.Fatal("an empty filter is not full")
	}
	f.Inserted = flagfilter.MaxFilterCapacity
	if f.Full() {
		t.Fatal("a filter at the cap never asks to grow")
	}
}

// The filter remembers its candidate list: a changed list never fits.
func TestFits_RequiresTheSameCandidates(t *testing.T) {
	old := []string{`template:ICE{\l\l\l}`}
	changed := []string{`template:ICE{\u\u\u}`}
	set := mustSet(t, old...)
	f, err := flagfilter.New(set, 100, nil, seeded(21))
	if err != nil {
		t.Fatal(err)
	}
	if !f.Fits(set, 100) {
		t.Fatal("same candidates must fit")
	}
	if f.Fits(mustSet(t, changed...), 100) {
		t.Fatal("other candidates of the same size must not fit")
	}
	if a, b := flagfilter.Fingerprint([]string{"ab", "c"}), flagfilter.Fingerprint([]string{"a", "bc"}); string(a) == string(b) {
		t.Fatal("the fingerprint must not join candidates ambiguously")
	}
}

func TestBloomSize_Table(t *testing.T) {
	for _, tc := range []struct {
		n int
		m uint64
		k int
	}{
		{100, 512, 4},
		{10, 64, 4},
		{1, 64, 8},
		{200, 1024, 4},
		{500, 4096, 6},
		{1000, 8192, 6},
		{103, 1024, 7},
	} {
		m, k := flagfilter.BloomSize(tc.n)
		if m != tc.m || k != tc.k {
			t.Fatalf("n=%d: m=%d k=%d, want m=%d k=%d", tc.n, m, k, tc.m, tc.k)
		}
	}
}

func TestPositions_DeterministicForAFixedSalt(t *testing.T) {
	salt := bytes.Repeat([]byte{7}, flagfilter.SaltSize)
	a := flagfilter.Positions(salt, "ICE{flag}", 4096, 6)
	b := flagfilter.Positions(salt, "ICE{flag}", 4096, 6)
	if len(a) != 6 {
		t.Fatalf("positions = %v", a)
	}
	for i := range a {
		if a[i] != b[i] || a[i] >= 4096 {
			t.Fatalf("positions differ or out of range: %v %v", a, b)
		}
	}
	other := flagfilter.Positions(bytes.Repeat([]byte{8}, flagfilter.SaltSize), "ICE{flag}", 4096, 6)
	same := true
	for i := range a {
		same = same && a[i] == other[i]
	}
	if same {
		t.Fatal("another salt gives the same positions")
	}
}

// 10k inserts into 4096 bits: every bit gets hit and no 64-bit word is wildly
// above the mean (loose bounds, fixed seed).
func TestPositions_FillIsRoughlyUniform(t *testing.T) {
	salt := bytes.Repeat([]byte{1}, flagfilter.SaltSize)
	src := mrand.New(seeded(3))
	const m = 4096
	counts := make([]int, m/64)
	for i := range 10000 {
		flag := "ICE{" + string(rune('a'+i%26)) + string(rune('a'+src.IntN(26))) + string(rune('0'+src.IntN(10))) + "}" + string(rune(i))
		for _, p := range flagfilter.Positions(salt, flag, m, 4) {
			counts[p/64]++
		}
	}
	mean := 10000 * 4 / (m / 64)
	for word, c := range counts {
		if c < mean/2 || c > mean*3/2 {
			t.Fatalf("word %d got %d hits, mean %d", word, c, mean)
		}
	}
}

func TestBloom_NoFalseNegatives(t *testing.T) {
	set := mustSet(t, `template:ICE{\l\l\l\l\l\l\d\d}`)
	f, err := flagfilter.New(set, 100, nil, seeded(1))
	if err != nil {
		t.Fatal(err)
	}
	if f.Mode != flagfilter.ModeBloom || f.Size != 512 || f.Hashes != 4 {
		t.Fatalf("shape = %d/%d/%d", f.Mode, f.Size, f.Hashes)
	}
	var issued []string
	for range 100 {
		flag, issueErr := f.Issue(set, seeded(byte(len(issued)+10)))
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		issued = append(issued, flag)
	}
	for _, flag := range issued {
		if !f.Contains(set, flag) {
			t.Fatalf("issued %q not present", flag)
		}
	}
	rebuilt, err := flagfilter.New(set, 100, issued, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range issued {
		if !rebuilt.Contains(set, flag) {
			t.Fatalf("rebuilt filter misses %q", flag)
		}
	}
	if rebuilt.Inserted != 100 {
		t.Fatalf("rebuilt inserted = %d", rebuilt.Inserted)
	}
}

// 100 teams plus the moderators team on a large template: all distinct.
func TestBloom_HundredTeamsAndModeratorsAreDistinct(t *testing.T) {
	set := mustSet(t, `template:ICE{[a-f]\d\d\d}`) // 6000 values
	f, err := flagfilter.New(set, 100, nil, seeded(2))
	if err != nil {
		t.Fatal(err)
	}
	if f.Mode != flagfilter.ModeBloom {
		t.Fatalf("mode = %d", f.Mode)
	}
	src := seeded(9)
	seen := map[string]bool{}
	for range 101 {
		flag, issueErr := f.Issue(set, src)
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		if seen[flag] {
			t.Fatalf("repeated %q", flag)
		}
		seen[flag] = true
	}
	if f.Resets != 0 {
		t.Fatalf("resets = %d", f.Resets)
	}
	if !f.Full() {
		t.Fatal("101 flags in a 100 filter is not full")
	}
}

// Ten values, ten teams: all distinct; the eleventh resets the bitmap.
func TestBitmap_TenValuesThenReset(t *testing.T) {
	set := mustSet(t, `template:ICE{\d}`)
	f, err := flagfilter.New(set, 100, nil, seeded(4))
	if err != nil {
		t.Fatal(err)
	}
	if f.Mode != flagfilter.ModeBitmap || f.Size != 10 {
		t.Fatalf("shape = %d/%d", f.Mode, f.Size)
	}
	if !flagfilter.SmallSet(set, 100) {
		t.Fatal("10 values for 100 teams is not small")
	}
	src := seeded(5)
	seen := map[string]bool{}
	for range 10 {
		flag, issueErr := f.Issue(set, src)
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		if seen[flag] {
			t.Fatalf("repeated %q", flag)
		}
		seen[flag] = true
	}
	if f.Resets != 0 {
		t.Fatalf("early reset")
	}
	flag, err := f.Issue(set, src)
	if err != nil {
		t.Fatal(err)
	}
	if f.Resets != 1 || f.Inserted != 1 || !seen[flag] {
		t.Fatalf("after reset: resets=%d inserted=%d flag=%q", f.Resets, f.Inserted, flag)
	}
}

// Overlapping candidates: the bitmap is by value, so ICE{a0} is issued once.
func TestBitmap_OverlappingCandidatesStayDistinct(t *testing.T) {
	set := mustSet(t, `ICE{a0}`, `template:ICE{[ab]0}`) // N=3, two distinct values
	f, err := flagfilter.New(set, 100, nil, seeded(6))
	if err != nil {
		t.Fatal(err)
	}
	src := seeded(7)
	a, _ := f.Issue(set, src)
	b, _ := f.Issue(set, src)
	if a == b || f.Resets != 0 {
		t.Fatalf("flags %q %q resets %d", a, b, f.Resets)
	}
	if _, err = f.Issue(set, src); err != nil || f.Resets != 1 {
		t.Fatalf("third issue: resets %d, %v", f.Resets, err)
	}
}

// Every value of a tiny bloom-mode set is taken: after 64 rejected draws the
// filter resets and issues the draw.
func TestBloom_ResetsAfterSixtyFourRejects(t *testing.T) {
	set := mustSet(t, `template:ICE{\l\l}`) // 676 values > 64 bits for capacity 10
	f, err := flagfilter.New(set, 10, nil, seeded(8))
	if err != nil {
		t.Fatal(err)
	}
	if f.Mode != flagfilter.ModeBloom || f.Size != 64 {
		t.Fatalf("shape = %d/%d", f.Mode, f.Size)
	}
	for i := range f.Bits {
		f.Bits[i] = 0xff // every flag looks issued
	}
	flag, err := f.Issue(set, seeded(11))
	if err != nil {
		t.Fatal(err)
	}
	if f.Resets != 1 || f.Inserted != 1 || !f.Contains(set, flag) {
		t.Fatalf("resets=%d inserted=%d", f.Resets, f.Inserted)
	}
	ones := 0
	for _, b := range f.Bits {
		for ; b != 0; b &= b - 1 {
			ones++
		}
	}
	if ones > f.Hashes {
		t.Fatalf("reset kept old bits: %d set", ones)
	}
}

func TestFits_DetectsShapeAndCapacityChanges(t *testing.T) {
	set := mustSet(t, `template:ICE{\l\l\l}`)
	f, err := flagfilter.New(set, 100, nil, seeded(12))
	if err != nil {
		t.Fatal(err)
	}
	if !f.Fits(set, 100) || !f.Fits(set, 50) {
		t.Fatal("same or lower capacity must fit")
	}
	if f.Fits(set, 200) {
		t.Fatal("raised capacity must not fit")
	}
	if f.Fits(mustSet(t, `template:ICE{\d}`), 100) {
		t.Fatal("a bitmap-sized set must not fit a bloom filter")
	}
}
