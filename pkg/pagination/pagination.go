package pagination

// SortOrder controls which comparison operator a cursor query compiles to
// (id > cursor for SortAsc, id < cursor for SortDesc). It has no other
// effect — see CursorParams.
type SortOrder string

const (
	SortAsc  SortOrder = "asc"
	SortDesc SortOrder = "desc"
)

const (
	// DefaultPageSize is used when a request omits pageSize.
	DefaultPageSize = 20
	// MaxPageSize is the largest pageSize a request may ask for.
	MaxPageSize = 200
)
