package handler

import (
	"flag"
	"fmt"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"unsafe"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// This test is the authorization map of the HTTP API. It builds the REAL route
// tree (every handler's Init, with a recording protector), reads each route's
// middleware chain back out of gin and compares the result with
// testdata/routes.golden: for every route, the permissions that gate it and the
// other layers (tenant resolution, per-resource policy, rate limits) in front
// of the handler.
//
// Adding or re-gating a route therefore shows up as a diff of the golden file
// that a reviewer has to read. Run `go test ./internal/delivery/controller/http/handler -run
// TestRoutePolicy -update` after a deliberate change. tools/checkroutes is only the
// fast static pre-check; this is the check that sees what is actually mounted.
var updateGolden = flag.Bool("update", false, "rewrite testdata/routes.golden")

const (
	probeGateKey      = "probe.gate"
	probeRecaptchaKey = "probe.recaptcha"
	goldenPath        = "testdata/routes.golden"
)

// probeProtector records instead of protecting: calling a gate middleware on a
// probe context stores its permission and aborts.
type probeProtector struct{}

func (probeProtector) Authenticate(*gin.Context, string) {}
func (probeProtector) DeAuthenticate(*gin.Context)       {}
func (probeProtector) IssueClientToken(*gin.Context)     {}
func (probeProtector) RequireCaptcha(action string) gin.HandlerFunc {
	return func(c *gin.Context) { c.Set(probeRecaptchaKey, action) }
}
func (probeProtector) RequirePermission(required rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) { c.Set(probeGateKey, required); c.Abort() }
}

type routeSummary struct {
	method, path string
	gates        []rbac.Permission
	recaptcha    bool
	layers       []string // named middlewares between the gate(s) and the handler
}

// routeChains reads every route's handler chain out of the gin engine. gin keeps
// the chains in its unexported radix trees; reflection is the only way to see
// them, and a gin upgrade that moves them fails this test loudly instead of
// silently checking nothing.
func routeChains(t *testing.T, engine *gin.Engine) map[string]gin.HandlersChain {
	t.Helper()
	out := map[string]gin.HandlersChain{}
	field := func(v reflect.Value, name string) reflect.Value {
		f := v.FieldByName(name)
		if !f.IsValid() {
			t.Fatalf("gin internals changed: no field %q on %s; update routeChains", name, v.Type())
		}
		return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
	}
	var walk func(method string, n reflect.Value)
	walk = func(method string, n reflect.Value) {
		if n.IsNil() {
			return
		}
		n = n.Elem()
		handlers := field(n, "handlers").Interface().(gin.HandlersChain)
		if len(handlers) > 0 {
			out[method+" "+field(n, "fullPath").String()] = handlers
		}
		children := field(n, "children")
		for i := 0; i < children.Len(); i++ {
			walk(method, children.Index(i))
		}
	}
	trees := field(reflect.ValueOf(engine).Elem(), "trees")
	for i := 0; i < trees.Len(); i++ {
		tree := trees.Index(i)
		walk(field(tree, "method").String(), field(tree, "root"))
	}
	return out
}

var closureSuffix = regexp.MustCompile(`\.func\d+(\.\d+)*$`)

// layerName is a stable display name of a non-gate middleware.
func layerName(fn gin.HandlerFunc) string {
	name := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	name = strings.TrimSuffix(name, "-fm")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = closureSuffix.ReplaceAllString(name, ".func")
	return strings.NewReplacer("(*Handler).", "", "handler.", "").Replace(name)
}

// probe runs one middleware on an empty context and reports what it did.
func probe(fn gin.HandlerFunc) (gate *rbac.Permission, recaptcha bool) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	func() {
		defer func() { _ = recover() }() // a middleware that needs its use case panics on the nil one: it is a layer
		fn(c)
	}()
	if v, ok := c.Get(probeGateKey); ok {
		p := v.(rbac.Permission)
		return &p, false
	}
	_, recaptcha = c.Get(probeRecaptchaKey)
	return nil, recaptcha
}

func buildRoutePolicy(t *testing.T) []routeSummary {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	hosts := config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test", Exercises: "exercises.example.test", EventDomain: "example.test"}
	NewAPIHandler(nil, probeProtector{}, config.AuthConfig{Hosts: hosts}).Init(router)

	var out []routeSummary
	for key, chain := range routeChains(t, router) {
		method, path, _ := strings.Cut(key, " ")
		s := routeSummary{method: method, path: path}
		for i, fn := range chain {
			if i == len(chain)-1 {
				break // the handler itself
			}
			gate, recaptcha := probe(fn)
			switch {
			case gate != nil:
				s.gates = append(s.gates, *gate)
			case recaptcha:
				s.recaptcha = true
			default:
				s.layers = append(s.layers, layerName(fn))
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].method < out[j].method
	})
	return out
}

func (s routeSummary) line() string {
	gates := make([]string, len(s.gates))
	for i, g := range s.gates {
		gates[i] = string(g)
		if rbac.RolePublic.HasPermission(g) {
			gates[i] += "(public)"
		}
	}
	gate := "-"
	if len(gates) > 0 {
		gate = strings.Join(gates, "+")
	}
	extra := append([]string(nil), s.layers...)
	if s.recaptcha {
		extra = append([]string{"recaptcha"}, extra...)
	}
	layers := "-"
	if len(extra) > 0 {
		layers = strings.Join(extra, ",")
	}
	return fmt.Sprintf("%-6s %s | gate: %s | layers: %s", s.method, s.path, gate, layers)
}

func TestRoutePolicy(t *testing.T) {
	var lines []string
	for _, s := range buildRoutePolicy(t) {
		lines = append(lines, s.line())
	}
	got := strings.Join(lines, "\n") + "\n"
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (run with -update to create it)", goldenPath, err)
	}
	if string(want) == got {
		return
	}
	wantSet, gotSet := map[string]bool{}, map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(string(want)), "\n") {
		wantSet[l] = true
	}
	for _, l := range lines {
		gotSet[l] = true
	}
	var diff []string
	for _, l := range lines {
		if !wantSet[l] {
			diff = append(diff, "+ "+l)
		}
	}
	for l := range wantSet {
		if !gotSet[l] {
			diff = append(diff, "- "+l)
		}
	}
	sort.Strings(diff)
	t.Fatalf("the mounted routes differ from %s (review the authorization change, then run with -update):\n%s", goldenPath, strings.Join(diff, "\n"))
}

// publicRoutes are the routes mounted without a real gate: reachable by anyone.
// Each carries the reason it is safe. A route missing from this map that has no
// gate fails TestRouteInvariants; so does an entry that got a gate (stale).
var publicRoutes = map[string]string{
	"POST /api/auth/sign-in":                "credential entry; recaptcha, failure lockout",
	"POST /api/auth/sign-up":                "registration entry; recaptcha, per-recipient mail quota",
	"GET /api/auth/setup":                   "setup-token flow: the token is the credential",
	"POST /api/auth/setup":                  "setup-token flow: the token is the credential",
	"POST /api/auth/password/reset-request": "recaptcha; neutral answer; per-recipient mail quota",
	"POST /api/auth/password/reset":         "single-use emailed code is the credential",
	"GET /api/auth/password/policy":         "static complexity policy (config, no user data)",
	"POST /api/auth/account/email/confirm":  "single-use emailed code is the credential",
	"GET /api/auth/avatar/:id":              "public avatar proxy; the bucket stays private",
	"GET /api/auth/google":                  "OAuth sign-in redirect",
	"GET /api/auth/google/register":         "OAuth registration redirect",
	"GET /api/auth/google/setup":            "OAuth setup-link redirect; the email must match the account",
	"GET /api/auth/google/callback":         "OAuth callback; state cookie double-submit",
	"POST /api/client-token":                "client token of DOS_PROTECTION=on; the provider's bot check, limited by the no-token bucket",
	"GET /api/events/upcoming":              "landing card of the nearest published event (public data only)",
}

// selfWithoutLayerPrefixes are the areas where PermSelf alone is the whole route
// gate by design: the caller's own account, and the exercise catalog whose
// per-exercise policy lives in the exercise use case (actor-scoped).
var selfWithoutLayerPrefixes = map[string]string{
	"/api/auth/":                            "the caller's own account and sessions",
	"/api/exercises":                        "per-exercise policy in the exercise use case",
	"/api/events/:id/teams":                 "participant routes: every query is keyed (event, caller) in the event use case",
	"/api/events/:id/forms":                 "participant routes: every query is keyed (event, caller) in the event use case",
	"/api/events/:id/results/team":          "participant routes: every query is keyed (event, caller) in the event use case",
	"/api/events/:id/results/participation": "participant routes: every query is keyed (event, caller) in the event use case",
}

// notAuthzLayers are middlewares that are in front of a handler but decide
// nothing about who may call it (presence bookkeeping, request-rate limits).
var notAuthzLayers = []string{"touchPresence", "RateLimit", "windowLimiter"}

func authzLayers(s routeSummary) []string {
	var out []string
next:
	for _, l := range s.layers {
		for _, n := range notAuthzLayers {
			if strings.Contains(l, n) {
				continue next
			}
		}
		out = append(out, l)
	}
	return out
}

func hasPrefix(path string, prefixes map[string]string) bool {
	for p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func hasLayer(s routeSummary, names ...string) bool {
	for _, l := range s.layers {
		for _, n := range names {
			if strings.Contains(l, n) {
				return true
			}
		}
	}
	return false
}

// TestRouteInvariants are the structural rules behind the golden file, so a
// regenerated golden cannot launder a mistake.
func TestRouteInvariants(t *testing.T) {
	routes := buildRoutePolicy(t)
	seenPublic := map[string]bool{}
	for _, s := range routes {
		id := s.method + " " + s.path
		var real, public []rbac.Permission
		for _, g := range s.gates {
			if rbac.RolePublic.HasPermission(g) {
				public = append(public, g)
			} else {
				real = append(real, g)
			}
		}

		// 1. Every route has a real gate or is an explicitly justified public route.
		if len(real) == 0 {
			if _, ok := publicRoutes[id]; ok {
				seenPublic[id] = true
				if len(s.gates) > 0 {
					t.Errorf("%s: listed as public but has gate(s) %v: drop it from publicRoutes", id, s.gates)
				}
				continue
			}
			// Public-optional reads: a permission RolePublic holds, GET only.
			if len(public) > 0 && s.method == "GET" {
				continue
			}
			t.Errorf("%s: no gate (gates=%v, layers=%v) and not in publicRoutes", id, s.gates, s.layers)
			continue
		}
		if _, ok := publicRoutes[id]; ok {
			t.Errorf("%s: listed in publicRoutes but it is gated by %v", id, real)
		}

		// 2. PermSelf is "signed in", nothing more: it needs a second layer
		// (tenant / manager / per-resource policy) unless the area is
		// self-service by design.
		onlySelf := len(real) == 1 && real[0] == rbac.PermSelf
		if onlySelf && len(authzLayers(s)) == 0 && !hasPrefix(s.path, selfWithoutLayerPrefixes) {
			t.Errorf("%s: PermSelf is the only gate: add a second layer or list its area in selfWithoutLayerPrefixes", id)
		}

		// 3. The event manage area is only for the event's managers.
		if strings.Contains(s.path, "/manage/") && !hasLayer(s, "requireManage", "requireRead", "requireSections", "requireSensitive") {
			t.Errorf("%s: a /manage/ route without a manager-membership layer (requireManage/requireRead/requireSections)", id)
		}
	}
	for id := range publicRoutes {
		if !seenPublic[id] {
			t.Errorf("publicRoutes lists %s, which is not mounted as a public route any more", id)
		}
	}
}

// The public-media routes (CORP cross-origin, no Referer check) are exactly the ones that use
// middleware.PublicMedia, they are unauthenticated GETs, and no other route is exempt.
func TestPublicMediaRoutesMatchTheMiddlewareSet(t *testing.T) {
	used := map[string]bool{}
	for _, s := range buildRoutePolicy(t) {
		if hasLayer(s, "PublicMedia") {
			if s.method != "GET" {
				t.Errorf("%s %s: public media is read-only", s.method, s.path)
			}
			for _, g := range s.gates {
				if !rbac.RolePublic.HasPermission(g) {
					t.Errorf("%s: public media must be unauthenticated, it has the real gate %v", s.path, g)
				}
			}
			used[s.path] = true
		}
	}
	for path := range middleware.PublicMediaRoutes {
		if !used[path] {
			t.Errorf("%s is exempt in middleware.PublicMediaRoutes but the route does not use PublicMedia", path)
		}
	}
	for path := range used {
		if !middleware.PublicMediaRoutes[path] {
			t.Errorf("%s uses PublicMedia but is not in middleware.PublicMediaRoutes (its Referer would still be checked)", path)
		}
	}
}

// The origin guard's public reads are exactly the mounted GET routes that have no gate or only public
// gates: nothing authenticated is exempt, and no public read is checked by mistake.
func TestPublicReadRoutesMatchTheMountedRouter(t *testing.T) {
	want := map[string]bool{}
	for _, s := range buildRoutePolicy(t) {
		if s.method != "GET" {
			continue
		}
		public := true
		for _, g := range s.gates {
			if !rbac.RolePublic.HasPermission(g) {
				public = false
			}
		}
		if public {
			want[s.path] = true
		}
	}
	for path := range want {
		if !middleware.PublicReadRoutes[path] {
			t.Errorf("%s is a public read but the origin guard would check it: add it to middleware.PublicReadRoutes", path)
		}
	}
	for path := range middleware.PublicReadRoutes {
		if !want[path] {
			t.Errorf("%s is exempt from the origin guard but the router gates it (or does not mount it)", path)
		}
	}
}

// The general request limiter skips these routes: every one must be mounted (a renamed stream would silently
// become limited).
func TestRateLimitExemptRoutesAreMounted(t *testing.T) {
	mounted := map[string]bool{}
	for _, s := range buildRoutePolicy(t) {
		mounted[s.path] = true
	}
	for _, route := range middleware.RateLimitExemptRoutes() {
		if route == "/api/health" {
			continue // registered by the controller, not by the handler aggregator
		}
		if !mounted[route] {
			t.Errorf("%s is exempt from the rate limiter but not mounted", route)
		}
	}
}
