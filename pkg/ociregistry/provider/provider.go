// Package provider holds the provider-specific parts of the platform image registry: listing the
// repositories a token can see and deleting tags. Everything else goes through the OCI client.
package provider

import (
	"context"
	"encoding/json"
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
	// DeleteTag removes a tag from a repository ("account/name"). An absent tag is not an error.
	DeleteTag(ctx context.Context, account, token, repository, tag string) error
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

var errCrossHost = fmt.Errorf("%w: pagination link points to another host", ociregistry.ErrUnreachable)

func statusErr(op string, code int) error {
	switch {
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
