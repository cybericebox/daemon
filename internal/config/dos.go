package config

import (
	"errors"
	"fmt"
	"time"
)

// DOSConfig is the protection of the public routes. DOS_PROTECTION=on gives every browser a signed client
// cookie (the "client token") after one invisible bot check, and the anonymous rate limit becomes a bucket per
// cookie; requests without the cookie share one small bucket that only lets the token endpoint, the health
// probe and public media through. off keeps the one shared anonymous bucket (RATE_LIMIT_ANON_*).
//
// Nothing is keyed on the client address (an on-site event shares one router). The buckets are in memory, per
// replica; the per-minute value is the steady refill, the burst is the bucket size.
type DOSConfig struct {
	Protection string `env:"PROTECTION" envDefault:"off"`
	// ClientTokenTTL is how long the client cookie is valid; the frontend fetches a new one after it.
	ClientTokenTTL time.Duration `env:"CLIENT_TOKEN_TTL" envDefault:"24h"`
	// ClientTokenSecret signs the client cookie. Empty derives a key from JWT_TOKEN_SIGNATURE, which every
	// replica shares anyway.
	ClientTokenSecret string `env:"CLIENT_TOKEN_SECRET"`

	// NoTokenPerMinute/NoTokenBurst: the shared bucket of requests without a valid client cookie. It is
	// shared by every first visit of the platform, so an event start (hundreds of people at once) must fit.
	NoTokenPerMinute int `env:"NO_TOKEN_PER_MINUTE" envDefault:"600"`
	NoTokenBurst     int `env:"NO_TOKEN_BURST"      envDefault:"300"`
	// ClientPerMinute/ClientBurst: the bucket of one client cookie.
	ClientPerMinute int `env:"CLIENT_PER_MINUTE" envDefault:"600"`
	ClientBurst     int `env:"CLIENT_BURST"      envDefault:"200"`
	// AuthPerMinute/AuthBurst: the shared bucket of the sign-in, sign-up and password-reset group, so a
	// flood of credential attempts cannot starve the public pages (and the other way round).
	AuthPerMinute int `env:"AUTH_PER_MINUTE" envDefault:"1200"`
	AuthBurst     int `env:"AUTH_BURST"      envDefault:"400"`
	// EventPerMinute/EventBurst: the shared bucket of the public pages of one event, per event tag.
	EventPerMinute int `env:"EVENT_PER_MINUTE" envDefault:"6000"`
	EventBurst     int `env:"EVENT_BURST"      envDefault:"3000"`
}

// Enabled says whether the client token is on.
func (c DOSConfig) Enabled() bool { return c.Protection == "on" }

func (c DOSConfig) Validate() error {
	if c.Protection != "on" && c.Protection != "off" {
		return fmt.Errorf("dos: DOS_PROTECTION must be on or off, got %q", c.Protection)
	}
	if c.ClientTokenTTL < time.Minute {
		return errors.New("dos: DOS_CLIENT_TOKEN_TTL must be at least 1m")
	}
	for _, v := range []int{c.NoTokenPerMinute, c.NoTokenBurst, c.ClientPerMinute, c.ClientBurst,
		c.AuthPerMinute, c.AuthBurst, c.EventPerMinute, c.EventBurst} {
		if v < 1 {
			return errors.New("dos: every DOS_*_PER_MINUTE and DOS_*_BURST must be at least 1")
		}
	}
	return nil
}
