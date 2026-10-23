package teamChallengeModel_test

import (
	"encoding/json"
	"math/rand/v2"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// refBalance is the origin/develop VariantBalance, copied verbatim before the
// weights were added: the fewest teams wins, a tie goes to the lowest index.
type refBalance struct{ counts []int }

func newRefBalance(variantCount int, used map[int32]int) *refBalance {
	counts := make([]int, variantCount)
	for index, teams := range used {
		if index >= 0 && int(index) < variantCount {
			counts[index] = teams
		}
	}
	return &refBalance{counts: counts}
}

func (b *refBalance) Pick() int32 {
	best := 0
	for index, teams := range b.counts {
		if teams < b.counts[best] {
			best = index
		}
	}
	return int32(best)
}

func (b *refBalance) Use(index int32) {
	if index >= 0 && int(index) < len(b.counts) {
		b.counts[index]++
	}
}

// neutralWeights returns a weight list that must mean "no weights": nil, all 1,
// all 0 (unset in JSON), negative, too short, too long, or mixed; and the same
// through Variant.EffectiveWeight, as the event use case computes them.
func neutralWeights(r *rand.Rand, n int) []int {
	switch r.IntN(8) {
	case 0:
		return nil
	case 1:
		return repeat(n, 1)
	case 2:
		return repeat(n, 0)
	case 3:
		return repeat(r.IntN(n+1), 1) // shorter than the variant list
	case 4:
		return repeat(n+1+r.IntN(4), 1) // longer than the variant list
	case 5:
		w := make([]int, n)
		for i := range w {
			w[i] = []int{-7, -1, 0, 1}[r.IntN(4)]
		}
		return w
	default:
		// As variantWeights does: the effective weight of stored variants without a weight.
		w := make([]int, n)
		for i := range w {
			w[i] = exerciseModel.Variant{Weight: []int{0, 1, -1}[r.IntN(3)]}.EffectiveWeight()
		}
		return w
	}
}

func repeat(n, value int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = value
	}
	return out
}

func randomUsed(r *rand.Rand, n int) map[int32]int {
	used := map[int32]int{}
	switch r.IntN(4) {
	case 0: // a fresh event exercise
	case 1: // even start
		k := r.IntN(5)
		for i := range n {
			used[int32(i)] = k
		}
	default:
		for range r.IntN(2 * n) {
			// Includes indexes of variants that no longer exist (>= n) and junk (< 0).
			used[int32(r.IntN(n+4)-1)] = r.IntN(40)
		}
	}
	return used
}

func copyUsed(used map[int32]int) map[int32]int {
	out := make(map[int32]int, len(used))
	for k, v := range used {
		out[k] = v
	}
	return out
}

// Over many random scenarios with no effective weights, the new Pick returns
// exactly what the origin/develop rule returned, step by step.
func TestVariantBalance_EquivalentToOldRuleWithoutWeights(t *testing.T) {
	r := rand.New(rand.NewPCG(20261026, 1))
	const scenarios = 20000
	for s := range scenarios {
		n := 1 + r.IntN(12)
		used := randomUsed(r, n)
		weights := neutralWeights(r, n)
		got, err := teamChallengeModel.NewVariantBalance(n, copyUsed(used), weights...)
		if err != nil {
			t.Fatal(err)
		}
		want := newRefBalance(n, copyUsed(used))
		// counts mirrors the stored teams per variant, for re-reading the balance
		// as a new transaction would after the advisory lock.
		counts := copyUsed(used)
		for step := range 1 + r.IntN(60) {
			switch op := r.IntN(10); {
			case op < 6: // a team arrives: pick and count it
				a, b := got.Pick(), want.Pick()
				if a != b {
					t.Fatalf("scenario %d step %d (n=%d used=%v weights=%v): new pick %d, old pick %d", s, step, n, used, weights, a, b)
				}
				got.Use(a)
				want.Use(b)
				counts[a]++
			case op == 6: // the moderators team: picks, takes no slot
				if a, b := got.Pick(), want.Pick(); a != b {
					t.Fatalf("scenario %d step %d moderators: new %d, old %d", s, step, a, b)
				}
			case op == 7: // another transaction committed a team on some variant
				index := int32(r.IntN(n))
				got.Use(index)
				want.Use(index)
				counts[index]++
			case op == 8: // the next preparation re-reads the counts from the store
				got, err = teamChallengeModel.NewVariantBalance(n, copyUsed(counts), weights...)
				if err != nil {
					t.Fatal(err)
				}
				want = newRefBalance(n, copyUsed(counts))
			default: // a source change removes variants; their teams are re-picked
				if n == 1 {
					continue
				}
				m := 1 + r.IntN(n-1)
				nextWeights := neutralWeights(r, m)
				got, err = teamChallengeModel.NewVariantBalance(m, copyUsed(counts), nextWeights...)
				if err != nil {
					t.Fatal(err)
				}
				want = newRefBalance(m, copyUsed(counts))
				moved := 0
				for index := int32(m); index < int32(n); index++ {
					moved += counts[index]
					delete(counts, index)
				}
				for range moved {
					a, b := got.Pick(), want.Pick()
					if a != b {
						t.Fatalf("scenario %d step %d re-pick (n=%d->%d): new %d, old %d", s, step, n, m, a, b)
					}
					got.Use(a)
					want.Use(b)
					counts[a]++
				}
				n, weights = m, nextWeights
			}
		}
	}
}

// Concurrent-looking sequences: several preparations interleave, each under the
// pick lock, each re-reading the committed counts. Old and new agree.
func TestVariantBalance_EquivalentUnderInterleavedPreparations(t *testing.T) {
	r := rand.New(rand.NewPCG(20261026, 2))
	for s := range 5000 {
		n := 1 + r.IntN(12)
		counts := randomUsed(r, n)
		weights := neutralWeights(r, n)
		for step := range 1 + r.IntN(40) {
			// The lock serializes; one preparation commits, the next reads after it.
			got, err := teamChallengeModel.NewVariantBalance(n, copyUsed(counts), weights...)
			if err != nil {
				t.Fatal(err)
			}
			a, b := got.Pick(), newRefBalance(n, copyUsed(counts)).Pick()
			if a != b {
				t.Fatalf("scenario %d step %d: new %d, old %d (counts %v)", s, step, a, b, counts)
			}
			if r.IntN(5) > 0 { // a rolled-back preparation commits nothing
				counts[a]++
			}
		}
	}
}

// Exhaustive check for small cases: every count vector 0..5 on 1..4 variants.
func TestVariantBalance_EquivalentExhaustiveSmall(t *testing.T) {
	for n := 1; n <= 4; n++ {
		total := 1
		for range n {
			total *= 6
		}
		for code := range total {
			used := map[int32]int{}
			c := code
			for i := range n {
				used[int32(i)] = c % 6
				c /= 6
			}
			for _, weights := range [][]int{nil, repeat(n, 0), repeat(n, 1)} {
				got, _ := teamChallengeModel.NewVariantBalance(n, copyUsed(used), weights...)
				if a, b := got.Pick(), newRefBalance(n, used).Pick(); a != b {
					t.Fatalf("n=%d used=%v weights=%v: new %d, old %d", n, used, weights, a, b)
				}
			}
		}
	}
}

// A stored version from before the weights (no "weight" key) reads as weight 1.
func TestVariant_StoredWithoutWeightIsOne(t *testing.T) {
	var version struct {
		Variants []exerciseModel.Variant `json:"variants"`
	}
	raw := `{"variants":[{"id":"0192f0a0-0000-7000-8000-000000000001","index":0,"note":"","tasks":[],"topology":{}},{"index":1,"tasks":[],"topology":{}}]}`
	if err := json.Unmarshal([]byte(raw), &version); err != nil {
		t.Fatal(err)
	}
	for i, v := range version.Variants {
		if v.Weight != 0 || v.EffectiveWeight() != 1 {
			t.Fatalf("variant %d: weight %d, effective %d; want 0 and 1", i, v.Weight, v.EffectiveWeight())
		}
	}
	encoded, err := json.Marshal(exerciseModel.Variant{})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	_ = json.Unmarshal(encoded, &back)
	if _, has := back["weight"]; has {
		t.Fatalf("an unweighted variant writes no weight key: %s", encoded)
	}
}

// ---- weighted behaviour ----

func TestVariantBalance_WeightsTwoToOneSequence(t *testing.T) {
	// 1-based: 1 2 1 1 2 1 1 2 1 …
	got := assign(t, 12, 2, nil, 2, 1)
	want := []int32{0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0}
	if !equalPicks(got, want) {
		t.Fatalf("picks = %v, want %v", got, want)
	}
}

func TestVariantBalance_LongRunFollowsWeights(t *testing.T) {
	r := rand.New(rand.NewPCG(20261026, 3))
	for range 300 {
		n := 1 + r.IntN(8)
		weights := make([]int, n)
		sum := 0
		for i := range weights {
			weights[i] = 1 + r.IntN(10)
			sum += weights[i]
		}
		teams := sum * (1 + r.IntN(20))
		got := assign(t, teams, n, nil, weights...)
		counts := make([]int, n)
		for _, index := range got {
			counts[index]++
		}
		// After a whole number of rounds, each variant has exactly its share.
		for i := range n {
			if want := teams / sum * weights[i]; counts[i] != want {
				t.Fatalf("weights %v, %d teams: counts %v, variant %d want %d", weights, teams, counts, i, want)
			}
		}
		// Teams ÷ weight with a lowest-index tie means every variant gets its first
		// team before any gets a second, whatever the weights (all start at 0/w = 0).
		for i := range min(n, teams) {
			if got[i] != int32(i) {
				t.Fatalf("weights %v: first round %v, want 0..%d", weights, got[:n], n-1)
			}
		}
		// At every prefix a variant never runs a whole team above its share (upper
		// quota), and never falls n or more teams below it.
		prefix := make([]int, n)
		for k, index := range got {
			prefix[index]++
			for i := range n {
				share := float64(k+1) * float64(weights[i]) / float64(sum)
				if d := float64(prefix[i]) - share; d >= 1+1e-9 || d <= -float64(n) {
					t.Fatalf("weights %v after %d teams: counts %v drift from share at variant %d", weights, k+1, prefix, i)
				}
			}
		}
	}
}

func TestVariantBalance_WeightCapsAtHundred(t *testing.T) {
	for _, w := range []int{100, 101, 1000, 1 << 30} {
		effective := exerciseModel.Variant{Weight: w}.EffectiveWeight()
		if effective != exerciseModel.MaxVariantWeight {
			t.Fatalf("weight %d: effective %d, want %d", w, effective, exerciseModel.MaxVariantWeight)
		}
		got := assign(t, 202, 2, nil, effective, 1)
		counts := [2]int{}
		for _, index := range got {
			counts[index]++
		}
		if counts != [2]int{200, 2} {
			t.Fatalf("weight %d (effective %d) vs 1 over 202 teams: %v, want [200 2]", w, effective, counts)
		}
	}
}

func TestVariantBalance_WeightedTieGoesToLowerIndex(t *testing.T) {
	for _, tc := range []struct {
		used    map[int32]int
		weights []int
		want    int32
	}{
		{map[int32]int{0: 2, 1: 1}, []int{2, 1}, 0}, // 2/2 == 1/1
		{map[int32]int{0: 0, 1: 0, 2: 0}, []int{1, 5, 9}, 0},
		{map[int32]int{0: 3, 1: 2, 2: 4}, []int{3, 2, 4}, 0}, // all 1
		{map[int32]int{0: 5, 1: 2, 2: 4}, []int{3, 2, 4}, 1}, // 5/3 > 1 == 1: lower of 1, 2
	} {
		b, err := teamChallengeModel.NewVariantBalance(len(tc.weights), tc.used, tc.weights...)
		if err != nil {
			t.Fatal(err)
		}
		if got := b.Pick(); got != tc.want {
			t.Fatalf("used %v weights %v: pick %d, want %d", tc.used, tc.weights, got, tc.want)
		}
	}
}

func TestVariantBalance_RemovedVariantRepickedWithWeights(t *testing.T) {
	// Before: 3 variants, 3 teams each. Variant 2 is removed; the new version
	// has weights 2:1. Its 3 teams go to the weighted least-used variant:
	// ratios 3/2 vs 3/1 -> 0; 4/2 vs 3 -> 0; 5/2 vs 3 -> 0. Then 6/2 == 3 -> 0 on a tie.
	b, err := teamChallengeModel.NewVariantBalance(2, map[int32]int{0: 3, 1: 3, 2: 3}, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	var got []int32
	for range 4 {
		index := b.Pick()
		b.Use(index)
		got = append(got, index)
	}
	if want := []int32{0, 0, 0, 0}; !equalPicks(got, want) {
		t.Fatalf("re-picks = %v, want %v", got, want)
	}
	// Without weights the same removal re-picks round robin, as before.
	if got := assign(t, 3, 2, map[int32]int{0: 3, 1: 3, 2: 3}); !equalPicks(got, []int32{0, 1, 0}) {
		t.Fatalf("unweighted re-picks = %v, want [0 1 0]", got)
	}
}
