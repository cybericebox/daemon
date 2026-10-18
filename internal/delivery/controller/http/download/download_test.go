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
	defer func() { _ = resp.Body.Close() }()
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

// slowWork stands for a route that works on a big file for longer than the generic deadline, then answers.
func slowWork(t *testing.T, long bool, work time.Duration) (status int, ctxErr error, err error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var handlers []gin.HandlerFunc
	if long {
		handlers = append(handlers, LongOperation())
	}
	handlers = append(handlers, func(ctx *gin.Context) {
		time.Sleep(work)
		ctxErr = ctx.Request.Context().Err()
		ctx.String(http.StatusOK, "done")
	})
	engine.POST("/work", handlers...)
	server := httptest.NewUnstartedServer(engine)
	server.EnableHTTP2 = true // the browser's protocol: the write timeout resets the stream and cancels the request
	server.Config.WriteTimeout = 200 * time.Millisecond
	server.StartTLS()
	defer server.Close()
	resp, err := server.Client().Post(server.URL+"/work", "text/plain", strings.NewReader(""))
	if err != nil {
		return 0, ctxErr, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, err = io.ReadAll(resp.Body)
	return resp.StatusCode, ctxErr, err
}

func TestLongOperationOutlivesTheGenericDeadline(t *testing.T) {
	status, ctxErr, err := slowWork(t, true, 500*time.Millisecond)
	if err != nil || status != http.StatusOK || ctxErr != nil {
		t.Fatalf("status %d ctx %v err %v", status, ctxErr, err)
	}
}

func TestGenericDeadlineStillAppliesToNormalRoutes(t *testing.T) {
	status, ctxErr, err := slowWork(t, false, 500*time.Millisecond)
	if err == nil && status == http.StatusOK && ctxErr == nil {
		t.Fatal("a normal route must still be cut by the write timeout; the test no longer proves anything")
	}
}
