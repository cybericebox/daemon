package teamChallengeModel

// VariantBalance hands the variants of one event exercise to teams in a
// predictable round robin: a new team gets the variant with the fewest teams
// already on it, ties go to the lowest index. So teams get 0, 1, …, N-1, then
// 0, 1, … again. Teams on a variant that no longer exists are not counted.
type VariantBalance struct{ counts []int }

// NewVariantBalance starts from the teams already on each variant.
func NewVariantBalance(variantCount int, used map[int32]int) (*VariantBalance, error) {
	if variantCount < 1 {
		return nil, ErrTeamChallengeInvalid.Err()
	}
	counts := make([]int, variantCount)
	for index, teams := range used {
		if index >= 0 && int(index) < variantCount {
			counts[index] = teams
		}
	}
	return &VariantBalance{counts: counts}, nil
}

// Pick returns the least used variant (the lowest index on a tie).
func (b *VariantBalance) Pick() int32 {
	best := 0
	for index, teams := range b.counts {
		if teams < b.counts[best] {
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

// Count is the number of teams counted on a variant so far (0 outside the range).
func (b *VariantBalance) Count(index int32) int {
	if index < 0 || int(index) >= len(b.counts) {
		return 0
	}
	return b.counts[index]
}
