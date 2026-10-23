package teamChallengeModel

// VariantBalance hands the variants of one event exercise to teams in a
// predictable round robin: a new team gets the variant with the fewest teams
// already on it relative to its weight, ties go to the lowest index. With equal
// weights teams get 0, 1, …, N-1, then 0, 1, … again. Teams on a variant that
// no longer exists are not counted.
type VariantBalance struct{ counts, weights []int }

// NewVariantBalance starts from the teams already on each variant.
func NewVariantBalance(variantCount int, used map[int32]int, weights ...int) (*VariantBalance, error) {
	if variantCount < 1 {
		return nil, ErrTeamChallengeInvalid.Err()
	}
	counts := make([]int, variantCount)
	for index, teams := range used {
		if index >= 0 && int(index) < variantCount {
			counts[index] = teams
		}
	}
	// A variant's weight (1 when not given) makes it come up that many times as often.
	w := make([]int, variantCount)
	for index := range w {
		w[index] = 1
		if index < len(weights) && weights[index] > 1 {
			w[index] = weights[index]
		}
	}
	return &VariantBalance{counts: counts, weights: w}, nil
}

// Pick returns the variant used least relative to its weight (teams ÷ weight; the lowest index on a tie).
// Deterministic: with weights 2 and 1 the order is 0, 1, 0, 0, 1, 0, …
func (b *VariantBalance) Pick() int32 {
	best := 0
	for index, teams := range b.counts {
		// teams/weight < best/bestWeight, in integers.
		if teams*b.weights[best] < b.counts[best]*b.weights[index] {
			best = index
		}
	}
	return int32(best)
}

// Use counts one more team on a variant.
func (b *VariantBalance) Use(index int32) {
	if index >= 0 && int(index) < len(b.counts) {
		b.counts[index]++
	}
}
