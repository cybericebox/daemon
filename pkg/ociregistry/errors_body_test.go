package ociregistry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func respOf(status int, body string) *http.Response {
	rec := httptest.NewRecorder()
	rec.WriteHeader(status)
	_, _ = rec.WriteString(body)
	return rec.Result()
}

func TestStatusFailureCarriesRegistryCode(t *testing.T) {
	body := `{"errors":[{"code":"DENIED","message":"requested access to the resource is denied"}]}`
	err := statusFailure("put manifest", respOf(http.StatusForbidden, body))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("sentinel lost: %v", err)
	}
	want := "put manifest: registry refused the credentials (status 403: DENIED: requested access to the resource is denied)"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

func TestStatusFailureWithoutParsableBody(t *testing.T) {
	for _, body := range []string{"", "<html>nope</html>", `{"errors":[]}`, `{"errors":[{"code":"","message":""}]}`} {
		err := statusFailure("push blob", respOf(http.StatusForbidden, body))
		if err.Error() != "push blob: registry refused the credentials (status 403)" {
			t.Fatalf("body %q: got %q", body, err.Error())
		}
	}
}

func TestStatusFailureBoundsAndCleansDetail(t *testing.T) {
	long := strings.Repeat("x", 5000)
	body := `{"errors":[{"code":"BAD\n","message":"line1\nline2\x00 ` + long + `"}]}`
	err := statusFailure("put manifest", respOf(http.StatusBadGateway, body))
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("5xx must stay unreachable: %v", err)
	}
	if strings.ContainsAny(err.Error(), "\n\x00") || len(err.Error()) > 400 {
		t.Fatalf("detail not cleaned or bounded (%d): %q", len(err.Error()), err.Error())
	}
}

func TestStatusFailureOnlyFirstTwoKiBRead(t *testing.T) {
	// a valid error object that starts after 2 KiB is not seen
	body := strings.Repeat(" ", 2100) + `{"errors":[{"code":"DENIED","message":"late"}]}`
	err := statusFailure("put manifest", respOf(http.StatusForbidden, body))
	if strings.Contains(err.Error(), "DENIED") {
		t.Fatalf("read past the bound: %v", err)
	}
}

func TestPutManifestReportsRegistryCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[{"code":"DENIED","message":"tag is immutable"}]}`))
	}))
	defer srv.Close()
	c := newClient(srv, "", "")
	_, err := c.PutManifest(context.Background(), "t", "application/vnd.oci.image.manifest.v1+json", []byte("{}"))
	if !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "DENIED: tag is immutable") {
		t.Fatalf("got %v", err)
	}
}
