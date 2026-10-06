package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

type archivedGuard struct{}

func (archivedGuard) RequireEventWritable(context.Context, uuid.UUID) error {
	return eventModel.ErrEventArchived.Err()
}

func TestCheckEventWritable_ReadsPassAndWritesAreRefused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := uuid.Must(uuid.NewV7())
	for method, wantErr := range map[string]bool{
		http.MethodGet: false, http.MethodHead: false, http.MethodOptions: false,
		http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(method, "/x", nil)
		err := CheckEventWritable(c, archivedGuard{}, id)
		assert.Equal(t, wantErr, err != nil, method)
	}
}

func TestCheckEventWritable_UseCaseWithoutGuardIsUnguarded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	assert.NoError(t, CheckEventWritable(c, struct{}{}, uuid.Must(uuid.NewV7())))
}

func TestCheckEventWritable_RevocationsStayAllowedOnAnArchivedEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := uuid.Must(uuid.NewV7())
	cases := []struct {
		method, route string
		allowed       bool
	}{
		{http.MethodDelete, "/api/events/:id/manage/content/live/screen-link", true},
		{http.MethodPost, "/api/events/:id/manage/content/live/screen-link/regenerate", true},
		{http.MethodDelete, "/api/events/:id/manage/participants/:userID/invitation", true},
		{http.MethodPost, "/api/events/:id/manage/participants/:userID/reject", true},
		{http.MethodDelete, "/api/events/:id/manage/teams/:teamID/members/:userID", true},
		{http.MethodDelete, "/api/events/:id/manage/teams/:teamID", true},
		{http.MethodPost, "/api/events/:id/manage/content/live/screen-link", false},
		{http.MethodPut, "/api/events/:id/manage/name", false},
		{http.MethodDelete, "/api/events/:id/manage/logo", false},
		{http.MethodPost, "/api/events/:id/manage/participants/:userID/approve", false},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)
		var got error
		r.Handle(tc.method, tc.route, func(c *gin.Context) { got = CheckEventWritable(c, archivedGuard{}, id) })
		r.ServeHTTP(w, httptest.NewRequest(tc.method, strings.NewReplacer(":id", "1", ":userID", "2", ":teamID", "3").Replace(tc.route), nil))
		assert.Equal(t, tc.allowed, got == nil, tc.method+" "+tc.route)
	}
}
