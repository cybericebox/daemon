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
	r.POST("sign-in", h.prot.RequireRecaptcha("signIn"), h.signIn)
	r.GET("callback", h.cb)
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
	r.POST("admin-thing", h.prot.RequireRecaptcha("x"), h.doAdmin)
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
