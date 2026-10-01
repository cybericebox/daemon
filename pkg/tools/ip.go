package tools

import (
	"net/netip"

	"github.com/cybericebox/daemon/pkg/err"
)

// FromStringToNetPrefixSlice parses CIDR strings into netip prefixes.
func FromStringToNetPrefixSlice(strPrefixes []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(strPrefixes))
	for _, cidr := range strPrefixes {
		prefix, parseErr := netip.ParsePrefix(cidr)
		if parseErr != nil {
			return nil, err.ErrInvalidData.WithError(parseErr).WithMessage("Failed to parse CIDR").Err()
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

// FromNetPrefixToStringSlice renders netip prefixes as CIDR strings.
func FromNetPrefixToStringSlice(prefixes []netip.Prefix) []string {
	out := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		out = append(out, prefix.String())
	}
	return out
}
