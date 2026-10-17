// Package flagfilter keeps the flags already issued for one task variant of an
// event exercise, so a new team gets a value no other team has. A flag value is
// crypto-random over the candidates' outputs; the filter only rejects repeats.
//
//   - Bloom mode: m bits (the smallest power of two >= 5 x capacity, at least
//     64) and k = round(m/capacity x ln2) hashes in [1, 8]. A flag's positions
//     are p_i = (h1 + i x h2) mod m (Kirsch-Mitzenmacher), h1 the first 8 bytes
//     and h2 the next 8 (forced odd) of SHA-256(salt || flag), both big-endian.
//     A flag with any bit 0 was never issued. After MaxRejects consecutive
//     draws that may have been issued the filter resets (new salt) and the last
//     draw is issued: the flags may then repeat.
//   - Bitmap mode, when the set has N <= m outputs: one bit per global index
//     of the set (see flagpattern.Set), exact. A draw picks uniformly among the
//     free indices; when every index is used the bitmap resets (a new round).
//
// Deleted teams never clear bits.
package flagfilter

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"math/big"
	"math/bits"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

type Mode int16

const (
	ModeBloom  Mode = 1
	ModeBitmap Mode = 2
)

const (
	// DefaultCapacity is the team capacity of an event without a team limit.
	DefaultCapacity = 100
	// MaxFilterCapacity bounds the capacity of every filter (and so its size:
	// at most 2^19 bits, 64 KiB), whatever the team limit or the growth.
	MaxFilterCapacity = 100000
	// MaxRejects is the number of consecutive "maybe issued" draws after which a
	// bloom filter resets.
	MaxRejects = 64
	// SaltSize is the length of a filter salt in bytes.
	SaltSize = 16
	minBits  = 64
	maxHash  = 8
)

var ErrInvalidFilter = errors.New("invalid flag filter")

// Capacity is the number of teams a filter is sized for: the event's team
// limit, or DefaultCapacity when the event has none.
func Capacity(maxTeams *int32) int {
	if maxTeams == nil || *maxTeams <= 0 {
		return DefaultCapacity
	}
	return clampCapacity(int(*maxTeams))
}

// GrownCapacity is the capacity a full bloom filter grows to: twice the
// capacity, never above MaxFilterCapacity.
func GrownCapacity(capacity int) int {
	if capacity >= MaxFilterCapacity/2 {
		return MaxFilterCapacity
	}
	return clampCapacity(2 * capacity)
}

func clampCapacity(capacity int) int {
	return min(max(capacity, 1), MaxFilterCapacity)
}

// Fingerprint identifies a candidate list: SHA-256 over each candidate as its
// length (8 bytes, big-endian) followed by its bytes, in author order.
func Fingerprint(candidates []string) []byte {
	h := sha256.New()
	for _, candidate := range candidates {
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(candidate))))
		h.Write([]byte(candidate))
	}
	return h.Sum(nil)
}

// BloomSize is the bit count m and hash count k of a bloom filter for n flags.
func BloomSize(n int) (uint64, int) {
	n = max(n, 1)
	m := uint64(minBits)
	for m < 5*uint64(n) {
		m <<= 1
	}
	k := int(math.Round(float64(m) / float64(n) * math.Ln2))
	return m, min(max(k, 1), maxHash)
}

// Plan is the shape of a filter for a set and a capacity: the mode, its size in
// bits (m for bloom, N for bitmap) and the hash count (0 for bitmap).
func Plan(set flagpattern.Set, capacity int) (Mode, uint64, int) {
	m, k := BloomSize(capacity)
	n := set.Size()
	if n.IsUint64() && n.Uint64() <= m {
		return ModeBitmap, n.Uint64(), 0
	}
	return ModeBloom, m, k
}

// SmallSet reports that the set has fewer outputs than the capacity, so the
// flags of the teams may repeat.
func SmallSet(set flagpattern.Set, capacity int) bool {
	return set.Size().Cmp(big.NewInt(int64(capacity))) < 0
}

// Filter is one stored filter.
type Filter struct {
	// Fingerprint is the Fingerprint of the candidate list the filter was built for.
	Fingerprint []byte
	Mode        Mode
	Salt        []byte
	Hashes      int
	Bits        []byte
	Size        uint64
	Inserted    int
	Capacity    int
	Resets      int
}

// New builds a filter for the set at the capacity with a fresh salt and adds
// the flags already issued (values outside the set are skipped in bitmap mode).
// A bloom filter is sized for at least the number of flags it starts with.
func New(set flagpattern.Set, capacity int, issued []string, source io.Reader) (*Filter, error) {
	if set.Size().Sign() <= 0 {
		return nil, ErrInvalidFilter
	}
	capacity = clampCapacity(capacity)
	mode, size, hashes := Plan(set, capacity)
	if mode == ModeBloom && len(issued) > capacity {
		capacity = clampCapacity(len(issued))
		mode, size, hashes = Plan(set, capacity)
	}
	f := &Filter{Fingerprint: Fingerprint(set.Candidates()), Mode: mode, Size: size, Hashes: hashes, Capacity: capacity, Bits: make([]byte, (size+7)/8)}
	if err := f.newSalt(source); err != nil {
		return nil, err
	}
	for _, flag := range issued {
		f.add(set, flag)
	}
	return f, nil
}

// Fits reports that the stored filter still matches the set and the capacity:
// built for the same candidate list, same mode and size, and sized for at
// least the capacity (capped at MaxFilterCapacity).
func (f *Filter) Fits(set flagpattern.Set, capacity int) bool {
	if f == nil || len(f.Bits) != int((f.Size+7)/8) || len(f.Salt) != SaltSize || !bytes.Equal(f.Fingerprint, Fingerprint(set.Candidates())) {
		return false
	}
	if f.Capacity < clampCapacity(capacity) {
		return false
	}
	mode, size, hashes := Plan(set, f.Capacity)
	return mode == f.Mode && size == f.Size && hashes == f.Hashes
}

// Full reports that a bloom filter holds as many flags as it is sized for and
// may still grow; the caller rebuilds it at GrownCapacity before the next issue.
// A filter at MaxFilterCapacity never grows (it resets when exhausted).
func (f *Filter) Full() bool {
	return f.Mode == ModeBloom && f.Inserted >= f.Capacity && f.Capacity < MaxFilterCapacity
}

// Contains reports that the flag may have been issued (bloom) or was issued in
// this round (bitmap).
func (f *Filter) Contains(set flagpattern.Set, flag string) bool {
	if f.Mode == ModeBitmap {
		indices := set.Indices(flag)
		for _, i := range indices {
			if !f.bit(i.Uint64()) {
				return false
			}
		}
		return len(indices) > 0
	}
	for _, p := range Positions(f.Salt, flag, f.Size, f.Hashes) {
		if !f.bit(p) {
			return false
		}
	}
	return true
}

// Issue draws a new flag of the set with randomness from source and records it.
func (f *Filter) Issue(set flagpattern.Set, source io.Reader) (string, error) {
	if f.Mode == ModeBitmap {
		return f.issueBitmap(set, source)
	}
	for rejects := 1; ; rejects++ {
		g, err := randInt(source, set.Size())
		if err != nil {
			return "", err
		}
		flag, err := set.At(g)
		if err != nil {
			return "", err
		}
		if !f.Contains(set, flag) {
			f.add(set, flag)
			return flag, nil
		}
		if rejects >= MaxRejects {
			if err = f.reset(source); err != nil {
				return "", err
			}
			f.add(set, flag)
			return flag, nil
		}
	}
}

// issueBitmap draws uniformly over the free indices of the set numbering. With
// overlapping candidates a value owned by several indices is more likely to be
// drawn (as in the random mode), but issuing it marks all of its indices, so a
// value never repeats within a round.
func (f *Filter) issueBitmap(set flagpattern.Set, source io.Reader) (string, error) {
	used := uint64(0)
	for _, b := range f.Bits {
		used += uint64(bits.OnesCount8(b))
	}
	if used >= f.Size {
		if err := f.reset(source); err != nil {
			return "", err
		}
		used = 0
	}
	j, err := randInt(source, new(big.Int).SetUint64(f.Size-used))
	if err != nil {
		return "", err
	}
	nth := j.Uint64()
	for i := range f.Size {
		if f.bit(i) {
			continue
		}
		if nth == 0 {
			flag, atErr := set.At(new(big.Int).SetUint64(i))
			if atErr != nil {
				return "", atErr
			}
			f.add(set, flag)
			return flag, nil
		}
		nth--
	}
	return "", ErrInvalidFilter
}

func (f *Filter) add(set flagpattern.Set, flag string) {
	if f.Mode == ModeBitmap {
		indices := set.Indices(flag)
		if len(indices) == 0 {
			return
		}
		for _, i := range indices {
			f.set(i.Uint64())
		}
	} else {
		for _, p := range Positions(f.Salt, flag, f.Size, f.Hashes) {
			f.set(p)
		}
	}
	f.Inserted++
}

func (f *Filter) reset(source io.Reader) error {
	clear(f.Bits)
	f.Inserted = 0
	f.Resets++
	return f.newSalt(source)
}

func (f *Filter) newSalt(source io.Reader) error {
	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(source, salt); err != nil {
		return err
	}
	f.Salt = salt
	return nil
}

func (f *Filter) bit(i uint64) bool { return f.Bits[i/8]&(1<<(i%8)) != 0 }
func (f *Filter) set(i uint64)      { f.Bits[i/8] |= 1 << (i % 8) }

// Positions are the k bloom positions of a flag in m bits.
func Positions(salt []byte, flag string, m uint64, k int) []uint64 {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(flag))
	d := h.Sum(nil)
	h1 := binary.BigEndian.Uint64(d[0:8])
	h2 := binary.BigEndian.Uint64(d[8:16]) | 1
	out := make([]uint64, k)
	for i := range k {
		out[i] = (h1 + uint64(i)*h2) % m
	}
	return out
}

func randInt(source io.Reader, n *big.Int) (*big.Int, error) {
	if n.Sign() <= 0 {
		return nil, ErrInvalidFilter
	}
	return rand.Int(source, n)
}
