package event_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

type bannerStreamFake struct {
	eventHandler.IUseCase
	streamErr error
	calls     int
	closed    int
}

type bannerReader struct {
	io.Reader
	close func()
}

func (r bannerReader) Close() error { r.close(); return nil }

func (u *bannerStreamFake) stream(fileID uuid.UUID) (io.ReadCloser, string, error) {
	u.calls++
	if u.streamErr != nil {
		return nil, "", u.streamErr
	}
	return bannerReader{Reader: strings.NewReader(fileID.String()), close: func() { u.closed++ }}, "image/png", nil
}

func (u *bannerStreamFake) StreamEventPreviewPicture(_ context.Context, _, fileID uuid.UUID) (io.ReadCloser, string, error) {
	return u.stream(fileID)
}

func (u *bannerStreamFake) StreamEventContentImage(_ context.Context, _, fileID uuid.UUID) (io.ReadCloser, string, error) {
	return u.stream(fileID)
}

func TestEventBannersRevalidatePrivatelyAfterAccessChecks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, route := range []string{"preview-picture", "content-images"} {
		t.Run(route, func(t *testing.T) {
			u := &bannerStreamFake{}
			router := gin.New()
			router.Use(response.WithErrorHandler)
			eventHandler.NewEventAPIHandler(u, allowEventCRUD{}).Init(router.Group("api"))
			eventID, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			get := func(file string, tags ...string) *httptest.ResponseRecorder {
				request := httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/"+route+"/"+file, nil)
				for _, tag := range tags {
					request.Header.Add("If-None-Match", tag)
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, request)
				return w
			}
			first := get(fileID.String())
			require.Equal(t, http.StatusOK, first.Code)
			require.Equal(t, fileID.String(), first.Body.String())
			tag := first.Header().Get("ETag")
			require.NotEmpty(t, tag)
			require.NotContains(t, tag, "W/", "file UUID identifies immutable bytes")
			require.Equal(t, "private, no-cache, must-revalidate, no-transform", first.Header().Get("Cache-Control"))
			require.Equal(t, "nosniff", first.Header().Get("X-Content-Type-Options"))
			for _, tags := range [][]string{{tag}, {"W/" + tag}, {`"other", W/` + tag}, {"*"}, {`"other"`, tag}} {
				before := u.calls
				same := get(fileID.String(), tags...)
				require.Equal(t, http.StatusNotModified, same.Code)
				require.Empty(t, same.Body.String())
				require.Equal(t, tag, same.Header().Get("ETag"))
				require.Equal(t, before+1, u.calls, "conditional requests still run admission and ownership checks")
				require.Equal(t, u.calls, u.closed, "304 must close the opened stream")
			}
			for _, nonmatch := range []string{`"other"`, "W/*"} {
				fresh := get(fileID.String(), nonmatch)
				require.Equal(t, http.StatusOK, fresh.Code)
				require.Equal(t, fileID.String(), fresh.Body.String())
			}
			changedID := uuid.Must(uuid.NewV7())
			changed := get(changedID.String(), tag)
			require.Equal(t, http.StatusOK, changed.Code)
			require.Equal(t, changedID.String(), changed.Body.String())
			require.NotEqual(t, tag, changed.Header().Get("ETag"))
			for _, denied := range []error{eventModel.ErrEventNotFound.Err(), mediaModel.ErrFileNotFound.Err()} {
				u.streamErr = denied
				for _, condition := range []string{tag, "*"} {
					failure := get(fileID.String(), condition)
					require.Equal(t, http.StatusNotFound, failure.Code)
					require.Empty(t, failure.Header().Get("ETag"))
					require.Equal(t, "private, no-store", failure.Header().Get("Cache-Control"))
				}
			}
			u.streamErr = errors.New("storage unavailable")
			unavailable := get(fileID.String(), "*")
			require.Equal(t, http.StatusInternalServerError, unavailable.Code)
			require.Empty(t, unavailable.Header().Get("ETag"))
			require.Equal(t, "private, no-store", unavailable.Header().Get("Cache-Control"))
			malformed := get("invalid", "*")
			require.Equal(t, http.StatusNotFound, malformed.Code)
			require.Equal(t, "private, no-store", malformed.Header().Get("Cache-Control"))
		})
	}
}
