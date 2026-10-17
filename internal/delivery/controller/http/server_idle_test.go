package http

import (
	"net/http"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/config"
)

// Every listener keeps an idle connection for IDLE_TIMEOUT, not for READ_TIMEOUT: Cloudflare keeps one HTTP/2
// connection to the origin, and closing it after every 10 s pause made the next request wait for a new one.
func TestNewServer_SetsIdleTimeoutOnEveryListener(t *testing.T) {
	cfg := config.HTTPServerConfig{Host: "127.0.0.1", Port: "0", InternalPort: "0", ReadTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: 5 * time.Minute, MaxHeaderMegabytes: 1}
	s := NewServer(&cfg, "127.0.0.1", http.NotFoundHandler())
	if len(s.listeners) == 0 {
		t.Fatal("no listeners")
	}
	for _, l := range s.listeners {
		if l.srv.IdleTimeout != 5*time.Minute {
			t.Fatalf("%s listener idle timeout %v, want 5m", l.name, l.srv.IdleTimeout)
		}
	}
}
