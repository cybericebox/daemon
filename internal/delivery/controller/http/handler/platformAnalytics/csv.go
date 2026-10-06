package platformAnalytics

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/download"
	"github.com/cybericebox/daemon/pkg/tools"
)

// startCSV opens a CSV download (UTF-8 with BOM, so spreadsheets read the
// Cyrillic headers). Every section export is `<section>/export.csv?table=`.
func startCSV(ctx *gin.Context, name string) *csv.Writer {
	download.ExtendDeadline(ctx)
	ctx.Header("Content-Type", "text/csv; charset=utf-8")
	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="platform-%s-%s.csv"`, name, time.Now().UTC().Format("20060102-1504")))
	ctx.Header("Cache-Control", "private, no-store")
	ctx.Status(http.StatusOK)
	_, _ = ctx.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	return csv.NewWriter(ctx.Writer)
}

// csvText neutralizes spreadsheet formulas in user-controlled text (event
// and task names, error messages).
func csvText(value string) string { return tools.CSVText(value) }

func csvTime(value time.Time) string { return value.UTC().Format(time.RFC3339) }

func csvTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return csvTime(*value)
}

func csvInt(value int64) string { return strconv.FormatInt(value, 10) }

// csvFloat prints a ratio or average with a fixed precision.
func csvFloat(value float64) string { return strconv.FormatFloat(value, 'f', 2, 64) }

func csvDay(value time.Time) string { return value.UTC().Format("2006-01-02") }
