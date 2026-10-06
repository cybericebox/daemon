package pagination

// OffsetParams is the request-side shape for classic offset pagination —
// used where a list needs arbitrary column sorting and/or a total count
// with page numbers, which cursor pagination cannot express. If the embedding
// struct defines its own Validate() method, that method shadows this one; the
// embedding struct must explicitly call OffsetParams.Validate() to run the
// normalization and bounds check.
type OffsetParams struct {
	Page     int `form:"page"`
	PageSize int `form:"pageSize"`
}

// Validate normalizes zero-value defaults (Page -> 1, PageSize ->
// DefaultPageSize) and rejects out-of-range values. It mutates p in place,
// so it must be called on a pointer.
func (p *OffsetParams) Validate() error {
	if p.Page == 0 {
		p.Page = 1
	}
	if p.Page < 1 {
		return ErrInvalidPage
	}
	if p.PageSize == 0 {
		p.PageSize = DefaultPageSize
	}
	if p.PageSize < 1 || p.PageSize > MaxPageSize {
		return ErrInvalidPageSize
	}
	return nil
}

// Offset returns the zero-based row offset for the current Page/PageSize.
func (p *OffsetParams) Offset() int {
	return (p.Page - 1) * p.PageSize
}

// OffsetPage is the response-side shape for an offset-paginated list.
type OffsetPage[T any] struct {
	Items    []T   `json:"Items"`
	Total    int64 `json:"Total"`
	Page     int   `json:"Page"`
	PageSize int   `json:"PageSize"`
}

// NewOffsetPage assembles an offset response page from mapped items, the total
// row count, and the current page/pageSize. A nil items slice is normalized to
// an empty slice so the JSON is always "Items": [].
func NewOffsetPage[T any](items []T, total int64, page, pageSize int) OffsetPage[T] {
	if items == nil {
		items = []T{}
	}
	return OffsetPage[T]{Items: items, Total: total, Page: page, PageSize: pageSize}
}
