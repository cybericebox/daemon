package platformAnalytics

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

// compareEnvelope is the answer of a report asked with compare=previous: the report of the period, the same report of
// the period just before it (same length) and that period. Previous is null when there is no such period (all time).
type compareEnvelope[T any] struct {
	Current        T                `json:"Current"`
	Previous       *T               `json:"Previous"`
	PreviousPeriod *comparedPeriods `json:"PreviousPeriod"`
}

type comparedPeriods struct {
	From time.Time `json:"From"`
	To   time.Time `json:"To"`
}

// previousWindow is the period of the same length that ends where the given one starts; false for an open start
// (all time) or an empty window.
func previousWindow(from, to *time.Time, now time.Time) (time.Time, time.Time, bool) {
	if from == nil {
		return time.Time{}, time.Time{}, false
	}
	end := now
	if to != nil {
		end = *to
	}
	length := end.Sub(*from)
	if length <= 0 {
		return time.Time{}, time.Time{}, false
	}
	return from.Add(-length), *from, true
}

// answer writes a report. With compare=previous (or compare=true) it also loads the report of the previous period and wraps both;
// without it the answer is the report itself, as before.
func answer[T any](
	ctx *gin.Context,
	from, to *time.Time,
	load func(from, to *time.Time) (T, error),
) {
	current, err := load(from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	if mode := ctx.Query("compare"); mode != "previous" && mode != "true" {
		response.AbortWithData(ctx, current)
		return
	}
	envelope := compareEnvelope[T]{Current: current}
	if pf, pt, ok := previousWindow(from, to, time.Now()); ok {
		previous, loadErr := load(&pf, &pt)
		if loadErr != nil {
			response.AbortWithError(ctx, loadErr)
			return
		}
		envelope.Previous, envelope.PreviousPeriod = &previous, &comparedPeriods{From: pf, To: pt}
	}
	response.AbortWithData(ctx, envelope)
}
