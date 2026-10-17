package ociregistry

import (
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"

	"github.com/cybericebox/daemon/pkg/email"
)

// GuardedTransport returns a clone of base (a default transport when nil) whose dialer refuses, at connect
// time and after name resolution, loopback, private, link-local, unspecified, multicast and other special
// addresses. Proxies are disabled so the check cannot be bypassed.
func GuardedTransport(base *http.Transport) http.RoundTripper {
	var t *http.Transport
	if base != nil {
		t = base.Clone()
	} else {
		t = http.DefaultTransport.(*http.Transport).Clone()
	}
	t.Proxy = nil
	t.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: guardControl}).DialContext
	return t
}

func guardControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return blocked(address)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || email.AddressBlocked(ip) {
		return blocked(host)
	}
	return nil
}

type blockedError struct{ addr string }

func blocked(addr string) error { return &blockedError{addr: addr} }

func (e *blockedError) Error() string { return "destination " + e.addr + " is not allowed" }
