package pagination

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
)

func TestCursorParamsValidate_Defaults(t *testing.T) {
	p := CursorParams{}
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	if p.Sort != SortDesc {
		t.Errorf("Sort = %q, want %q", p.Sort, SortDesc)
	}
	if p.PageSize != DefaultPageSize {
		t.Errorf("PageSize = %d, want %d", p.PageSize, DefaultPageSize)
	}
}

func TestCursorParamsValidate_ValidSort(t *testing.T) {
	for _, sort := range []SortOrder{SortAsc, SortDesc} {
		p := CursorParams{Sort: sort, PageSize: 10}
		if err := p.Validate(); err != nil {
			t.Errorf("Validate() with Sort=%q error = %v, want nil", sort, err)
		}
		if p.Sort != sort {
			t.Errorf("Sort = %q, want unchanged %q", p.Sort, sort)
		}
	}
}

func TestCursorParamsValidate_InvalidSort(t *testing.T) {
	p := CursorParams{Sort: "sideways", PageSize: 10}
	if err := p.Validate(); err != ErrInvalidSort {
		t.Errorf("Validate() error = %v, want %v", err, ErrInvalidSort)
	}
}

func TestCursorParamsValidate_InvalidPageSize(t *testing.T) {
	for _, size := range []int{-1, MaxPageSize + 1} {
		p := CursorParams{Sort: SortAsc, PageSize: size}
		if err := p.Validate(); err != ErrInvalidPageSize {
			t.Errorf(
				"Validate() with PageSize=%d error = %v, want %v",
				size,
				err,
				ErrInvalidPageSize,
			)
		}
	}
}

func TestNewCursorPage_HasMoreSetsNextCursor(t *testing.T) {
	next := uuid.Must(uuid.NewV7())
	page := NewCursorPage([]int{1, 2}, true, next, 5)
	if page.Total != 5 {
		t.Fatalf("Total = %d, want 5", page.Total)
	}
	if page.NextCursor == nil || *page.NextCursor != next {
		t.Fatalf("NextCursor = %v, want %v", page.NextCursor, next)
	}
	if page.PrevCursor != nil {
		t.Fatalf("PrevCursor = %v, want nil", page.PrevCursor)
	}
}

func TestNewCursorPage_NoMoreOmitsCursor(t *testing.T) {
	page := NewCursorPage([]int{1}, false, uuid.Must(uuid.NewV7()), 1)
	if page.NextCursor != nil {
		t.Fatalf("NextCursor = %v, want nil (no further page)", page.NextCursor)
	}
}

func TestNewCursorPage_NilItemsBecomesEmpty(t *testing.T) {
	page := NewCursorPage[int](nil, false, uuid.UUID{}, 0)
	b, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(b), `"Items":[]`) {
		t.Fatalf(`expected "Items":[], got %s`, b)
	}
}

func TestCursorPage_JSONOmitsAbsentCursors(t *testing.T) {
	page := CursorPage[int]{Items: []int{1, 2, 3}}
	b, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	s := string(b)
	if strings.Contains(s, "NextCursor") || strings.Contains(s, "PrevCursor") {
		t.Errorf("expected NextCursor/PrevCursor omitted, got %s", s)
	}
}

func TestCursorPage_JSONIncludesNonNilCursorEvenIfZeroUUID(t *testing.T) {
	zero := uuid.UUID{}
	page := CursorPage[int]{Items: []int{1}, PrevCursor: &zero}
	b, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"PrevCursor":"00000000-0000-0000-0000-000000000000"`) {
		t.Errorf("expected PrevCursor present as zero UUID, got %s", s)
	}
	if strings.Contains(s, "NextCursor") {
		t.Errorf("expected NextCursor still omitted, got %s", s)
	}
}
