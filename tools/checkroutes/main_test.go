package main

import (
	"go/parser"
	"go/token"
	"testing"
)

func parse(t *testing.T, src string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return fileViolations(f)
}

func TestUngatedRouteIsViolation(t *testing.T) {
	got := parse(t, `package x
func (h *H) Init(r *gin.RouterGroup) {
	r.GET("users", h.list)
}`)
	if len(got) != 1 {
		t.Fatalf("want 1 violation, got %v", got)
	}
}

func TestInlineGatePasses(t *testing.T) {
	got := parse(t, `package x
func (h *H) Init(r *gin.RouterGroup) {
	r.GET("users", h.prot.RequirePermission(rbac.PermUsersRead), h.list)
}`)
	if len(got) != 0 {
		t.Fatalf("want 0 violations, got %v", got)
	}
}

func TestGateViaLocalVariablePasses(t *testing.T) {
	got := parse(t, `package x
func (h *H) Init(r *gin.RouterGroup) {
	self := h.prot.RequirePermission(rbac.PermSelf)
	r.GET("me", self, h.me)
}`)
	if len(got) != 0 {
		t.Fatalf("want 0 violations, got %v", got)
	}
}

func TestAllowlistedPublicPathPasses(t *testing.T) {
	got := parse(t, `package x
func (h *H) Init(r *gin.RouterGroup) {
	pub := r.Group("auth")
	pub.POST("sign-in", h.prot.RequireCaptcha("signIn"), h.signIn)
	google := pub.Group("google")
	google.GET("callback", h.cb)
}`)
	if len(got) != 0 {
		t.Fatalf("want 0 violations, got %v", got)
	}
}

// Recaptcha alone must NOT gate a non-public path: it is bot protection,
// not authorization.
func TestRecaptchaOnlyOnPrivatePathIsViolation(t *testing.T) {
	got := parse(t, `package x
func (h *H) Init(r *gin.RouterGroup) {
	r.POST("admin-thing", h.prot.RequireCaptcha("x"), h.doAdmin)
}`)
	if len(got) != 1 {
		t.Fatalf("want 1 violation, got %v", got)
	}
}

func TestGateOnGroupCoversChildren(t *testing.T) {
	got := parse(t, `package x
func (h *H) Init(r *gin.RouterGroup) {
	dispatches := r.Group("dispatches", h.prot.RequirePermission(rbac.PermX))
	dispatches.GET("", h.list)
	dispatches.GET(":id", h.get)
}`)
	if len(got) != 0 {
		t.Fatalf("want 0 violations, got %v", got)
	}
}

func TestUngatedGroupChildIsViolation(t *testing.T) {
	got := parse(t, `package x
func (h *H) Init(r *gin.RouterGroup) {
	open := r.Group("dispatches")
	open.GET("", h.list)
}`)
	if len(got) != 1 {
		t.Fatalf("want 1 violation, got %v", got)
	}
}

// The password policy route is public only on the password group; a "policy"
// route on any other receiver still needs a gate.
func TestPasswordPolicyPublicOnlyOnPasswordGroup(t *testing.T) {
	got := parse(t, `package x
func (h *H) Init(r *gin.RouterGroup) {
	password := r.Group("password")
	password.GET("policy", h.passwordPolicy)
}`)
	if len(got) != 0 {
		t.Fatalf("want 0 violations, got %v", got)
	}

	got = parse(t, `package x
func (h *H) Init(r *gin.RouterGroup) {
	r.GET("policy", h.adminPolicy)
}`)
	if len(got) != 1 {
		t.Fatalf("want 1 violation, got %v", got)
	}
}

// The audit's false negatives. Each used to pass silently.

// A path that is not a literal cannot be matched against the allowlist.
func TestNonLiteralPathIsViolation(t *testing.T) {
	got := parse(t, `package x
func (h *H) Init(r *gin.RouterGroup) {
	r.GET(adminPath, h.secret)
	r.POST(prefix+"/x", h.secret)
}`)
	if len(got) != 2 {
		t.Fatalf("want 2 violations, got %v", got)
	}
}

func TestAnyHandleHeadOptionsAreRoutes(t *testing.T) {
	got := parse(t, `package x
func (h *H) Init(r *gin.RouterGroup) {
	r.Any("a", h.a)
	r.Handle("DELETE", "b", h.b)
	r.HEAD("c", h.c)
	r.OPTIONS("d", h.d)
}`)
	if len(got) != 4 {
		t.Fatalf("want 4 violations, got %v", got)
	}
}

func TestHandleWithGatePasses(t *testing.T) {
	got := parse(t, `package x
func (h *H) Init(r *gin.RouterGroup) {
	r.Handle("DELETE", "b", h.prot.RequirePermission(rbac.PermX), h.b)
	r.Any("a", h.prot.RequirePermission(rbac.PermX), h.a)
}`)
	if len(got) != 0 {
		t.Fatalf("want 0 violations, got %v", got)
	}
}

// "reset" and "callback" are public only as POST/GET on their own groups: a
// DELETE "reset" (or the same literal on another receiver) is a new, ungated route.
func TestPublicLiteralIsPerMethodAndReceiver(t *testing.T) {
	got := parse(t, `package x
func (h *H) Init(r *gin.RouterGroup) {
	password := r.Group("password")
	password.POST("reset", h.reset)
	password.DELETE("reset", h.wipe)
	admin := r.Group("admin")
	admin.POST("reset", h.adminReset)
	admin.GET("callback", h.adminCallback)
}`)
	if len(got) != 3 {
		t.Fatalf("want 3 violations (DELETE reset, admin reset, admin callback), got %v", got)
	}
}

// A group variable's gate belongs to that function and to the assignment, not to
// the name file-wide.
func TestGroupGateDoesNotLeakAcrossFunctions(t *testing.T) {
	got := parse(t, `package x
func (h *H) A(r *gin.RouterGroup) {
	g := r.Group("a", h.prot.RequirePermission(rbac.PermX))
	g.GET("x", h.x)
}
func (h *H) B(r *gin.RouterGroup) {
	g := r.Group("b")
	g.GET("y", h.y)
}`)
	if len(got) != 1 {
		t.Fatalf("want 1 violation (B's group is ungated), got %v", got)
	}
}

func TestGroupReassignedUngatedLosesItsGate(t *testing.T) {
	got := parse(t, `package x
func (h *H) A(r *gin.RouterGroup) {
	g := r.Group("a", h.prot.RequirePermission(rbac.PermX))
	g.GET("x", h.x)
	g = r.Group("b")
	g.GET("y", h.y)
}`)
	if len(got) != 1 {
		t.Fatalf("want 1 violation (the reassigned group), got %v", got)
	}
}

func TestGroupUseGatesFollowingRoutes(t *testing.T) {
	got := parse(t, `package x
func (h *H) A(r *gin.RouterGroup) {
	g := r.Group("a")
	g.Use(h.prot.RequirePermission(rbac.PermX))
	g.GET("x", h.x)
}`)
	if len(got) != 0 {
		t.Fatalf("want 0 violations, got %v", got)
	}
}

// A gate stored in a variable of ANOTHER function is not a gate here.
func TestGateVariableIsFunctionScoped(t *testing.T) {
	got := parse(t, `package x
func (h *H) A(r *gin.RouterGroup) {
	self := h.prot.RequirePermission(rbac.PermSelf)
	r.GET("x", self, h.x)
}
func (h *H) B(r *gin.RouterGroup) {
	r.GET("y", self, h.y)
}`)
	if len(got) != 1 {
		t.Fatalf("want 1 violation, got %v", got)
	}
}
