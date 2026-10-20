// Package ociregistry is a small OCI distribution client: blob HEAD/push, manifest PUT/HEAD and an auth check,
// with the token/basic challenge handled by go-containerregistry's transport.
package ociregistry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/cybericebox/daemon/pkg/email"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// Accept lists the manifest media types asked for on manifest HEAD.
const manifestAccept = "application/vnd.oci.image.manifest.v1+json, application/vnd.oci.image.index.v1+json, " +
	"application/vnd.docker.distribution.manifest.v2+json, application/vnd.docker.distribution.manifest.list.v2+json"

// Options configure a Client. Empty Username and Password mean anonymous access.
type Options struct {
	Host       string
	Repository string
	Username   string
	Password   string
	// Insecure talks plain http (tests only).
	Insecure bool
	// AllowInternal lets redirects reach loopback/private addresses (tests only).
	AllowInternal bool
	// Transport is the base round tripper; use GuardedTransport for user-supplied hosts. Default when nil.
	Transport http.RoundTripper
}

// Client talks to one repository of one registry.
type Client struct {
	opts Options
}

// New creates a Client.
func New(opts Options) *Client { return &Client{opts: opts} }

func (c *Client) repo() (name.Repository, error) {
	var nopts []name.Option
	if c.opts.Insecure {
		nopts = append(nopts, name.Insecure)
	}
	r, err := name.NewRepository(c.opts.Host+"/"+c.opts.Repository, nopts...)
	if err != nil {
		return name.Repository{}, fmt.Errorf("%w: %s", ErrInvalidReference, c.scrub(err.Error()))
	}
	// go-containerregistry speaks plain http to localhost, *.local and private hosts; credentials
	// never travel that way (Insecure is for tests only).
	if !c.opts.Insecure && (c.opts.Username != "" || c.opts.Password != "") && r.Registry.Scheme() == "http" {
		return name.Repository{}, fmt.Errorf("%w: plain http registry host", ErrInvalidReference)
	}
	return r, nil
}

// scrub removes the credentials from a message.
func (c *Client) scrub(s string) string {
	if c.opts.Password != "" {
		s = strings.ReplaceAll(s, c.opts.Password, "***")
	}
	return s
}

// wrap turns any failure into one of the sentinels, keeping a scrubbed detail.
func (c *Client) wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	// cancellation and deadlines stay matchable by callers
	for _, ce := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, ce) {
			return fmt.Errorf("%s: %w", op, ce)
		}
	}
	for _, s := range []error{ErrUnauthorized, ErrNotFound, ErrUnreachable, ErrRateLimited, ErrInvalidReference} {
		if errors.Is(err, s) {
			return err
		}
	}
	var te *transport.Error
	if errors.As(err, &te) {
		if s := classifyStatus(te.StatusCode); s != nil {
			return fmt.Errorf("%s: %w (status %d)", op, s, te.StatusCode)
		}
		return fmt.Errorf("%s: %w (status %d)", op, ErrUnreachable, te.StatusCode)
	}
	return fmt.Errorf("%s: %w: %s", op, ErrUnreachable, c.scrub(err.Error()))
}

// http builds an authenticated client for the given scope.
func (c *Client) http(ctx context.Context, repo name.Repository, scope string) (*http.Client, error) {
	var auth authn.Authenticator = authn.Anonymous
	if c.opts.Username != "" || c.opts.Password != "" {
		auth = authn.FromConfig(authn.AuthConfig{Username: c.opts.Username, Password: c.opts.Password})
	}
	base := c.opts.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	rt, err := transport.NewWithContext(ctx, repo.Registry, auth, base, []string{repo.Scope(scope)})
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: rt, CheckRedirect: c.checkRedirect}, nil
}

func (c *Client) base(repo name.Repository) string {
	return repo.Registry.Scheme() + "://" + repo.RegistryStr() + "/v2/" + repo.RepositoryStr()
}

func (c *Client) do(hc *http.Client, req *http.Request, op string) (*http.Response, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, c.wrap(op, err)
	}
	return resp, nil
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
}

const maxRedirects = 10

// checkRedirect is the redirect policy: at most 10 hops, no https to http downgrade, no hop to an internal
// address (unless AllowInternal), and no credentials on a request for another host. Redirects to another
// host are allowed: registries send blob reads to CDN/object-storage hosts.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("%w: stopped after %d redirects", ErrUnreachable, maxRedirects)
	}
	if len(via) == 0 {
		return nil
	}
	first := via[0]
	if first.URL.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("%w: refusing redirect from https to %s", ErrUnreachable, req.URL.Scheme)
	}
	if req.URL.Host == first.URL.Host {
		return nil
	}
	req.Header.Del("Authorization")
	if !c.opts.AllowInternal && hostInternal(req.Context(), req.URL.Hostname()) {
		return fmt.Errorf("%w: refusing redirect to an internal address", ErrUnreachable)
	}
	return nil
}

// hostInternal reports whether a literal address, or any address a name resolves to, is not publicly routable.
func hostInternal(ctx context.Context, host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return email.AddressBlocked(ip)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return false // cannot connect anyway; the dial fails with its own error
	}
	for _, ip := range ips {
		if email.AddressBlocked(ip) {
			return true
		}
	}
	return false
}

// Ping proves the credentials allow pushing to the repository: it opens an upload session (202 = allowed)
// and abandons it. A pull-only token is refused.
func (c *Client) Ping(ctx context.Context) error {
	repo, err := c.repo()
	if err != nil {
		return err
	}
	hc, err := c.http(ctx, repo, transport.PushScope)
	if err != nil {
		return c.wrap("ping", err)
	}
	startURL := c.base(repo) + "/blobs/uploads/"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.do(hc, req, "ping")
	if err != nil {
		return err
	}
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusAccepted {
		defer drain(resp)
		return statusFailure("ping", resp)
	}
	drain(resp)
	// best effort: do not leave the session open
	if base, perr := url.Parse(startURL); perr == nil && loc != "" {
		if u, perr := base.Parse(loc); perr == nil {
			if del, derr := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil); derr == nil {
				if dr, derr := hc.Do(del); derr == nil {
					drain(dr)
				}
			}
		}
	}
	return nil
}

// PingPull proves the credentials can read the repository: a pull-scoped GET /v2/<repo>/tags/list?n=1.
// 200 passes; 401/403 is ErrUnauthorized (a token the registry knows but without access to the repository
// fails here, unlike a bare /v2/ check). 404 passes too: the token was accepted for the pull scope (a refused
// one gets 401/403) and the repository is still empty — a new platform repository before its first push.
func (c *Client) PingPull(ctx context.Context) error {
	repo, err := c.repo()
	if err != nil {
		return err
	}
	hc, err := c.http(ctx, repo, transport.PullScope)
	if err != nil {
		return c.wrap("ping", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base(repo)+"/tags/list?n=1", nil)
	if err != nil {
		return err
	}
	resp, err := c.do(hc, req, "ping")
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return statusFailure("ping", resp)
}

// PingCredentials proves the registry accepts the account without naming a repository (an organizer secret may
// hold a host only): the token or basic exchange runs with no scope, then GET /v2/ must answer 200. A revoked,
// expired or wrong credential is ErrUnauthorized; an account that is valid but cannot read some repository still
// passes. The repository of the Options is ignored.
func (c *Client) PingCredentials(ctx context.Context) error {
	var nopts []name.Option
	if c.opts.Insecure {
		nopts = append(nopts, name.Insecure)
	}
	reg, err := name.NewRegistry(c.opts.Host, nopts...)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidReference, c.scrub(err.Error()))
	}
	if !c.opts.Insecure && (c.opts.Username != "" || c.opts.Password != "") && reg.Scheme() == "http" {
		return fmt.Errorf("%w: plain http registry host", ErrInvalidReference)
	}
	auth := authn.Anonymous
	if c.opts.Username != "" || c.opts.Password != "" {
		auth = authn.FromConfig(authn.AuthConfig{Username: c.opts.Username, Password: c.opts.Password})
	}
	base := c.opts.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	rt, err := transport.NewWithContext(ctx, reg, auth, base, nil)
	if err != nil {
		return c.wrap("ping", err)
	}
	hc := &http.Client{Transport: rt, CheckRedirect: c.checkRedirect}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reg.Scheme()+"://"+reg.RegistryStr()+"/v2/", nil)
	if err != nil {
		return err
	}
	resp, err := c.do(hc, req, "ping")
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return statusFailure("ping", resp)
}

func (c *Client) head(ctx context.Context, url, accept, scope, op string) (bool, error) {
	ok, _, err := c.headSize(ctx, url, accept, scope, op)
	return ok, err
}

// headSize is a HEAD request: found (200) with its Content-Length, not found (404), or an error.
func (c *Client) headSize(ctx context.Context, url, accept, scope, op string) (bool, int64, error) {
	repo, err := c.repo()
	if err != nil {
		return false, 0, err
	}
	hc, err := c.http(ctx, repo, scope)
	if err != nil {
		return false, 0, c.wrap(op, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false, 0, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.do(hc, req, op)
	if err != nil {
		return false, 0, err
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusOK {
		return true, resp.ContentLength, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, 0, nil
	}
	return false, 0, statusFailure(op, resp)
}

// BlobSize reports whether the repository has the blob and its size as the registry states it (HEAD
// Content-Length; -1 when the registry sends none).
func (c *Client) BlobSize(ctx context.Context, digest string) (int64, bool, error) {
	repo, err := c.repo()
	if err != nil {
		return 0, false, err
	}
	ok, size, err := c.headSize(ctx, c.base(repo)+"/blobs/"+digest, "", transport.PullScope, "blob size")
	return size, ok, err
}

// GetBlob reads a small blob (at most max bytes, else ErrBlobTooLarge) and checks it against its sha256 digest.
func (c *Client) GetBlob(ctx context.Context, digest string, max int64) ([]byte, error) {
	repo, err := c.repo()
	if err != nil {
		return nil, err
	}
	hc, err := c.http(ctx, repo, transport.PullScope)
	if err != nil {
		return nil, c.wrap("get blob", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base(repo)+"/blobs/"+digest, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(hc, req, "get blob")
	if err != nil {
		return nil, err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, statusFailure("get blob", resp)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, c.wrap("get blob", err)
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("get blob: %w", ErrBlobTooLarge)
	}
	sum := sha256.Sum256(body)
	if "sha256:"+hex.EncodeToString(sum[:]) != digest {
		return nil, fmt.Errorf("get blob: %w: content does not match its digest", ErrUnreachable)
	}
	return body, nil
}

// BlobExists reports whether the repository has the blob.
func (c *Client) BlobExists(ctx context.Context, digest string) (bool, error) {
	repo, err := c.repo()
	if err != nil {
		return false, err
	}
	return c.head(ctx, c.base(repo)+"/blobs/"+digest, "", transport.PullScope, "blob exists")
}

// ManifestExists reports whether the repository has the manifest (a tag or a digest).
func (c *Client) ManifestExists(ctx context.Context, reference string) (bool, error) {
	repo, err := c.repo()
	if err != nil {
		return false, err
	}
	return c.head(ctx, c.base(repo)+"/manifests/"+reference, manifestAccept, transport.PullScope, "manifest exists")
}

// PushBlob uploads a blob of the given size with one streamed PUT after opening an upload session.
func (c *Client) PushBlob(ctx context.Context, digest string, size int64, r io.Reader) error {
	repo, err := c.repo()
	if err != nil {
		return err
	}
	hc, err := c.http(ctx, repo, transport.PushScope)
	if err != nil {
		return c.wrap("push blob", err)
	}
	startURL := c.base(repo) + "/blobs/uploads/"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.do(hc, req, "push blob")
	if err != nil {
		return err
	}
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusAccepted {
		defer drain(resp)
		return statusFailure("push blob", resp)
	}
	drain(resp)
	base, _ := url.Parse(startURL)
	u, err := base.Parse(loc)
	if err != nil || loc == "" {
		return fmt.Errorf("push blob: %w: bad upload location", ErrUnreachable)
	}
	q := u.Query()
	q.Set("digest", digest)
	u.RawQuery = q.Encode()

	put, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), r)
	if err != nil {
		return err
	}
	put.ContentLength = size
	put.Header.Set("Content-Type", "application/octet-stream")
	resp, err = c.do(hc, put, "push blob")
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		return nil
	}
	return statusFailure("push blob", resp)
}

// PutManifest uploads a manifest under a tag and returns its sha256 digest.
func (c *Client) PutManifest(ctx context.Context, tag, mediaType string, body []byte) (string, error) {
	repo, err := c.repo()
	if err != nil {
		return "", err
	}
	hc, err := c.http(ctx, repo, transport.PushScope)
	if err != nil {
		return "", c.wrap("put manifest", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base(repo)+"/manifests/"+tag, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mediaType)
	resp, err := c.do(hc, req, "put manifest")
	if err != nil {
		return "", err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", statusFailure("put manifest", resp)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
