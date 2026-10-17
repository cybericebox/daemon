package infrastructure

import (
	"encoding/json"
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

func TestRegistryCredentialJSONRedactsThePassword(t *testing.T) {
	b, err := json.Marshal([]RegistryCredential{{Registry: "ghcr.io", Username: "acme", Password: "pull-token-fixed"}, {Registry: "quay.io", Username: "bob"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `[{"Registry":"ghcr.io","Username":"acme","Password":"***"},{"Registry":"quay.io","Username":"bob","Password":""}]` {
		t.Fatalf("json %s", b)
	}
}
