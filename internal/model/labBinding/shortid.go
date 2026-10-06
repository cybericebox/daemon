package labBindingModel

import (
	"math/big"
	"strings"

	"github.com/gofrs/uuid"
)

const (
	// ShortIDLen is the fixed width of a short id: 36^25 > 2^128, so any UUID fits.
	ShortIDLen = 25
	base36     = "0123456789abcdefghijklmnopqrstuvwxyz"
)

var (
	base36Radix = big.NewInt(36)
	maxUUID     = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
)

// ShortID encodes a UUID as 25 lowercase base36 characters, left-padded with
// '0'. It is the same encoding the laboratory operator uses for lab ids, so names
// built from it stay within Kubernetes label limits.
func ShortID(id uuid.UUID) string {
	n := new(big.Int).SetBytes(id.Bytes())
	out := make([]byte, ShortIDLen)
	for i := ShortIDLen - 1; i >= 0; i-- {
		var digit big.Int
		n.DivMod(n, base36Radix, &digit)
		out[i] = base36[digit.Int64()]
	}
	return string(out)
}

// ParseShortID is the inverse of ShortID.
func ParseShortID(s string) (uuid.UUID, bool) {
	if len(s) != ShortIDLen {
		return uuid.Nil, false
	}
	n := new(big.Int)
	for _, c := range s {
		d := strings.IndexRune(base36, c)
		if d < 0 {
			return uuid.Nil, false
		}
		n.Mul(n, base36Radix)
		n.Add(n, big.NewInt(int64(d)))
	}
	if n.Cmp(maxUUID) > 0 {
		return uuid.Nil, false
	}
	raw := make([]byte, 16)
	n.FillBytes(raw)
	id, err := uuid.FromBytes(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}
