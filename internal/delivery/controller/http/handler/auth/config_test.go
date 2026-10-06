package auth_test

import (
	"time"

	"github.com/cybericebox/daemon/internal/config"
)

// testAuthConfig is the host set of the handler tests.
var testAuthConfig = config.AuthConfig{
	Hosts: config.HostsConfig{
		Main: "example.test", API: "api.example.test", ID: "id.example.test",
		Admin: "admin.example.test", Exercises: "exercises.example.test", EventDomain: "example.test",
	},
	OAuth: config.OAuthConfig{StateTTL: 10 * time.Minute},
}
