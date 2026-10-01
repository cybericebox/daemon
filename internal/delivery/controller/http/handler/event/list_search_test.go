package event

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestListSearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name, raw, want string
		wantErr         bool
	}{
		{name: "empty", raw: "", want: ""},
		{name: "trimmed", raw: "  Іван ", want: "Іван"},
		{name: "at limit", raw: strings.Repeat("я", maxListSearchLength), want: strings.Repeat("я", maxListSearchLength)},
		{name: "too long", raw: strings.Repeat("я", maxListSearchLength+1), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("GET", "/?search="+url.QueryEscape(tc.raw), nil)
			got, err := listSearch(ctx)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("search = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTableSort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for raw, want := range map[string]struct {
		key     string
		desc    bool
		wantErr bool
	}{
		"":                               {},
		"sortBy=@name":                   {key: "@name"},
		"sortBy=%20city%20&sortDir=desc": {key: "city", desc: true},
		"sortBy=@name&sortDir=up":        {wantErr: true},
	} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest("GET", "/?"+raw, nil)
		got, err := tableSort(ctx)
		if (err != nil) != want.wantErr || got.Key != want.key || got.Desc != want.desc {
			t.Fatalf("%q: sort = %+v err=%v, want %+v", raw, got, err, want)
		}
	}
}
