package middleware_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
)

func TestUploadTimeoutFollowsTheSizeWithinBounds(t *testing.T) {
	assert.Equal(t, time.Minute, middleware.UploadTimeout(0), "never below a minute")
	assert.Equal(t, time.Minute, middleware.UploadTimeout(1<<20))
	assert.Equal(t, 200*time.Second, middleware.UploadTimeout(50<<20), "a 50 MiB chunk at 256 KiB/s")
	assert.Equal(t, 10*time.Minute, middleware.UploadTimeout(10<<30), "never above ten minutes")
}

// trickleBody sends the body in pieces with a pause between them, longer in total than the server read timeout.
func trickleBody(pieces int, pause time.Duration) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		for range pieces {
			time.Sleep(pause)
			if _, err := pw.Write([]byte("chunk")); err != nil {
				return
			}
		}
		_ = pw.Close()
	}()
	return pr
}

func serveWithShortTimeouts(t *testing.T, extend bool) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.PUT("/chunk", func(c *gin.Context) {
		if extend {
			middleware.ExtendUploadDeadlines(c, 1<<20)
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		time.Sleep(200 * time.Millisecond) // the answer itself is later than the write timeout too
		c.String(http.StatusOK, "%d", len(body))
	})
	srv := httptest.NewUnstartedServer(engine)
	srv.Config.ReadTimeout = 150 * time.Millisecond
	srv.Config.WriteTimeout = 150 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func putSlow(t *testing.T, srv *httptest.Server) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/chunk", trickleBody(4, 100*time.Millisecond))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func TestExtendUploadDeadlinesLetsASlowBodyFinish(t *testing.T) {
	status, body := putSlow(t, serveWithShortTimeouts(t, true))
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "20", body)
}

func TestWithoutTheExtensionASlowBodyIsCutOff(t *testing.T) {
	status, _ := putSlow(t, serveWithShortTimeouts(t, false))
	assert.NotEqual(t, http.StatusOK, status, "the control: the short server timeouts do cut this body")
}
