package http_utils

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/pkg/pagination"
)

// GetCursorPaginationParams binds and validates the cursor-pagination query
// parameters (cursor, sort, pageSize) from ctx.
func GetCursorPaginationParams(ctx *gin.Context) (pagination.CursorParams, error) {
	var p pagination.CursorParams
	if err := BindQuery(ctx, &p); err != nil {
		return pagination.CursorParams{}, err
	}
	return p, nil
}

// GetOffsetPaginationParams binds and validates the offset-pagination query
// parameters (page, pageSize) from ctx.
func GetOffsetPaginationParams(ctx *gin.Context) (pagination.OffsetParams, error) {
	var p pagination.OffsetParams
	if err := BindQuery(ctx, &p); err != nil {
		return pagination.OffsetParams{}, err
	}
	return p, nil
}
