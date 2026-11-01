package tablePrefsModel_test

import (
	"strings"
	"testing"

	tablePrefsModel "github.com/cybericebox/daemon/internal/model/tablePrefs"
)

func TestValidKey(t *testing.T) {
	for _, k := range []string{"admin.users", "event-teams", "a", "t_1", strings.Repeat("a", 64)} {
		if !tablePrefsModel.ValidKey(k) {
			t.Errorf("%q must be valid", k)
		}
	}
	for _, k := range []string{"", "Admin", "a b", "a/b", "../x", strings.Repeat("a", 65), "a\x00"} {
		if tablePrefsModel.ValidKey(k) {
			t.Errorf("%q must be refused", k)
		}
	}
}

func TestNormalize(t *testing.T) {
	good := tablePrefsModel.Prefs{
		ColumnOrder: []string{
			"a",
			"b",
		},
		ColumnWidths: map[string]int{"a": 120},
		Hidden:       []string{"c"},
		Sort:         &tablePrefsModel.Sort{Key: "a", Dir: "asc"},
		PageSize:     50,
	}
	if _, raw, ok := tablePrefsModel.Normalize(good); !ok || len(raw) == 0 {
		t.Fatal("a normal view must pass")
	}
	empty, _, emptyOK := tablePrefsModel.Normalize(tablePrefsModel.Prefs{})
	if !emptyOK || empty.ColumnOrder == nil || empty.Hidden == nil || empty.ColumnWidths == nil {
		t.Fatalf("an empty view passes with empty lists, got %+v", empty)
	}

	bad := map[string]tablePrefsModel.Prefs{
		"sort direction":  {Sort: &tablePrefsModel.Sort{Key: "a", Dir: "up"}},
		"sort key":        {Sort: &tablePrefsModel.Sort{Key: "", Dir: "asc"}},
		"zero width":      {ColumnWidths: map[string]int{"a": 0}},
		"huge width":      {ColumnWidths: map[string]int{"a": 100000}},
		"negative page":   {PageSize: -1},
		"huge page":       {PageSize: 100000},
		"empty column":    {ColumnOrder: []string{""}},
		"long column":     {Hidden: []string{strings.Repeat("x", 65)}},
		"too many":        {ColumnOrder: make([]string, 201)},
		"over eight KB":   {ColumnOrder: manyColumns(180, 60)},
		"control in name": {Hidden: []string{"a\nb"}},
	}
	for name, p := range bad {
		if _, _, ok := tablePrefsModel.Normalize(p); ok {
			t.Errorf("%s must be refused", name)
		}
	}
}

func manyColumns(n, length int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = strings.Repeat("c", length)
	}
	return out
}
