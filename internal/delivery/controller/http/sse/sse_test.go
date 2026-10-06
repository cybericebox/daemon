package sse

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// streamServer runs a real server with a short WriteTimeout and a handler that
// sends one event every tick until the stream context ends.
func streamServer(t *testing.T, writeTimeout, tick time.Duration, events int) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/live", func(ctx *gin.Context) {
		flusher, streamCtx, cancel, err := Open(ctx)
		if err != nil {
			ctx.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		defer cancel()
		WriteHeaders(ctx)
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		for sent := 0; events == 0 || sent < events; sent++ {
			select {
			case <-streamCtx.Done():
				return
			case <-ticker.C:
				_, _ = fmt.Fprintf(ctx.Writer, "event: tick\ndata: %d\n\n", sent)
				flusher.Flush()
			}
		}
	})
	server := httptest.NewUnstartedServer(engine)
	server.Config.WriteTimeout = writeTimeout
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func readTicks(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status=%d content-type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	ticks := 0
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "event: tick") {
			ticks++
		}
	}
	return ticks
}

func TestOpenKeepsStreamAlivePastServerWriteTimeout(t *testing.T) {
	server := streamServer(t, 200*time.Millisecond, 50*time.Millisecond, 16)

	start := time.Now()
	if got := readTicks(t, server.URL+"/live"); got != 16 {
		t.Fatalf("stream delivered %d of 16 events in %s", got, time.Since(start))
	}
	if elapsed := time.Since(start); elapsed < 600*time.Millisecond {
		t.Fatalf("stream ended in %s, before outliving the 200ms write timeout", elapsed)
	}
}

func TestOpenEndsStreamAtMaxLifetime(t *testing.T) {
	previous := maxLifetime
	maxLifetime = 300 * time.Millisecond
	t.Cleanup(func() { maxLifetime = previous })
	server := streamServer(t, 100*time.Millisecond, 50*time.Millisecond, 0)

	start := time.Now()
	got := readTicks(t, server.URL+"/live")
	if elapsed := time.Since(start); elapsed > 2*time.Second || got < 3 {
		t.Fatalf("stream delivered %d events and ended after %s", got, elapsed)
	}
}

func TestHeartbeatIsANamedEvent(t *testing.T) {
	recorder := httptest.NewRecorder()
	Heartbeat(recorder, recorder)
	if got := recorder.Body.String(); got != "event: heartbeat\ndata: {}\n\n" {
		t.Fatalf("heartbeat = %q", got)
	}
	if !recorder.Flushed {
		t.Fatal("heartbeat must be flushed")
	}
}

// L18: one caller cannot hold an unbounded number of streams open.
func TestLimiterCapsStreamsPerKeyAndReleases(t *testing.T) {
	var l Limiter
	var releases []func()
	for i := 0; i < 3; i++ {
		release, ok := l.Acquire("user-1", 3)
		if !ok {
			t.Fatalf("stream %d must be admitted", i)
		}
		releases = append(releases, release)
	}
	if _, ok := l.Acquire("user-1", 3); ok {
		t.Fatal("the 4th stream of one key must be refused")
	}
	if _, ok := l.Acquire("user-2", 3); !ok {
		t.Fatal("another key has its own budget")
	}
	releases[0]()
	releases[0]() // releasing twice frees one slot only
	if _, ok := l.Acquire("user-1", 3); !ok {
		t.Fatal("a released slot is reusable")
	}
	if _, ok := l.Acquire("user-1", 3); ok {
		t.Fatal("a double release must not free two slots")
	}
}
