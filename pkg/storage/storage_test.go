package storage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A missing object must read as ErrObjectNotFound: the reported bug returned a nil reader and a nil error,
// which the avatar route then streamed (panic, 500).
func TestGetReportsAMissingObjectInsteadOfANilReader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(r.URL.Path, "/")
		if !strings.Contains(path, "/") { // the bucket itself exists
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>no</Message></Error>`))
		}
	}))
	defer srv.Close()
	c, err := New(Config{Endpoint: strings.TrimPrefix(srv.URL, "http://"), AccessKey: "a", SecretKey: "b", Bucket: "bkt", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	rc, _, err := c.Get(context.Background(), "avatars/none")
	if rc != nil || !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("reader %v err %v", rc, err)
	}
}
