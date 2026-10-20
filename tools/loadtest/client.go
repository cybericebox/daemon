package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const sessionCookie = "__Host-session"

// api talks to the backend the way the frontends do: JSON over HTTPS to api.<domain>, the session
// in the cookie, and the Origin header naming the site the request comes from (the event site
// resolves its tenant from it).
type api struct {
	http    *http.Client
	baseURL string // where to connect, e.g. https://api.example.com
	host    string // Host header override for a local stand; empty keeps the URL host
	m       *metrics
}

func newAPI(baseURL, host string, insecure bool, m *metrics, conns int) *api {
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        conns,
		MaxIdleConnsPerHost: conns,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec // opt-in for a local stand
		ForceAttemptHTTP2:   true,
	}
	return &api{http: &http.Client{Transport: transport, Timeout: 60 * time.Second}, baseURL: strings.TrimRight(baseURL, "/"), host: host, m: m}
}

// envelope is the platform answer: Status carries the code, Data the payload.
type envelope struct {
	Status struct {
		Code    int
		Message string
	}
	Data json.RawMessage
}

// result is what a call returns to the flow.
type result struct {
	Status  int
	Data    json.RawMessage
	Code    int // platform status code from the envelope
	Message string
	Header  http.Header
}

func (r result) ok() bool { return r.Status >= 200 && r.Status < 300 }

// request describes one call. Label is the route template the metrics are grouped by.
type request struct {
	Method, Path, Label string
	Origin              string
	Cookie              string
	Body                any
	Header              map[string]string
	// Expect lists 4xx answers the flow treats as normal.
	Expect []int
}

var uuidLike = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F-]{27}`)

// do sends the call, records latency and outcome, and waits out a 429 Retry-After (counted as a
// throttled request, then retried once by the caller's next step, not here).
func (a *api) do(ctx context.Context, rq request) result {
	var body io.Reader
	if rq.Body != nil {
		raw, _ := json.Marshal(rq.Body)
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, rq.Method, a.baseURL+rq.Path, body)
	if err != nil {
		a.m.observe(rq.label(), 0, 0, false)
		return result{}
	}
	if a.host != "" {
		req.Host = a.host
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cybericebox-loadtest")
	if rq.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if rq.Origin != "" {
		req.Header.Set("Origin", rq.Origin)
	}
	if rq.Cookie != "" {
		// The daemon unescapes the value (gin), so the sealed ticket travels query-escaped, as its Set-Cookie writes it.
		req.Header.Set("Cookie", sessionCookie+"="+url.QueryEscape(rq.Cookie))
	}
	for k, v := range rq.Header {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := a.http.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			a.m.observe(rq.label(), 0, time.Since(start), false)
		}
		return result{}
	}
	raw, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	took := time.Since(start)
	if readErr != nil {
		a.m.observe(rq.label(), 0, took, false)
		return result{}
	}
	a.m.observe(rq.label(), resp.StatusCode, took, slices.Contains(rq.Expect, resp.StatusCode))

	out := result{Status: resp.StatusCode, Header: resp.Header}
	var env envelope
	if json.Unmarshal(raw, &env) == nil {
		out.Data, out.Code, out.Message = env.Data, env.Status.Code, env.Status.Message
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		wait := 2 * time.Second
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			wait = time.Duration(min(s, 60)) * time.Second
		}
		sleep(ctx, wait)
	}
	return out
}

func (r request) label() string {
	if r.Label != "" {
		return r.Method + " " + r.Label
	}
	return r.Method + " " + uuidLike.ReplaceAllString(r.Path, "{id}")
}

// sleep waits d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// newUUID is a random v4 UUID (the idempotency key of a submit).
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// cookieFrom extracts the session cookie value from a Set-Cookie header, if present.
func cookieFrom(h http.Header) string {
	for _, c := range (&http.Response{Header: h}).Cookies() {
		if c.Name == sessionCookie {
			if plain, err := url.QueryUnescape(c.Value); err == nil {
				return plain
			}
			return c.Value
		}
	}
	return ""
}
