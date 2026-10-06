package pagination

import "github.com/gofrs/uuid"

// CursorParams is the request-side shape for cursor (keyset) pagination.
// Embed it in an endpoint-specific request struct to pick up its form tags
// and Validate. If the embedding struct defines its own Validate() method,
// that method shadows this one; the embedding struct must explicitly call
// CursorParams.Validate() to run the normalization and bounds check.
type CursorParams struct {
	// parser=encoding.TextUnmarshaler is required: uuid.UUID is a [16]byte
	// array under the hood, and gin's query binder otherwise tries to bind
	// it as 16 separate array elements (one per query value) and fails.
	Cursor   uuid.UUID `form:"cursor,parser=encoding.TextUnmarshaler"`
	Sort     SortOrder `form:"sort"`
	PageSize int       `form:"pageSize"`
}

// Validate normalizes zero-value defaults (Sort -> SortDesc, PageSize ->
// DefaultPageSize) and rejects out-of-range values. It mutates p in place,
// so it must be called on a pointer.
func (p *CursorParams) Validate() error {
	if p.Sort == "" {
		p.Sort = SortDesc
	}
	if p.Sort != SortAsc && p.Sort != SortDesc {
		return ErrInvalidSort
	}
	if p.PageSize == 0 {
		p.PageSize = DefaultPageSize
	}
	if p.PageSize < 1 || p.PageSize > MaxPageSize {
		return ErrInvalidPageSize
	}
	return nil
}

// CursorPage is the response-side shape for a cursor-paginated list.
// NextCursor/PrevCursor use omitempty so a nil pointer omits the JSON key
// entirely (no further page in that direction), while a non-nil pointer —
// even one pointing at uuid.Nil — means a page exists and should be
// requested with that cursor value.
type CursorPage[T any] struct {
	Total      int64      `json:"Total"`
	Items      []T        `json:"Items"`
	NextCursor *uuid.UUID `json:"NextCursor,omitempty"`
	PrevCursor *uuid.UUID `json:"PrevCursor,omitempty"`
}

// NewCursorPage assembles a response page from a keyset query's raw result:
// the mapped items, whether a further page exists (hasMore), the value cursor
// the caller computed for the last row, and the total row count. It bridges
// the internal "value cursor + bool hasMore" convention every list use case
// produces to the response contract's pointer NextCursor — nil (key omitted)
// when there is no further page, otherwise the forward cursor. A nil items
// slice is normalized to an empty slice so the JSON is always "Items": [].
func NewCursorPage[T any](items []T, hasMore bool, next uuid.UUID, total int64) CursorPage[T] {
	if items == nil {
		items = []T{}
	}
	page := CursorPage[T]{Total: total, Items: items}
	if hasMore {
		n := next
		page.NextCursor = &n
	}
	return page
}
