package flagpattern

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/big"
)

// ErrNoCandidates is returned by ResolveAt for a task without candidates: the
// full random mode has no numbering, so it never resolves by slot.
var ErrNoCandidates = errors.New("flag slot needs at least one candidate")

// feistelRounds is the number of Feistel rounds of Permute.
const feistelRounds = 8

// Numbering of the outputs of a candidate list (stable; stored flags depend on it):
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
	patterns []Pattern
	total    *big.Int
}

// ParseSet parses candidates in author order. An empty list is a valid set of
// size 0 (the full random mode).
func ParseSet(candidates []string) (Set, error) {
	set := Set{patterns: make([]Pattern, 0, len(candidates)), total: big.NewInt(0)}
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

// Permute is P_k, a keyed bijection on [0, n): a balanced Feistel network on
// 2h bits (h the smallest value >= 1 with 2^(2h) >= n) with cycle walking until
// the result is below n. x splits into L (high h bits) and R (low h bits); each
// of the 8 rounds maps (L, R) to (R, L XOR F(round, R)), where F is
// HMAC-SHA256(key, round byte || R as ceil(h/8) big-endian bytes), its first
// ceil(h/8) bytes read big-endian and cut to the low h bits. When h is wider
// than one digest (h > 256), F continues with HMAC-SHA256(key, round byte || R
// || block as 4 big-endian bytes) for block = 1, 2, ... appended in order.
func Permute(key []byte, n, x *big.Int) (*big.Int, error) {
	if n == nil || x == nil || n.Sign() <= 0 || x.Sign() < 0 || x.Cmp(n) >= 0 {
		return nil, ErrInvalidPattern
	}
	h := halfBits(n)
	f := feistel{key: key, h: h, width: (h + 7) / 8, mask: new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(h)), big.NewInt(1))}
	y := new(big.Int).Set(x)
	for {
		y = f.encrypt(y)
		if y.Cmp(n) < 0 {
			return y, nil
		}
	}
}

// halfBits is the smallest h >= 1 with 2^(2h) >= n.
func halfBits(n *big.Int) int {
	need := new(big.Int).Sub(n, big.NewInt(1)).BitLen() // 2^bits >= n
	h := (need + 1) / 2
	if h < 1 {
		h = 1
	}
	return h
}

type feistel struct {
	key   []byte
	h     int
	width int
	mask  *big.Int
}

func (f feistel) encrypt(x *big.Int) *big.Int {
	left := new(big.Int).Rsh(x, uint(f.h))
	right := new(big.Int).And(x, f.mask)
	for round := range feistelRounds {
		next := new(big.Int).Xor(left, f.round(byte(round), right))
		left, right = right, next
	}
	return left.Lsh(left, uint(f.h)).Or(left, right)
}

func (f feistel) round(round byte, right *big.Int) *big.Int {
	input := make([]byte, 1+f.width, 1+f.width+4)
	input[0] = round
	right.FillBytes(input[1:])
	stream := make([]byte, 0, f.width+sha256.Size)
	for block := uint32(0); len(stream) < f.width; block++ {
		mac := hmac.New(sha256.New, f.key)
		mac.Write(input)
		if block > 0 {
			mac.Write(binary.BigEndian.AppendUint32(nil, block))
		}
		stream = mac.Sum(stream)
	}
	out := new(big.Int).SetBytes(stream[:f.width])
	return out.And(out, f.mask)
}

// ResolveAt is the flag of slot r: the value at g = P_key(r mod N). Slots
// 0..N-1 give N distinct outputs of the numbering; the slot means nothing here.
func ResolveAt(candidates []string, slot *big.Int, key []byte) (string, error) {
	if len(candidates) == 0 {
		return "", ErrNoCandidates
	}
	if slot == nil || slot.Sign() < 0 {
		return "", ErrInvalidPattern
	}
	set, err := ParseSet(candidates)
	if err != nil {
		return "", err
	}
	g, err := Permute(key, set.total, new(big.Int).Mod(slot, set.total))
	if err != nil {
		return "", err
	}
	return set.At(g)
}
