package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
)

type fakeAdminLinkIssuer struct {
	link authUseCase.AdminLink
	err  error
}

func (f fakeAdminLinkIssuer) IssueSuperAdminSetupLink(context.Context) (authUseCase.AdminLink, error) {
	return f.link, f.err
}

func TestPrintAdminLinkWritesURLAndExpiry(t *testing.T) {
	var out bytes.Buffer
	exp := time.Date(2026, 10, 5, 12, 30, 0, 0, time.UTC)
	err := printAdminLink(context.Background(), fakeAdminLinkIssuer{link: authUseCase.AdminLink{URL: "https://id.x.test/setup?token=T", ExpiresAt: exp}}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.HasPrefix(got, "https://id.x.test/setup?token=T\n") || !strings.Contains(got, "2026-10-05T12:30:00Z") {
		t.Fatalf("unexpected output %q", got)
	}
}

func TestPrintAdminLinkRefusalPrintsNothing(t *testing.T) {
	var out bytes.Buffer
	err := printAdminLink(context.Background(), fakeAdminLinkIssuer{err: authUseCase.ErrAdminLinkAccountActive}, &out)
	if !errors.Is(err, authUseCase.ErrAdminLinkAccountActive) || out.Len() != 0 {
		t.Fatalf("want the refusal and empty stdout, got %v %q", err, out.String())
	}
}
