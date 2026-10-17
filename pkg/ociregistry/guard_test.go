package ociregistry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGuardedTransportRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := GuardedTransport(nil).RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not allowed")
}

func TestGuardedTransportRefusesInternalRanges(t *testing.T) {
	for _, addr := range []string{"10.0.0.1:80", "192.168.1.1:80", "169.254.169.254:80", "0.0.0.0:80", "[::1]:80", "[fe80::1]:80"} {
		require.Error(t, guardControl("tcp", addr, nil), addr)
	}
	require.NoError(t, guardControl("tcp", "93.184.216.34:443", nil))
}
