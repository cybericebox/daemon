// Package ociregistry is a small OCI distribution client: blob HEAD/push, manifest PUT/HEAD and an auth check,
// with the token/basic challenge handled by go-containerregistry's transport.
package ociregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

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
		return name.Repository{}, fmt.Errorf("invalid repository reference: %w", err)
	}
	return r, nil
}

// scrub removes the credentials from a message.
func (c *Client) scrub(s string) string {
	for _, secret := range []string{c.opts.Password, c.opts.Username} {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "***")
		}
	}
	return s
}

// wrap turns any failure into one of the sentinels, keeping a scrubbed detail.
func (c *Client) wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	for _, s := range []error{ErrUnauthorized, ErrNotFound, ErrUnreachable, ErrRateLimited} {
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
	return &http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}, nil
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

// Ping checks that the registry answers and accepts the credentials for pushing to the repository.
func (c *Client) Ping(ctx context.Context) error {
	repo, err := c.repo()
	if err != nil {
		return err
	}
	hc, err := c.http(ctx, repo, transport.PushScope)
	if err != nil {
		return c.wrap("ping", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, repo.Registry.Scheme()+"://"+repo.RegistryStr()+"/v2/", nil)
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
	if s := classifyStatus(resp.StatusCode); s != nil {
		return fmt.Errorf("ping: %w (status %d)", s, resp.StatusCode)
	}
	return fmt.Errorf("ping: %w (status %d)", ErrUnreachable, resp.StatusCode)
}

func (c *Client) head(ctx context.Context, url, accept, scope, op string) (bool, error) {
	repo, err := c.repo()
	if err != nil {
		return false, err
	}
	hc, err := c.http(ctx, repo, scope)
	if err != nil {
		return false, c.wrap(op, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.do(hc, req, op)
	if err != nil {
		return false, err
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if s := classifyStatus(resp.StatusCode); s != nil {
		return false, fmt.Errorf("%s: %w (status %d)", op, s, resp.StatusCode)
	}
	return false, fmt.Errorf("%s: %w (status %d)", op, ErrUnreachable, resp.StatusCode)
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
	status := resp.StatusCode
	drain(resp)
	if status != http.StatusAccepted {
		if s := classifyStatus(status); s != nil {
			return fmt.Errorf("push blob: %w (status %d)", s, status)
		}
		return fmt.Errorf("push blob: %w (status %d)", ErrUnreachable, status)
	}
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
	if s := classifyStatus(resp.StatusCode); s != nil {
		return fmt.Errorf("push blob: %w (status %d)", s, resp.StatusCode)
	}
	return fmt.Errorf("push blob: %w (status %d)", ErrUnreachable, resp.StatusCode)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base(repo)+"/manifests/"+tag, strings.NewReader(string(body)))
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
		if s := classifyStatus(resp.StatusCode); s != nil {
			return "", fmt.Errorf("put manifest: %w (status %d)", s, resp.StatusCode)
		}
		return "", fmt.Errorf("put manifest: %w (status %d)", ErrUnreachable, resp.StatusCode)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
