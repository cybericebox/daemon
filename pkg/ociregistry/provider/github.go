package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/cybericebox/daemon/pkg/ociregistry"
)

type gitHub struct {
	base string
	hc   *http.Client
}

// NewGitHub returns the GitHub Packages (ghcr.io) adapter. hc defaults to http.DefaultClient.
func NewGitHub(baseURL string, hc *http.Client) Adapter {
	return &gitHub{base: strings.TrimRight(baseURL, "/"), hc: safeClient(hc)}
}

func ghHeaders(token string) map[string]string {
	return map[string]string{
		"Authorization":        "Bearer " + token,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	}
}

// nextLink extracts the rel="next" target of a Link header.
func nextLink(h http.Header) string {
	for _, line := range h.Values("Link") {
		for _, part := range strings.Split(line, ",") {
			segs := strings.Split(part, ";")
			if len(segs) < 2 {
				continue
			}
			target := strings.TrimSpace(segs[0])
			if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
				continue
			}
			for _, p := range segs[1:] {
				if strings.ReplaceAll(strings.TrimSpace(p), " ", "") == `rel="next"` {
					return target[1 : len(target)-1]
				}
			}
		}
	}
	return ""
}

// paged GETs start and follows Link rel="next" within the same host, calling each with a page body.
// A 404 is returned as ErrNotFound so callers can fall back from org to user.
func (g *gitHub) paged(ctx context.Context, op, token, start string, each func(body []byte) error) error {
	next := start
	for page := 0; next != ""; page++ {
		if page >= maxPages {
			return fmt.Errorf("%s: %w: too many pages", op, ociregistry.ErrUnreachable)
		}
		if !sameHost(g.base, next) {
			return fmt.Errorf("%s: %w", op, errCrossHost)
		}
		code, h, b, err := call(ctx, g.hc, op, http.MethodGet, next, ghHeaders(token), nil)
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return statusErr(op, code, h)
		}
		if err := each(b); err != nil {
			if errors.Is(err, errStop) {
				return nil
			}
			return err
		}
		next = nextLink(h)
	}
	return nil
}

// ownerPaths returns the org and user path prefixes for an account.
func ownerPaths(account string) []string {
	a := url.PathEscape(account)
	return []string{"/orgs/" + a, "/users/" + a}
}

func (g *gitHub) ListRepositories(ctx context.Context, account, token string) ([]string, error) {
	var lastErr error
	for _, owner := range ownerPaths(account) {
		var out []string
		err := g.paged(ctx, "list packages", token, g.base+owner+"/packages?package_type=container&per_page=100", func(b []byte) error {
			var items []struct {
				Name string `json:"name"`
			}
			if err := decode("list packages", b, &items); err != nil {
				return err
			}
			for _, it := range items {
				out = append(out, strings.ToLower(account+"/"+it.Name))
			}
			return nil
		})
		if err == nil {
			return out, nil
		}
		if !isNotFound(err) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

func isNotFound(err error) bool { return errors.Is(err, ociregistry.ErrNotFound) }

// DeleteTags deletes the package versions that carry the tags. A version is deleted only when every tag it
// carries is among tags (the tags of one digest sit on one version); a version with other tags too is kept
// and ErrTagShared returned once the others were handled.
func (g *gitHub) DeleteTags(ctx context.Context, account, token, repository string, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	pkg := repository
	if p := strings.ToLower(account) + "/"; strings.HasPrefix(strings.ToLower(repository), p) {
		pkg = repository[len(p):]
	}
	given := make(map[string]bool, len(tags))
	for _, t := range tags {
		given[t] = true
	}
	type version struct {
		id   int64
		tags []string
	}
	var lastErr error
	for _, owner := range ownerPaths(account) {
		base := g.base + owner + "/packages/container/" + url.PathEscape(pkg) + "/versions"
		var found []version
		left := len(given)
		seen := map[string]bool{}
		err := g.paged(ctx, "list versions", token, base+"?per_page=100", func(b []byte) error {
			var items []struct {
				ID       int64 `json:"id"`
				Metadata struct {
					Container struct {
						Tags []string `json:"tags"`
					} `json:"container"`
				} `json:"metadata"`
			}
			if err := decode("list versions", b, &items); err != nil {
				return err
			}
			for _, it := range items {
				carries := false
				for _, t := range it.Metadata.Container.Tags {
					if given[t] && !seen[t] {
						seen[t], carries = true, true
						left--
					}
				}
				if carries {
					found = append(found, version{it.ID, it.Metadata.Container.Tags})
				}
				if left == 0 {
					return errStop
				}
			}
			return nil
		})
		if err != nil {
			if isNotFound(err) {
				lastErr = err
				continue
			}
			return err
		}
		var shared error
		for _, v := range found {
			if slices.ContainsFunc(v.tags, func(t string) bool { return !given[t] }) {
				shared = fmt.Errorf("delete version: %w", ErrTagShared)
				continue
			}
			code, h, _, err := call(ctx, g.hc, "delete version", http.MethodDelete, base+"/"+strconv.FormatInt(v.id, 10), ghHeaders(token), nil)
			if err != nil {
				return err
			}
			if code != http.StatusNotFound && (code < 200 || code >= 300) {
				return statusErr("delete version", code, h)
			}
		}
		return shared
	}
	// neither the org nor the user path shows the package (private packages invisible to the token also
	// answer 404), so report it instead of pretending the tags are gone
	return lastErr
}
