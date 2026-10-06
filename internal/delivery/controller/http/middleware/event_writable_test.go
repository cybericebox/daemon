package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
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
