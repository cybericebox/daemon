package teamChallengeModel_test

import (
	"testing"

	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// assign hands variants to n new teams one after another, as the stand engine
// does: every pick is counted before the next one.
func assign(t *testing.T, n, variants int, used map[int32]int, weights ...int) []int32 {
	t.Helper()
	balance, err := teamChallengeModel.NewVariantBalance(variants, used, weights...)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int32, 0, n)
	for range n {
		index := balance.Pick()
		balance.Use(index)
		out = append(out, index)
	}
	return out
}

func TestVariantBalance_TwelveTeamsFifteenVariantsAreDistinct(t *testing.T) {
	got := assign(t, 12, 15, nil)
	for i, index := range got {
		if index != int32(i) {
			t.Fatalf("team %d got variant %d, want %d (all: %v)", i, index, i, got)
		}
	}
}

func TestVariantBalance_TwentyTeamsWrapAroundEvenly(t *testing.T) {
	got := assign(t, 20, 15, nil)
	counts := map[int32]int{}
	for i, index := range got {
		if want := int32(i % 15); index != want {
			t.Fatalf("team %d got variant %d, want %d (all: %v)", i, index, want, got)
		}
		counts[index]++
	}
	low, high := 20, 0
	for index := range int32(15) {
		low, high = min(low, counts[index]), max(high, counts[index])
	}
	if high-low > 1 {
		t.Fatalf("uneven counts %v", counts)
	}
}

func TestVariantBalance_FillsTheLowestGapFirst(t *testing.T) {
	got := assign(t, 3, 3, map[int32]int{0: 1, 1: 0, 2: 1})
	if want := []int32{1, 0, 1}; got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("picks = %v, want %v", got, want)
	}
}

func TestVariantBalance_IgnoresVariantsThatNoLongerExist(t *testing.T) {
	// The version shrank from 5 to 2 variants: teams on 3 and 4 do not count.
	got := assign(t, 2, 2, map[int32]int{0: 1, 3: 4, 4: 9})
	if got[0] != 1 || got[1] != 0 {
		t.Fatalf("picks = %v, want [1 0]", got)
	}
}

func TestVariantBalance_PickWithoutUseKeepsCounts(t *testing.T) {
	balance, err := teamChallengeModel.NewVariantBalance(3, map[int32]int{0: 1})
	if err != nil {
		t.Fatal(err)
	}
	if a, b := balance.Pick(), balance.Pick(); a != 1 || b != 1 {
		t.Fatalf("picks without use = %d, %d, want 1, 1", a, b)
	}
}

func TestVariantBalance_RejectsNoVariants(t *testing.T) {
	if _, err := teamChallengeModel.NewVariantBalance(0, nil); err == nil {
		t.Fatal("a version without variants must be refused")
	}
}

func equalPicks(got, want []int32) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestVariantBalance_WeightTwoComesUpTwiceAsOften(t *testing.T) {
	got := assign(t, 9, 2, nil, 2, 1)
	if want := []int32{0, 1, 0, 0, 1, 0, 0, 1, 0}; !equalPicks(got, want) {
		t.Fatalf("picks = %v, want %v", got, want)
	}
}

func TestVariantBalance_WeightedTiesGoToTheLowestIndex(t *testing.T) {
	// Weights 1, 2, 2: after 0, 1, 2 the ratios are 1, 1/2, 1/2, so 1 then 2 come before 0 again.
	got := assign(t, 5, 3, nil, 1, 2, 2)
	if want := []int32{0, 1, 2, 1, 2}; !equalPicks(got, want) {
		t.Fatalf("picks = %v, want %v", got, want)
	}
}

func TestVariantBalance_MissingOrZeroWeightIsOne(t *testing.T) {
	plain := assign(t, 7, 3, nil)
	for _, weights := range [][]int{{0, 0, 0}, {1, 1, 1}, {1}, {-3, 0}} {
		if got := assign(t, 7, 3, nil, weights...); !equalPicks(got, plain) {
			t.Fatalf("weights %v: picks = %v, want the unweighted %v", weights, got, plain)
		}
	}
}

func TestVariantBalance_WeightCountsTeamsAlreadyThere(t *testing.T) {
	// Variant 0 (weight 3) already has 2 teams, variant 1 (weight 1) has 1: 2/3 < 1, so 0 goes next,
	// then 3/3 ties 1/1 and goes to 0 again, then 4/3 > 1 gives 1.
	got := assign(t, 3, 2, map[int32]int{0: 2, 1: 1}, 3, 1)
	if want := []int32{0, 0, 1}; !equalPicks(got, want) {
		t.Fatalf("picks = %v, want %v", got, want)
	}
}
