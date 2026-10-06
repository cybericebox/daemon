package download

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// slowExport writes rows with a pause between them, like a large export
// that is paged out of the database.
func slowExport(extend bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if extend {
			ExtendDeadline(ctx)
		}
		ctx.Header("Content-Type", "text/csv; charset=utf-8")
		ctx.Status(http.StatusOK)
		for row := range 12 {
			_, _ = io.WriteString(ctx.Writer, strings.Repeat("x", 64*1024)+"\n")
			ctx.Writer.Flush()
			if row < 11 {
				time.Sleep(50 * time.Millisecond)
			}
		}
		_, _ = io.WriteString(ctx.Writer, "end\n")
	}
}

func fetch(t *testing.T, extend bool) (string, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/export", slowExport(extend))
	server := httptest.NewUnstartedServer(engine)
	server.Config.WriteTimeout = 200 * time.Millisecond
	server.Start()
	defer server.Close()

	resp, err := http.Get(server.URL + "/export")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func TestExtendDeadlineLetsSlowExportOutliveServerWriteTimeout(t *testing.T) {
	body, err := fetch(t, true)
	if err != nil || !strings.HasSuffix(body, "end\n") {
		t.Fatalf("export was cut off: %d bytes, err=%v", len(body), err)
	}
}

func TestServerWriteTimeoutCutsSlowExportWithoutExtension(t *testing.T) {
	body, err := fetch(t, false)
	if err == nil && strings.HasSuffix(body, "end\n") {
		t.Fatal("the slow export should not finish under a 200ms write timeout; the test no longer proves anything")
	}
}
