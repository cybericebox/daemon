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
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

type mediaFake struct {
	eventHandler.IUseCase
	public   bool
	checkErr error
	openErr  error
	checks   int
	opens    int
	closed   int
}

type mediaReader struct {
	io.Reader
	close func()
}

func (r mediaReader) Close() error { r.close(); return nil }

func (u *mediaFake) CheckEventMedia(context.Context, eventUseCase.EventMediaKind, uuid.UUID, uuid.UUID) (bool, error) {
	u.checks++
	return u.public, u.checkErr
}

func (u *mediaFake) OpenEventMedia(_ context.Context, fileID uuid.UUID) (io.ReadCloser, string, error) {
	u.opens++
	if u.openErr != nil {
		return nil, "", u.openErr
	}
	return mediaReader{Reader: strings.NewReader(fileID.String()), close: func() { u.closed++ }}, "image/png", nil
}

var mediaRoutes = []string{"logo", "favicon", "preview-picture", "content-images"}

func mediaRouter(u *mediaFake) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler)
	eventHandler.NewEventAPIHandler(u, allowEventCRUD{}).Init(router.Group("api"))
	return router
}

func mediaGet(router *gin.Engine, route, eventID, file string, tags ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/events/"+eventID+"/"+route+"/"+file, nil)
	for _, tag := range tags {
		request.Header.Add("If-None-Match", tag)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	return w
}

func TestEventMediaOfAPublishedEventIsImmutable(t *testing.T) {
	for _, route := range mediaRoutes {
		t.Run(route, func(t *testing.T) {
			u := &mediaFake{public: true}
			router := mediaRouter(u)
			eventID, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			first := mediaGet(router, route, eventID.String(), fileID.String())
			require.Equal(t, http.StatusOK, first.Code)
			require.Equal(t, fileID.String(), first.Body.String())
			require.Equal(t, "public, max-age=31536000, immutable", first.Header().Get("Cache-Control"))
			require.Equal(t, "nosniff", first.Header().Get("X-Content-Type-Options"))
			require.Equal(t, "image/png", first.Header().Get("Content-Type"))
			require.Equal(t, 1, u.closed)
			tag := first.Header().Get("ETag")
			require.NotEmpty(t, tag)
			require.NotContains(t, tag, "W/", "the file id identifies immutable bytes")
		})
	}
}

func TestEventMediaOfAHiddenEventRevalidatesAndNeverOpensStorageOn304(t *testing.T) {
	for _, route := range mediaRoutes {
		t.Run(route, func(t *testing.T) {
			u := &mediaFake{public: false}
			router := mediaRouter(u)
			eventID, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			first := mediaGet(router, route, eventID.String(), fileID.String())
			require.Equal(t, http.StatusOK, first.Code)
			require.Equal(t, "private, no-cache, must-revalidate, no-transform", first.Header().Get("Cache-Control"))
			tag := first.Header().Get("ETag")
			require.NotEmpty(t, tag)
			require.Equal(t, 1, u.opens)
			for _, tags := range [][]string{{tag}, {"W/" + tag}, {`"other", W/` + tag}, {"*"}, {`"other"`, tag}} {
				checks, opens := u.checks, u.opens
				same := mediaGet(router, route, eventID.String(), fileID.String(), tags...)
				require.Equal(t, http.StatusNotModified, same.Code)
				require.Empty(t, same.Body.String())
				require.Equal(t, tag, same.Header().Get("ETag"))
				require.Equal(t, checks+1, u.checks, "a conditional request still checks access in the database")
				require.Equal(t, opens, u.opens, "a 304 must not open storage")
			}
			for _, nonmatch := range []string{`"other"`, "W/*"} {
				fresh := mediaGet(router, route, eventID.String(), fileID.String(), nonmatch)
				require.Equal(t, http.StatusOK, fresh.Code)
				require.Equal(t, fileID.String(), fresh.Body.String())
			}
			changedID := uuid.Must(uuid.NewV7())
			changed := mediaGet(router, route, eventID.String(), changedID.String(), tag)
			require.Equal(t, http.StatusOK, changed.Code)
			require.Equal(t, changedID.String(), changed.Body.String())
			require.NotEqual(t, tag, changed.Header().Get("ETag"))
		})
	}
}

func TestEventMediaDeniedOrBrokenCarriesNoValidatorAndIsNeverCached(t *testing.T) {
	for _, route := range mediaRoutes {
		t.Run(route, func(t *testing.T) {
			u := &mediaFake{public: true}
			router := mediaRouter(u)
			eventID, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			for _, denied := range []error{eventModel.ErrEventNotFound.Err(), mediaModel.ErrFileNotFound.Err()} {
				u.checkErr = denied
				for _, condition := range []string{`"event-image-` + fileID.String() + `"`, "*"} {
					failure := mediaGet(router, route, eventID.String(), fileID.String(), condition)
					require.Equal(t, http.StatusNotFound, failure.Code)
					require.Empty(t, failure.Header().Get("ETag"))
					require.Equal(t, "private, no-store", failure.Header().Get("Cache-Control"))
				}
			}
			u.checkErr = nil
			u.openErr = errors.New("storage unavailable")
			unavailable := mediaGet(router, route, eventID.String(), fileID.String())
			require.Equal(t, http.StatusInternalServerError, unavailable.Code)
			require.Empty(t, unavailable.Header().Get("ETag"))
			require.Equal(t, "private, no-store", unavailable.Header().Get("Cache-Control"))
			malformed := mediaGet(router, route, eventID.String(), "invalid", "*")
			require.Equal(t, http.StatusNotFound, malformed.Code)
			require.Equal(t, "private, no-store", malformed.Header().Get("Cache-Control"))
		})
	}
}
