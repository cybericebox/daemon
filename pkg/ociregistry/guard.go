package ociregistry

import (
	"net"
	"net/http"
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
	t.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: email.GuardControl}).DialContext
	return t
}
