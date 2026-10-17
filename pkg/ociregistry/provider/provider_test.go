package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cybericebox/daemon/pkg/ociregistry"
)

const tok = "SECRET-TOKEN-VALUE"

type rec struct {
	mu   sync.Mutex
	reqs []string // "METHOD rawURI"
	auth []string
}

func (r *rec) add(q *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, q.Method+" "+q.URL.RequestURI())
	r.auth = append(r.auth, q.Header.Get("Authorization"))
}

func (r *rec) noTokenInURL(t *testing.T) {
	t.Helper()
	for _, s := range r.reqs {
		if strings.Contains(s, tok) {
			t.Fatalf("token in URL: %s", s)
		}
	}
}

func hubServer(t *testing.T, r *rec, h http.HandlerFunc) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		r.add(q)
		if q.URL.Path == "/v2/auth/token" {
			var b map[string]string
			_ = json.NewDecoder(q.Body).Decode(&b)
			if b["identifier"] != "acme" || b["secret"] != tok {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"BEARER1"}`))
			return
		}
		if q.Header.Get("Authorization") != "Bearer BEARER1" {
			w.WriteHeader(401)
			return
		}
		h(w, q)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestDockerHubListPaginates(t *testing.T) {
	r := &rec{}
	var srv *httptest.Server
	srv = hubServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"next":null,"results":[{"name":"c","namespace":"acme"}]}`))
			return
		}
		fmt.Fprintf(w, `{"next":%q,"results":[{"name":"a","namespace":"acme"},{"name":"b","namespace":"acme"}]}`,
			srv.URL+"/v2/namespaces/acme/repositories?page=2&page_size=100")
	})
	got, err := NewDockerHub(srv.URL, srv.Client()).ListRepositories(context.Background(), "acme", tok)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "acme/a,acme/b,acme/c" {
		t.Fatalf("got %v", got)
	}
	r.noTokenInURL(t)
}

func TestDockerHubForeignNextNotFollowed(t *testing.T) {
	r := &rec{}
	evil := &rec{}
	es := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { evil.add(q) }))
	defer es.Close()
	srv := hubServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		fmt.Fprintf(w, `{"next":%q,"results":[{"name":"a","namespace":"acme"}]}`, es.URL+"/steal")
	})
	_, err := NewDockerHub(srv.URL, srv.Client()).ListRepositories(context.Background(), "acme", tok)
	if err == nil {
		t.Fatal("expected error")
	}
	if len(evil.reqs) != 0 {
		t.Fatalf("foreign host contacted: %v", evil.reqs)
	}
	if strings.Contains(err.Error(), tok) {
		t.Fatal("token in error")
	}
}

func TestDockerHubUnauthorized(t *testing.T) {
	r := &rec{}
	srv := hubServer(t, r, func(w http.ResponseWriter, q *http.Request) {})
	_, err := NewDockerHub(srv.URL, srv.Client()).ListRepositories(context.Background(), "acme", "wrong")
	if !errors.Is(err, ociregistry.ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
}

func TestDockerHubRateLimitedAndUnreachable(t *testing.T) {
	r := &rec{}
	srv := hubServer(t, r, func(w http.ResponseWriter, q *http.Request) { w.WriteHeader(429) })
	_, err := NewDockerHub(srv.URL, srv.Client()).ListRepositories(context.Background(), "acme", tok)
	if !errors.Is(err, ociregistry.ErrRateLimited) {
		t.Fatalf("got %v", err)
	}
	url := srv.URL
	srv.Close()
	_, err = NewDockerHub(url, http.DefaultClient).ListRepositories(context.Background(), "acme", tok)
	if !errors.Is(err, ociregistry.ErrUnreachable) || strings.Contains(err.Error(), tok) {
		t.Fatalf("got %v", err)
	}
}

func TestDockerHubDelete(t *testing.T) {
	r := &rec{}
	status := 204
	srv := hubServer(t, r, func(w http.ResponseWriter, q *http.Request) { w.WriteHeader(status) })
	a := NewDockerHub(srv.URL, srv.Client())
	if err := a.DeleteTags(context.Background(), "acme", tok, "acme/app", []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	last := r.reqs[len(r.reqs)-1]
	if last != "DELETE /v2/namespaces/acme/repositories/app/tags/v1" {
		t.Fatalf("got %s", last)
	}
	status = 404
	if err := a.DeleteTags(context.Background(), "acme", tok, "acme/app", []string{"v1"}); err != nil {
		t.Fatalf("missing tag must be nil, got %v", err)
	}
	status = 403
	if err := a.DeleteTags(context.Background(), "acme", tok, "acme/app", []string{"v1"}); !errors.Is(err, ociregistry.ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
	r.noTokenInURL(t)
}

func ghServer(t *testing.T, r *rec, h http.HandlerFunc) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		r.add(q)
		if q.Header.Get("Authorization") != "Bearer "+tok ||
			q.Header.Get("Accept") != "application/vnd.github+json" ||
			q.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			w.WriteHeader(401)
			return
		}
		h(w, q)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestGitHubListOrgPaginates(t *testing.T) {
	r := &rec{}
	var srv *httptest.Server
	srv = ghServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Path != "/orgs/Acme/packages" {
			w.WriteHeader(404)
			return
		}
		if q.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`[{"name":"Sub/Tool"}]`))
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/orgs/Acme/packages?package_type=container&per_page=100&page=2>; rel="next"`, srv.URL))
		_, _ = w.Write([]byte(`[{"name":"App"}]`))
	})
	got, err := NewGitHub(srv.URL, srv.Client()).ListRepositories(context.Background(), "Acme", tok)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "acme/app,acme/sub/tool" {
		t.Fatalf("got %v", got)
	}
	r.noTokenInURL(t)
}

func TestGitHubUserFallback(t *testing.T) {
	r := &rec{}
	srv := ghServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		if strings.HasPrefix(q.URL.Path, "/users/bob/packages") {
			_, _ = w.Write([]byte(`[{"name":"x"}]`))
			return
		}
		w.WriteHeader(404)
	})
	got, err := NewGitHub(srv.URL, srv.Client()).ListRepositories(context.Background(), "bob", tok)
	if err != nil || len(got) != 1 || got[0] != "bob/x" {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestGitHubForeignNextNotFollowed(t *testing.T) {
	r := &rec{}
	evil := &rec{}
	es := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { evil.add(q) }))
	defer es.Close()
	srv := ghServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		w.Header().Set("Link", `<`+es.URL+`/steal>; rel="next"`)
		_, _ = w.Write([]byte(`[{"name":"a"}]`))
	})
	_, err := NewGitHub(srv.URL, srv.Client()).ListRepositories(context.Background(), "acme", tok)
	if err == nil || len(evil.reqs) != 0 {
		t.Fatalf("err=%v evil=%v", err, evil.reqs)
	}
}

func TestGitHubUnauthorizedAndRateLimit(t *testing.T) {
	r := &rec{}
	srv := ghServer(t, r, func(w http.ResponseWriter, q *http.Request) {})
	_, err := NewGitHub(srv.URL, srv.Client()).ListRepositories(context.Background(), "acme", "bad")
	if !errors.Is(err, ociregistry.ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
	srv2 := ghServer(t, r, func(w http.ResponseWriter, q *http.Request) { w.WriteHeader(429) })
	_, err = NewGitHub(srv2.URL, srv2.Client()).ListRepositories(context.Background(), "acme", tok)
	if !errors.Is(err, ociregistry.ErrRateLimited) {
		t.Fatalf("got %v", err)
	}
}

func TestGitHubDeleteTag(t *testing.T) {
	r := &rec{}
	srv := ghServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		switch {
		case q.Method == "GET" && q.URL.Path == "/orgs/acme/packages/container/sub/tool/versions":
			_, _ = w.Write([]byte(`[{"id":11,"metadata":{"container":{"tags":["a"]}}},{"id":12,"metadata":{"container":{"tags":["v1"]}}}]`))
		case q.Method == "DELETE" && q.URL.Path == "/orgs/acme/packages/container/sub/tool/versions/12":
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	})
	a := NewGitHub(srv.URL, srv.Client())
	if err := a.DeleteTags(context.Background(), "acme", tok, "acme/sub/tool", []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	var del string
	for _, s := range r.reqs {
		if strings.HasPrefix(s, "DELETE") {
			del = s
		}
		if strings.HasPrefix(s, "GET") && !strings.Contains(s, "%2F") {
			t.Fatalf("package name not escaped: %s", s)
		}
	}
	if del != "DELETE /orgs/acme/packages/container/sub%2Ftool/versions/12" {
		t.Fatalf("got %q", del)
	}
	r.reqs = nil
	if err := a.DeleteTags(context.Background(), "acme", tok, "acme/sub/tool", []string{"missing"}); err != nil {
		t.Fatalf("absent tag must be nil: %v", err)
	}
	for _, s := range r.reqs {
		if strings.HasPrefix(s, "DELETE") {
			t.Fatalf("unexpected delete %s", s)
		}
	}
	r.noTokenInURL(t)
}

func TestGitHubDeleteUserFallback(t *testing.T) {
	r := &rec{}
	srv := ghServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		switch {
		case q.Method == "GET" && q.URL.Path == "/users/bob/packages/container/x/versions":
			_, _ = w.Write([]byte(`[{"id":5,"metadata":{"container":{"tags":["t"]}}}]`))
		case q.Method == "DELETE" && q.URL.Path == "/users/bob/packages/container/x/versions/5":
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	})
	if err := NewGitHub(srv.URL, srv.Client()).DeleteTags(context.Background(), "bob", tok, "bob/x", []string{"t"}); err != nil {
		t.Fatal(err)
	}
}

func TestDefault(t *testing.T) {
	if Default("dockerhub") == nil || Default("ghcr") == nil || Default("other") != nil {
		t.Fatal("Default mapping wrong")
	}
}

func TestGitHubDeleteSharedVersion(t *testing.T) {
	r := &rec{}
	srv := ghServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		if q.Method == "GET" && q.URL.Path == "/orgs/acme/packages/container/app/versions" {
			_, _ = w.Write([]byte(`[{"id":7,"metadata":{"container":{"tags":["v1","latest"]}}}]`))
			return
		}
		w.WriteHeader(404)
	})
	err := NewGitHub(srv.URL, srv.Client()).DeleteTags(context.Background(), "acme", tok, "acme/app", []string{"v1"})
	if !errors.Is(err, ErrTagShared) {
		t.Fatalf("got %v", err)
	}
	for _, s := range r.reqs {
		if strings.HasPrefix(s, "DELETE") {
			t.Fatalf("unexpected %s", s)
		}
	}
}

func TestGitHubDeleteBothNotFound(t *testing.T) {
	r := &rec{}
	srv := ghServer(t, r, func(w http.ResponseWriter, q *http.Request) { w.WriteHeader(404) })
	err := NewGitHub(srv.URL, srv.Client()).DeleteTags(context.Background(), "acme", tok, "acme/app", []string{"v1"})
	if !errors.Is(err, ociregistry.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestGitHubDeleteStopsPagingWhenFound(t *testing.T) {
	r := &rec{}
	var srv *httptest.Server
	srv = ghServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		switch {
		case q.Method == "GET" && q.URL.Query().Get("page") == "2":
			t.Error("page 2 must not be fetched")
		case q.Method == "GET":
			w.Header().Set("Link", fmt.Sprintf(`<%s/orgs/acme/packages/container/app/versions?page=2>; rel="next"`, srv.URL))
			_, _ = w.Write([]byte(`[{"id":3,"metadata":{"container":{"tags":["v1"]}}}]`))
		default:
			w.WriteHeader(204)
		}
	})
	if err := NewGitHub(srv.URL, srv.Client()).DeleteTags(context.Background(), "acme", tok, "acme/app", []string{"v1"}); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubRateLimit403(t *testing.T) {
	r := &rec{}
	srv := ghServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(403)
	})
	_, err := NewGitHub(srv.URL, srv.Client()).ListRepositories(context.Background(), "acme", tok)
	if !errors.Is(err, ociregistry.ErrRateLimited) {
		t.Fatalf("remaining=0: got %v", err)
	}
	srv2 := ghServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(403)
	})
	_, err = NewGitHub(srv2.URL, srv2.Client()).ListRepositories(context.Background(), "acme", tok)
	if !errors.Is(err, ociregistry.ErrRateLimited) {
		t.Fatalf("retry-after: got %v", err)
	}
}

func TestRedirectToOtherHostRefused(t *testing.T) {
	evil := &rec{}
	es := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { evil.add(q) }))
	defer es.Close()
	r := &rec{}
	gh := ghServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		http.Redirect(w, q, es.URL+"/steal", http.StatusTemporaryRedirect)
	})
	if _, err := NewGitHub(gh.URL, gh.Client()).ListRepositories(context.Background(), "acme", tok); err == nil {
		t.Fatal("expected error")
	}
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		http.Redirect(w, q, es.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer hub.Close()
	if _, err := NewDockerHub(hub.URL, hub.Client()).ListRepositories(context.Background(), "acme", tok); err == nil {
		t.Fatal("expected error")
	}
	if len(evil.reqs) != 0 {
		t.Fatalf("redirect followed: %v", evil.reqs)
	}
}

// A version is deleted when every tag it carries is given; one that also carries a tag not given stays
// (ErrTagShared), after the others were deleted.
func TestGitHubDeleteTagsDeletesAVersionWhoseTagsAllGo(t *testing.T) {
	r := &rec{}
	srv := ghServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		switch {
		case q.Method == "GET" && q.URL.Path == "/orgs/acme/packages/container/app/versions":
			_, _ = w.Write([]byte(`[{"id":7,"metadata":{"container":{"tags":["web-aa","old-aa"]}}},{"id":8,"metadata":{"container":{"tags":["web-bb","keep"]}}}]`))
		case q.Method == "DELETE":
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	})
	a := NewGitHub(srv.URL, srv.Client())
	if err := a.DeleteTags(context.Background(), "acme", tok, "acme/app", []string{"web-aa", "old-aa"}); err != nil {
		t.Fatal(err)
	}
	r.reqs = nil
	err := a.DeleteTags(context.Background(), "acme", tok, "acme/app", []string{"web-aa", "old-aa", "web-bb"})
	if !errors.Is(err, ErrTagShared) {
		t.Fatalf("got %v", err)
	}
	var dels []string
	for _, s := range r.reqs {
		if strings.HasPrefix(s, "DELETE") {
			dels = append(dels, s)
		}
	}
	if strings.Join(dels, ",") != "DELETE /orgs/acme/packages/container/app/versions/7" {
		t.Fatalf("deletes %v", dels)
	}
}

// Docker Hub tags are independent: each is deleted; a failure does not stop the others and is returned.
func TestDockerHubDeleteTagsTriesEveryTag(t *testing.T) {
	r := &rec{}
	srv := hubServer(t, r, func(w http.ResponseWriter, q *http.Request) {
		if strings.HasSuffix(q.URL.Path, "/bad") {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(204)
	})
	err := NewDockerHub(srv.URL, srv.Client()).DeleteTags(context.Background(), "acme", tok, "acme/app", []string{"bad", "ok"})
	if !errors.Is(err, ociregistry.ErrUnreachable) {
		t.Fatalf("got %v", err)
	}
	if last := r.reqs[len(r.reqs)-1]; last != "DELETE /v2/namespaces/acme/repositories/app/tags/ok" {
		t.Fatalf("got %s", last)
	}
}
