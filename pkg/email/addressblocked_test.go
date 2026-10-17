package email

import (
	"net/netip"
	"testing"
)

func TestAddressBlockedRanges(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "0.0.0.0", "0.1.2.3",
		"100.64.0.1", "100.127.255.255", "192.0.0.8", "198.18.0.1", "198.19.255.255", "240.0.0.1", "255.255.255.255", "224.0.0.1",
		"::1", "::", "::a00:1", "::ffff:10.0.0.1", "::ffff:100.64.0.1", "64:ff9b::808:808", "64:ff9b:1::1", "2002:a00:1::1",
		"fec0::1", "fe80::1", "fc00::1", "ff02::1"} {
		if !AddressBlocked(netip.MustParseAddr(a)) {
			t.Errorf("%s was not blocked", a)
		}
	}
	for _, a := range []string{"8.8.8.8", "1.1.1.1", "140.82.112.3", "2606:4700::1111", "::ffff:8.8.8.8"} {
		if AddressBlocked(netip.MustParseAddr(a)) {
			t.Errorf("%s was blocked", a)
		}
	}
}
