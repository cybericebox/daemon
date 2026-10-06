package http_utils

import "github.com/gin-gonic/gin"

// Validatable is implemented by request structs whose fields have been
// populated via query-string binding and need normalization/validation
// before use. See pagination.CursorParams.Validate / pagination.OffsetParams.Validate
// for the expected pattern (pointer receiver, mutate-then-check).
type Validatable interface {
	Validate() error
}

// BindQuery binds ctx's query-string parameters into v (a pointer to a
// struct with `form:` tags, e.g. *ListUsersRequest) and then runs
// v.Validate() so defaults are applied and bounds are checked. Query
// parameters are re-bindable (unlike the request body), so BindQuery may be
// called several times per request with different target structs.
func BindQuery(ctx *gin.Context, v Validatable) error {
	if err := ctx.ShouldBindQuery(v); err != nil {
		return err
	}
	return v.Validate()
}
