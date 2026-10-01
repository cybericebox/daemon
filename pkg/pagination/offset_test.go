package pagination

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewOffsetPage_Fields(t *testing.T) {
	page := NewOffsetPage([]int{1, 2, 3}, 42, 2, 20)
	if page.Total != 42 || page.Page != 2 || page.PageSize != 20 || len(page.Items) != 3 {
		t.Fatalf("unexpected page: %+v", page)
	}
}

func TestNewOffsetPage_NilItemsBecomesEmpty(t *testing.T) {
	b, err := json.Marshal(NewOffsetPage[int](nil, 0, 1, 20))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(b), `"Items":[]`) {
		t.Fatalf(`expected "Items":[], got %s`, b)
	}
}

func TestOffsetParamsValidate_Defaults(t *testing.T) {
	p := OffsetParams{}
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	if p.Page != 1 {
		t.Errorf("Page = %d, want 1", p.Page)
	}
	if p.PageSize != DefaultPageSize {
		t.Errorf("PageSize = %d, want %d", p.PageSize, DefaultPageSize)
	}
}

func TestOffsetParamsValidate_InvalidPage(t *testing.T) {
	p := OffsetParams{Page: -1, PageSize: 10}
	if err := p.Validate(); err != ErrInvalidPage {
		t.Errorf("Validate() error = %v, want %v", err, ErrInvalidPage)
	}
}

func TestOffsetParamsValidate_InvalidPageSize(t *testing.T) {
	for _, size := range []int{-1, MaxPageSize + 1} {
		p := OffsetParams{Page: 1, PageSize: size}
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

func TestOffsetParamsOffset(t *testing.T) {
	cases := []struct {
		params OffsetParams
		want   int
	}{
		{OffsetParams{Page: 1, PageSize: 20}, 0},
		{OffsetParams{Page: 2, PageSize: 20}, 20},
		{OffsetParams{Page: 3, PageSize: 20}, 40},
		{OffsetParams{Page: 5, PageSize: 10}, 40},
	}
	for _, c := range cases {
		if got := c.params.Offset(); got != c.want {
			t.Errorf("%+v.Offset() = %d, want %d", c.params, got, c.want)
		}
	}
}
