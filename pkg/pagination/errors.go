package pagination

import "errors"

var (
	ErrInvalidSort     = errors.New("pagination: sort must be \"asc\" or \"desc\"")
	ErrInvalidPageSize = errors.New("pagination: pageSize must be between 1 and MaxPageSize")
	ErrInvalidPage     = errors.New("pagination: page must be 1 or greater")
)
