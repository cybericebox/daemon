package infrastructure

import (
	"encoding/json"
	"fmt"
)

// RegistryCredential is one image pull credential the backend hands the laboratory agent with a lab deploy:
// the platform pull credential for platform images, or an event's own secret for an organizer's image.
// Registry is the registry host the agent pulls from (index.docker.io, ghcr.io, an organizer host). The
// password never appears in a formatted value (String, GoString, %v, %+v, %#v): only the deploy path reads it.
type RegistryCredential struct {
	Registry string
	Username string
	Password string
}

const redacted = "[REDACTED]"

func (c RegistryCredential) String() string {
	return fmt.Sprintf("{Registry:%s Username:%s Password:%s}", c.Registry, c.Username, c.redactedPassword())
}

func (c RegistryCredential) GoString() string {
	return fmt.Sprintf("infrastructure.RegistryCredential{Registry:%q, Username:%q, Password:%q}", c.Registry, c.Username, c.redactedPassword())
}

// Format covers every verb, so no fmt path prints the password.
func (c RegistryCredential) Format(f fmt.State, verb rune) {
	if verb == 'v' && f.Flag('#') {
		_, _ = fmt.Fprint(f, c.GoString())
		return
	}
	_, _ = fmt.Fprint(f, c.String())
}

// MarshalJSON writes the password as "***" (empty stays empty): a credential
// never reaches a log or an API body through JSON.
func (c RegistryCredential) MarshalJSON() ([]byte, error) {
	pw := ""
	if c.Password != "" {
		pw = "***"
	}
	return json.Marshal(struct{ Registry, Username, Password string }{c.Registry, c.Username, pw})
}

func (c RegistryCredential) redactedPassword() string {
	if c.Password == "" {
		return ""
	}
	return redacted
}
