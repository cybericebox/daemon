package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cybericebox/daemon/pkg/ociregistry"
)

type dockerHub struct {
	base string
	hc   *http.Client
}

// NewDockerHub returns the Docker Hub adapter. hc defaults to http.DefaultClient.
func NewDockerHub(baseURL string, hc *http.Client) Adapter {
	return &dockerHub{base: strings.TrimRight(baseURL, "/"), hc: safeClient(hc)}
}

func (d *dockerHub) login(ctx context.Context, account, token string) (string, error) {
	body, _ := json.Marshal(map[string]string{"identifier": account, "secret": token})
	code, h, b, err := call(ctx, d.hc, "docker hub login", http.MethodPost, d.base+"/v2/auth/token",
		map[string]string{"Content-Type": "application/json", "Accept": "application/json"}, body)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", statusErr("docker hub login", code, h)
	}
	var r struct {
		AccessToken string `json:"access_token"`
	}
	if err := decode("docker hub login", b, &r); err != nil {
		return "", err
	}
	if r.AccessToken == "" {
		return "", fmt.Errorf("docker hub login: %w", ociregistry.ErrUnauthorized)
	}
	return r.AccessToken, nil
}

func (d *dockerHub) ListRepositories(ctx context.Context, account, token string) ([]string, error) {
	bearer, err := d.login(ctx, account, token)
	if err != nil {
		return nil, err
	}
	hdr := map[string]string{"Authorization": "Bearer " + bearer, "Accept": "application/json"}
	next := d.base + "/v2/namespaces/" + url.PathEscape(account) + "/repositories?page_size=100"
	var out []string
	for page := 0; next != ""; page++ {
		if page >= maxPages {
			return nil, fmt.Errorf("list repositories: %w: too many pages", ociregistry.ErrUnreachable)
		}
		if !sameHost(d.base, next) {
			return nil, fmt.Errorf("list repositories: %w", errCrossHost)
		}
		code, h, b, err := call(ctx, d.hc, "list repositories", http.MethodGet, next, hdr, nil)
		if err != nil {
			return nil, err
		}
		if code != http.StatusOK {
			return nil, statusErr("list repositories", code, h)
		}
		var r struct {
			Next    *string `json:"next"`
			Results []struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"results"`
		}
		if err := decode("list repositories", b, &r); err != nil {
			return nil, err
		}
		for _, it := range r.Results {
			ns := it.Namespace
			if ns == "" {
				ns = account
			}
			out = append(out, ns+"/"+it.Name)
		}
		next = ""
		if r.Next != nil {
			next = *r.Next
		}
	}
	return out, nil
}

// DeleteTags deletes each tag on its own (Docker Hub tags are independent); the first failure is returned
// after the other tags were tried.
func (d *dockerHub) DeleteTags(ctx context.Context, account, token, repository string, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	ns, repo := account, repository
	if i := strings.Index(repository, "/"); i >= 0 {
		ns, repo = repository[:i], repository[i+1:]
	}
	bearer, err := d.login(ctx, account, token)
	if err != nil {
		return err
	}
	var first error
	for _, tag := range tags {
		u := d.base + "/v2/namespaces/" + url.PathEscape(ns) + "/repositories/" + url.PathEscape(repo) + "/tags/" + url.PathEscape(tag)
		code, h, _, err := call(ctx, d.hc, "delete tag", http.MethodDelete, u,
			map[string]string{"Authorization": "Bearer " + bearer, "Accept": "application/json"}, nil)
		if err == nil && code != http.StatusNotFound && (code < 200 || code >= 300) {
			err = statusErr("delete tag", code, h)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}
