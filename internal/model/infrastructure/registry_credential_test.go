package infrastructure

import (
	"fmt"
	"strings"
	"testing"
)

func TestRegistryCredentialNeverFormatsThePassword(t *testing.T) {
	c := RegistryCredential{Registry: "ghcr.io", Username: "acme", Password: "pull-token-fixed"}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		out := fmt.Sprintf(verb, c)
		if strings.Contains(out, "pull-token-fixed") {
			t.Fatalf("%s leaks the password: %s", verb, out)
		}
	}
	for _, out := range []string{fmt.Sprint(c), fmt.Sprint([]RegistryCredential{c}), fmt.Sprintf("%+v", struct{ C []RegistryCredential }{[]RegistryCredential{c}}), c.String(), c.GoString()} {
		if strings.Contains(out, "pull-token-fixed") {
			t.Fatalf("leaks the password: %s", out)
		}
		if !strings.Contains(out, "ghcr.io") || !strings.Contains(out, "acme") {
			t.Fatalf("registry and username stay visible: %s", out)
		}
	}
	if c.Password != "pull-token-fixed" {
		t.Fatal("the field itself keeps the value")
	}
}
