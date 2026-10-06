package event_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
)

type logoStreamFake struct{ eventHandler.IUseCase }

func (logoStreamFake) StreamEventLogo(_ context.Context, _, _ uuid.UUID) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader("image-bytes")), "image/png", nil
}

func TestEventLogoPublicProxyServesImageContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	eventHandler.NewEventAPIHandler(logoStreamFake{}, allowEventCRUD{}).Init(router.Group("api"))
	eventID, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	request := httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/logo/"+fileID.String(), nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "image-bytes" {
		t.Fatalf("status/body = %d/%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "image/png" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("image response headers: %+v", response.Header())
	}
}
