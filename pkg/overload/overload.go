// Package overload holds the sentinels that tell the HTTP layer a request was refused because the server is
// saturated, however many layers wrapped the cause on the way up.
package overload

import "errors"

// ErrDBBusy: no database connection became free within the acquire timeout. The HTTP error handler answers
// 503 with Retry-After for any error chain that contains it.
var ErrDBBusy = errors.New("database connection pool busy")
