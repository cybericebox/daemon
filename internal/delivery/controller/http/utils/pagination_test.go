package http_utils_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	utils "github.com/cybericebox/daemon/internal/delivery/controller/http/utils"
	"github.com/cybericebox/daemon/pkg/pagination"
)

func TestGetCursorPaginationParams_Defaults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var got pagination.CursorParams
	var gotErr error
	r.GET("/list", func(ctx *gin.Context) {
		got, gotErr = utils.GetCursorPaginationParams(ctx)
	})

	req := httptest.NewRequest(http.MethodGet, "/list", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if gotErr != nil {
		t.Fatalf("GetCursorPaginationParams() error = %v, want nil", gotErr)
	}
	if got.PageSize != pagination.DefaultPageSize {
		t.Errorf("PageSize = %d, want default %d", got.PageSize, pagination.DefaultPageSize)
	}
	if got.Sort != pagination.SortDesc {
		t.Errorf("Sort = %q, want default %q", got.Sort, pagination.SortDesc)
	}
}

func TestGetCursorPaginationParams_Values(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var got pagination.CursorParams
	var gotErr error
	r.GET("/list", func(ctx *gin.Context) {
		got, gotErr = utils.GetCursorPaginationParams(ctx)
	})

	id := uuid.Must(uuid.NewV7())
	req := httptest.NewRequest(http.MethodGet, "/list?cursor="+id.String()+"&pageSize=7&sort=asc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if gotErr != nil {
		t.Fatalf("GetCursorPaginationParams() error = %v, want nil", gotErr)
	}
	if got.Cursor != id {
		t.Errorf("Cursor = %v, want %v", got.Cursor, id)
	}
	if got.PageSize != 7 {
		t.Errorf("PageSize = %d, want 7", got.PageSize)
	}
	if got.Sort != pagination.SortAsc {
		t.Errorf("Sort = %q, want %q", got.Sort, pagination.SortAsc)
	}
}

func TestGetCursorPaginationParams_InvalidPageSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var gotErr error
	r.GET("/list", func(ctx *gin.Context) {
		_, gotErr = utils.GetCursorPaginationParams(ctx)
	})

	req := httptest.NewRequest(http.MethodGet, "/list?pageSize=-1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !errors.Is(gotErr, pagination.ErrInvalidPageSize) {
		t.Errorf("GetCursorPaginationParams() error = %v, want %v", gotErr, pagination.ErrInvalidPageSize)
	}
}
