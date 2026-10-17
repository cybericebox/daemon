package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// GetRange must ask the server for exactly bytes offset..offset+length-1 and return only those bytes.
func TestGetRange(t *testing.T) {
	object := []byte("0123456789abcdefghij") // 20 bytes
	var gotRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(r.URL.Path, "/")
		if !strings.Contains(path, "/") { // the bucket itself exists
			w.WriteHeader(http.StatusOK)
			return
		}
		gotRange = r.Header.Get("Range")
		var from, to int
		if _, err := fmt.Sscanf(gotRange, "bytes=%d-%d", &from, &to); err != nil {
			w.Header().Set("Content-Length", strconv.Itoa(len(object)))
			_, _ = w.Write(object)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, to, len(object)))
		w.Header().Set("Content-Length", strconv.Itoa(to-from+1))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Last-Modified", "Sat, 17 Oct 2026 10:00:00 GMT")
		w.WriteHeader(http.StatusPartialContent)
		if r.Method != http.MethodHead {
			_, _ = w.Write(object[from : to+1])
		}
	}))
	defer srv.Close()
	c, err := New(Config{Endpoint: strings.TrimPrefix(srv.URL, "http://"), AccessKey: "a", SecretKey: "b", Bucket: "bkt", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	rc, err := c.GetRange(context.Background(), "blobs/x", 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "56789" || gotRange != "bytes=5-9" {
		t.Fatalf("body %q range %q", got, gotRange)
	}
}
