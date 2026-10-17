package flagpattern

import (
	"math/big"
	"strings"
)

// Numbering of the outputs of a candidate list (stable; a stored bitmap flag filter depends on it):
//
//   - Candidates keep the author's order. Candidate i owns n_i outputs: 1 for a
//     fixed string, the product of the per-position choice counts for a template.
//     N = Σ n_i. A global index g in [0, N) falls into candidate i by prefix sums
//     (candidate 0 owns [0, n_0), candidate 1 owns [n_0, n_0+n_1), ...) and has
//     the offset o = g - (n_0 + ... + n_{i-1}) inside it.
//   - A template offset is a mixed-radix number over the template's choice
//     positions, left to right, with the LAST position the least significant
//     digit; each digit indexes that position's choices in their parsed order.
//     So `ICE{[ab][0-2]}` numbers a0, a1, a2, b0, b1, b2 as offsets 0..5.

// Set is a parsed candidate list with the global numbering of its outputs.
type Set struct {
	candidates []string
	patterns   []Pattern
	total      *big.Int
}

// ParseSet parses candidates in author order. An empty list is a valid set of
// size 0 (the full random mode).
func ParseSet(candidates []string) (Set, error) {
	set := Set{candidates: append([]string(nil), candidates...), patterns: make([]Pattern, 0, len(candidates)), total: big.NewInt(0)}
	for _, candidate := range candidates {
		p, err := Parse(candidate)
		if err != nil {
			return Set{}, err
		}
		set.patterns = append(set.patterns, p)
		set.total.Add(set.total, p.count)
	}
	return set, nil
}

// Candidates are the candidate strings of the set, in author order.
func (s Set) Candidates() []string { return append([]string(nil), s.candidates...) }

// Size is N, the number of outputs of the set (overlapping candidates count twice).
func (s Set) Size() *big.Int { return new(big.Int).Set(s.total) }

// At is the value at global index g in [0, N).
func (s Set) At(g *big.Int) (string, error) {
	if g == nil || g.Sign() < 0 || g.Cmp(s.total) >= 0 {
		return "", ErrInvalidPattern
	}
	offset := new(big.Int).Set(g)
	for _, p := range s.patterns {
		if offset.Cmp(p.count) < 0 {
			return p.At(offset)
		}
		offset.Sub(offset, p.count)
	}
	return "", ErrInvalidPattern
}

// At is the output at offset o in [0, Cardinality): o read as a mixed-radix
// number with the last choice position as the least significant digit.
func (p Pattern) At(o *big.Int) (string, error) {
	if o == nil || o.Sign() < 0 || o.Cmp(p.count) >= 0 {
		return "", ErrInvalidPattern
	}
	if !p.template {
		return p.literal, nil
	}
	digits := make([]int, len(p.segments))
	rest := new(big.Int).Set(o)
	digit := new(big.Int)
	for i := len(p.segments) - 1; i >= 0; i-- {
		if choices := p.segments[i].choices; len(choices) > 0 {
			rest.DivMod(rest, big.NewInt(int64(len(choices))), digit)
			digits[i] = int(digit.Int64())
		}
	}
	out := make([]byte, 0, len("ICE{}")+len(p.segments))
	out = append(out, "ICE{"...)
	for i, part := range p.segments {
		if len(part.choices) == 0 {
			out = append(out, part.literal...)
			continue
		}
		out = append(out, part.choices[digits[i]])
	}
	out = append(out, '}')
	return string(out), nil
}

// Indices are every global index whose value is v (more than one when
// candidates overlap), in ascending order; none when v is not an output.
func (s Set) Indices(v string) []*big.Int {
	var out []*big.Int
	base := new(big.Int)
	for _, p := range s.patterns {
		if o, ok := p.Offset(v); ok {
			out = append(out, o.Add(o, base))
		}
		base = new(big.Int).Add(base, p.count)
	}
	return out
}

// Offset is the inverse of At: the offset of v within the pattern, if v is one
// of its outputs.
func (p Pattern) Offset(v string) (*big.Int, bool) {
	if !p.template {
		if v == p.literal {
			return big.NewInt(0), true
		}
		return nil, false
	}
	if !strings.HasPrefix(v, "ICE{") || !strings.HasSuffix(v, "}") || len(v) < len("ICE{}") {
		return nil, false
	}
	body := v[len("ICE{") : len(v)-1]
	offset := new(big.Int)
	for _, part := range p.segments {
		if len(part.choices) == 0 {
			if !strings.HasPrefix(body, part.literal) {
				return nil, false
			}
			body = body[len(part.literal):]
			continue
		}
		if body == "" {
			return nil, false
		}
		digit := strings.IndexByte(string(part.choices), body[0])
		if digit < 0 {
			return nil, false
		}
		offset.Mul(offset, big.NewInt(int64(len(part.choices))))
		offset.Add(offset, big.NewInt(int64(digit)))
		body = body[1:]
	}
	if body != "" {
		return nil, false
	}
	return offset, true
}
