// Package tablePrefsModel is how a person likes a data table: the same on every device.
package tablePrefsModel

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

const (
	// MaxBytes caps the stored JSON of one table.
	MaxBytes = 8 * 1024
	// MaxTablesPerUser caps how many tables one person keeps a view for.
	MaxTablesPerUser = 100
	maxKeyLength     = 64
	maxColumns       = 200
	maxWidth         = 5000
	maxPageSize      = 500
)

var keyPattern = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

// Prefs is the saved view of one table. Column keys are the frontend's own ids.
type Prefs struct {
	ColumnOrder  []string       `json:"ColumnOrder"`
	ColumnWidths map[string]int `json:"ColumnWidths"`
	Hidden       []string       `json:"Hidden"`
	Sort         *Sort          `json:"Sort"`
	PageSize     int            `json:"PageSize"`
}

// Sort is the column and direction ("asc" or "desc") a table is sorted by.
type Sort struct {
	Key string `json:"Key"`
	Dir string `json:"Dir"`
}

// ValidKey reports whether key may name a table.
func ValidKey(key string) bool { return keyPattern.MatchString(key) }

// Normalize validates the prefs and returns them with empty lists instead of nulls, plus their stored JSON.
func Normalize(p Prefs) (Prefs, []byte, bool) {
	if p.ColumnOrder == nil {
		p.ColumnOrder = []string{}
	}
	if p.Hidden == nil {
		p.Hidden = []string{}
	}
	if p.ColumnWidths == nil {
		p.ColumnWidths = map[string]int{}
	}
	if len(p.ColumnOrder) > maxColumns || len(p.Hidden) > maxColumns || len(p.ColumnWidths) > maxColumns {
		return Prefs{}, nil, false
	}
	for _, list := range [][]string{p.ColumnOrder, p.Hidden} {
		for _, k := range list {
			if !validColumn(k) {
				return Prefs{}, nil, false
			}
		}
	}
	for k, w := range p.ColumnWidths {
		if !validColumn(k) || w < 1 || w > maxWidth {
			return Prefs{}, nil, false
		}
	}
	if p.Sort != nil && (!validColumn(p.Sort.Key) || (p.Sort.Dir != "asc" && p.Sort.Dir != "desc")) {
		return Prefs{}, nil, false
	}
	if p.PageSize < 0 || p.PageSize > maxPageSize {
		return Prefs{}, nil, false
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > MaxBytes {
		return Prefs{}, nil, false
	}
	return p, raw, true
}

func validColumn(k string) bool {
	return k != "" && len(k) <= maxKeyLength && !strings.ContainsAny(k, "\x00\n\r")
}

// Error-code convention: see internal/model/auth/errors.go. Enforced by `make lint-errors`.
//
// TablePrefsObjectCode — next free detail code: 3.
//
//nolint:gochecknoglobals,mnd // the error registry: one var per code, parsed by tools/errorcatalog
var (
	// ErrTablePrefsInvalid is multi-site: a bad table key or a view that is not valid or too large.
	ErrTablePrefsInvalid = err.ErrInvalidData.WithObjectCode(model.TablePrefsObjectCode).
				WithMessage("Invalid table view").WithDetailCode(1)
	ErrTablePrefsLimit = err.ErrInvalidData.WithObjectCode(model.TablePrefsObjectCode).
				WithMessage("Too many saved table views").WithDetailCode(2)
)
