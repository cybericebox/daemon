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

type testParams struct {
	Search   string `form:"search"`
	PageSize int    `form:"pageSize"`
}

var errInvalidPageSize = errors.New("invalid pageSize")

func (p *testParams) Validate() error {
	if p.PageSize == 0 {
		p.PageSize = 20
	}
	if p.PageSize < 0 {
		return errInvalidPageSize
	}
	return nil
}

func TestBindQuery_BindsAndValidates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var got testParams
	var bindErr error
	r.GET("/list", func(ctx *gin.Context) {
		var p testParams
		bindErr = utils.BindQuery(ctx, &p)
		got = p
	})

	req := httptest.NewRequest(http.MethodGet, "/list?search=foo", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if bindErr != nil {
		t.Fatalf("BindQuery() error = %v, want nil", bindErr)
	}
	if got.Search != "foo" {
		t.Errorf("Search = %q, want %q", got.Search, "foo")
	}
	if got.PageSize != 20 {
		t.Errorf("PageSize = %d, want default 20 (from Validate)", got.PageSize)
	}
}

func TestBindQuery_ValidateError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var bindErr error
	r.GET("/list", func(ctx *gin.Context) {
		var p testParams
		bindErr = utils.BindQuery(ctx, &p)
	})

	req := httptest.NewRequest(http.MethodGet, "/list?pageSize=-1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !errors.Is(bindErr, errInvalidPageSize) {
		t.Errorf("BindQuery() error = %v, want %v", bindErr, errInvalidPageSize)
	}
}

func TestBindQuery_CursorParamsIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)

	type listRequest struct {
		pagination.CursorParams
		Search string `form:"search"`
	}

	r := gin.New()
	var got listRequest
	var bindErr error
	r.GET("/list", func(ctx *gin.Context) {
		var p listRequest
		bindErr = utils.BindQuery(ctx, &p)
		got = p
	})

	id := uuid.Must(uuid.NewV7())
	req := httptest.NewRequest(
		http.MethodGet,
		"/list?cursor="+id.String()+"&sort=asc&pageSize=5&search=bar",
		nil,
	)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if bindErr != nil {
		t.Fatalf("BindQuery() error = %v, want nil", bindErr)
	}
	if got.Cursor != id {
		t.Errorf("Cursor = %v, want %v", got.Cursor, id)
	}
	if got.Sort != pagination.SortAsc {
		t.Errorf("Sort = %q, want %q", got.Sort, pagination.SortAsc)
	}
	if got.PageSize != 5 {
		t.Errorf("PageSize = %d, want 5", got.PageSize)
	}
	if got.Search != "bar" {
		t.Errorf("Search = %q, want %q", got.Search, "bar")
	}
}
