// Package provider holds the provider-specific parts of the platform image registry: listing the
// repositories a token can see and deleting tags. Everything else goes through the OCI client.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/cybericebox/daemon/pkg/ociregistry"
)

const (
	maxBody  = 4 << 20 // bytes read from one API response
	maxPages = 50      // pages followed per listing
)

// Adapter is the provider API surface. Tokens are only ever sent in headers or request bodies.
type Adapter interface {
	// ListRepositories returns "account/name" for every repository the token can see.
	ListRepositories(ctx context.Context, account, token string) ([]string, error)
	// DeleteTags removes tags from a repository ("account/name"); an absent tag is not an error. Where the
	// provider deletes a whole package version (GitHub), a version is deleted only when every tag it carries
	// is among tags; otherwise ErrTagShared and that version stays.
	DeleteTags(ctx context.Context, account, token, repository string, tags []string) error
}

// Default returns the adapter for a provider ("dockerhub" or "ghcr") on its real API, or nil.
func Default(provider string) Adapter {
	switch provider {
	case "dockerhub":
		return NewDockerHub("https://hub.docker.com", nil)
	case "ghcr":
		return NewGitHub("https://api.github.com", nil)
	}
	return nil
}

// ErrTagShared: a tag cannot be removed because its package version carries other tags too (not all given).
var ErrTagShared = errors.New("tag shares its package version with other tags")

var errCrossHost = fmt.Errorf("%w: pagination link points to another host", ociregistry.ErrUnreachable)

// statusErr maps an HTTP status (and, for 403, the rate-limit headers) to a typed error.
func statusErr(op string, code int, h http.Header) error {
	switch {
	case code == http.StatusForbidden && (h.Get("Retry-After") != "" || h.Get("X-RateLimit-Remaining") == "0"):
		return fmt.Errorf("%s: %w (status %d)", op, ociregistry.ErrRateLimited, code)
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Errorf("%s: %w (status %d)", op, ociregistry.ErrUnauthorized, code)
	case code == http.StatusNotFound:
		return fmt.Errorf("%s: %w (status %d)", op, ociregistry.ErrNotFound, code)
	case code == http.StatusTooManyRequests:
		return fmt.Errorf("%s: %w (status %d)", op, ociregistry.ErrRateLimited, code)
	}
	return fmt.Errorf("%s: %w (status %d)", op, ociregistry.ErrUnreachable, code)
}

// call performs one request and returns status, headers and the bounded body. Network errors map to
// ErrUnreachable without echoing the underlying error text.
func call(ctx context.Context, hc *http.Client, op, method, u string, hdr map[string]string, body []byte) (int, http.Header, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%s: %w: bad request", op, ociregistry.ErrUnreachable)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, fmt.Errorf("%s: %w", op, ctx.Err())
		}
		return 0, nil, nil, fmt.Errorf("%s: %w", op, ociregistry.ErrUnreachable)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%s: %w", op, ociregistry.ErrUnreachable)
	}
	return resp.StatusCode, resp.Header, b, nil
}

func sameHost(base, next string) bool {
	b, err1 := url.Parse(base)
	n, err2 := url.Parse(next)
	return err1 == nil && err2 == nil && strings.EqualFold(b.Scheme, n.Scheme) && strings.EqualFold(b.Host, n.Host)
}

func decode(op string, b []byte, v any) error {
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w: malformed response", op, ociregistry.ErrUnreachable)
	}
	return nil
}

// errStop ends a paged listing early without an error.
var errStop = errors.New("stop paging")

// safeClient copies hc and refuses redirects to another host or a scheme downgrade, so the
// Authorization header and the login body are never re-sent elsewhere.
func safeClient(hc *http.Client) *http.Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	c := *hc
	prev := hc.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		first := via[0].URL
		if !strings.EqualFold(req.URL.Host, first.Host) || (first.Scheme == "https" && req.URL.Scheme != "https") {
			return fmt.Errorf("%w: refusing redirect to another host", ociregistry.ErrUnreachable)
		}
		if prev != nil {
			return prev(req, via)
		}
		if len(via) >= 5 {
			return fmt.Errorf("%w: too many redirects", ociregistry.ErrUnreachable)
		}
		return nil
	}
	return &c
}
