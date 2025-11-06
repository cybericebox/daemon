package tools

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
)

func GetPaginationParams(ctx *gin.Context) (page, pageSize int, err error) {
	pageQuery := ctx.Query("page")
	pageSizeQuery := ctx.Query("page_size")

	page = 0
	pageSize = config.DefaultPageSize

	if pageQuery != "" {
		page, err = strconv.Atoi(pageQuery)
		if err != nil {
			return 0, 0, model.ErrPlatform.WithError(err).WithMessage("invalid page parameter").Err()
		}
	}

	if pageSizeQuery != "" {
		pageSize, err = strconv.Atoi(pageSizeQuery)
		if err != nil {
			return 0, 0, model.ErrPlatform.WithError(err).WithMessage("invalid page_size parameter").Err()
		}
	}

	return page, pageSize, nil
}
