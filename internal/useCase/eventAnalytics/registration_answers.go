package eventAnalytics

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

const (
	// maxNumberBins bounds a number histogram.
	maxNumberBins = 10
	// maxTopValues bounds the repeated text values of a question.
	maxTopValues = 10
	// maxValueRunes cuts a text value shown in the report.
	maxValueRunes = 80
	// maxTimelineDays switches a date timeline from days to months.
	maxTimelineDays = 120
)

// questionAgg collects the answers of one registration question.
type questionAgg struct {
	block    eventContentModel.Block
	asked    int64
	answered int64
	counts   map[string]int64
	numbers  []float64
	dates    []time.Time
	hours    map[string]int64
}

// buildQuestions distributes the latest registration answers per question.
// A question is identified by its key; the newest definition wins, and an
// answer from a version where the key had a different input type is ignored.
func buildQuestions(versions []eventAnalyticsRepo.FormVersion, answers []eventAnalyticsRepo.FormAnswer) []QuestionView {
	fieldsOf := make(map[uuid.UUID]map[string]eventContentModel.Block, len(versions))
	defs := map[string]eventContentModel.Block{}
	var order []string
	for _, v := range versions { // oldest first: a later version overrides
		fields := map[string]eventContentModel.Block{}
		for _, b := range v.Blocks {
			if b.Type != eventContentModel.BlockField || b.Key == "" {
				continue
			}
			fields[b.Key] = b
			if _, seen := defs[b.Key]; !seen {
				order = append(order, b.Key)
			}
			defs[b.Key] = b
		}
		fieldsOf[v.ID] = fields
	}
	// The newest version's questions first, in its order.
	if len(versions) > 0 {
		var latest []string
		inLatest := map[string]bool{}
		for _, b := range versions[len(versions)-1].Blocks {
			if b.Type == eventContentModel.BlockField && b.Key != "" && !inLatest[b.Key] {
				latest = append(latest, b.Key)
				inLatest[b.Key] = true
			}
		}
		for _, key := range order {
			if !inLatest[key] {
				latest = append(latest, key)
			}
		}
		order = latest
	}

	aggs := make(map[string]*questionAgg, len(defs))
	for key, block := range defs {
		aggs[key] = &questionAgg{block: block, counts: map[string]int64{}, hours: map[string]int64{}}
	}
	for _, a := range answers {
		for key, agg := range aggs {
			block, ok := fieldsOf[a.VersionID][key]
			if !ok || block.Input != agg.block.Input {
				continue
			}
			agg.asked++
			agg.add(a.Answers[key])
		}
	}

	out := make([]QuestionView, 0, len(order))
	for _, key := range order {
		out = append(out, aggs[key].view())
	}
	return out
}

func (q *questionAgg) add(value any) {
	switch q.block.Input {
	case "select":
		if s, ok := value.(string); ok && s != "" {
			q.answered++
			q.counts[s]++
		}
	case "multi_select":
		if list, ok := value.([]any); ok && len(list) > 0 {
			q.answered++
			for _, item := range list {
				if s, ok := item.(string); ok {
					q.counts[s]++
				}
			}
		}
	case "checkbox":
		q.answered++
		if b, _ := value.(bool); b {
			q.counts["yes"]++
		} else {
			q.counts["no"]++
		}
	case "number":
		if n, ok := value.(float64); ok && !math.IsNaN(n) && !math.IsInf(n, 0) {
			q.answered++
			q.numbers = append(q.numbers, n)
		}
	case "date":
		s, ok := value.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return
		}
		s = strings.TrimSpace(s)
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			q.answered++
			q.dates = append(q.dates, t.UTC())
		} else if t, err := time.Parse("2006-01-02", s); err == nil {
			q.answered++
			q.dates = append(q.dates, t)
		} else if t, err := time.Parse("15:04", s); err == nil {
			q.answered++
			q.hours[fmt.Sprintf("%02d:00", t.Hour())]++
		}
	case "file":
		if s := fmt.Sprint(value); value != nil && s != "" {
			q.answered++
		}
	default: // text, long_text
		if s, ok := value.(string); ok {
			s = strings.ToLower(strings.TrimSpace(s))
			if s != "" {
				q.answered++
				q.counts[cut(s)]++
			}
		}
	}
}

func (q *questionAgg) view() QuestionView {
	v := QuestionView{Key: q.block.Key, Label: q.block.Label, Input: q.block.Input, Asked: q.asked, Answered: q.answered, Buckets: []BucketView{}}
	switch q.block.Input {
	case "select", "multi_select":
		seen := map[string]bool{}
		for _, option := range q.block.Options {
			v.Buckets = append(v.Buckets, BucketView{Label: option, Count: q.counts[option]})
			seen[option] = true
		}
		var extra []string
		for label := range q.counts {
			if !seen[label] {
				extra = append(extra, label)
			}
		}
		sort.Strings(extra) // options removed since; kept so the totals add up
		for _, label := range extra {
			v.Buckets = append(v.Buckets, BucketView{Label: label, Count: q.counts[label]})
		}
	case "checkbox":
		v.Buckets = []BucketView{{Label: "yes", Count: q.counts["yes"]}, {Label: "no", Count: q.counts["no"]}}
	case "file":
		v.Buckets = []BucketView{{Label: "has", Count: q.answered}, {Label: "none", Count: q.asked - q.answered}}
	case "number":
		q.numberView(&v)
	case "date":
		q.dateView(&v)
	default:
		q.textView(&v)
	}
	return v
}

func (q *questionAgg) textView(v *QuestionView) {
	v.Distinct = int64(len(q.counts))
	for label, n := range q.counts {
		if n >= 2 { // single values would only expose one person's free text
			v.Buckets = append(v.Buckets, BucketView{Label: label, Count: n})
		}
	}
	sort.Slice(v.Buckets, func(i, j int) bool {
		if v.Buckets[i].Count != v.Buckets[j].Count {
			return v.Buckets[i].Count > v.Buckets[j].Count
		}
		return v.Buckets[i].Label < v.Buckets[j].Label
	})
	if len(v.Buckets) > maxTopValues {
		v.Buckets = v.Buckets[:maxTopValues]
	}
}

func (q *questionAgg) numberView(v *QuestionView) {
	if len(q.numbers) == 0 {
		return
	}
	lo, hi, sum, whole := q.numbers[0], q.numbers[0], 0.0, true
	for _, n := range q.numbers {
		lo, hi = math.Min(lo, n), math.Max(hi, n)
		sum += n
		whole = whole && n == math.Trunc(n)
	}
	avg := sum / float64(len(q.numbers))
	v.Min, v.Max, v.Avg = &lo, &hi, &avg
	if whole && hi-lo < maxNumberBins { // few whole values: a bar per value
		counts := map[int64]int64{}
		for _, n := range q.numbers {
			counts[int64(n)]++
		}
		for x := int64(lo); x <= int64(hi); x++ {
			v.Buckets = append(v.Buckets, BucketView{Label: strconv.FormatInt(x, 10), Count: counts[x]})
		}
		return
	}
	if lo == hi {
		v.Buckets = []BucketView{{Label: formatNumber(lo), Count: int64(len(q.numbers))}}
		return
	}
	width := (hi - lo) / maxNumberBins
	counts := make([]int64, maxNumberBins)
	for _, n := range q.numbers {
		i := int((n - lo) / width)
		if i >= maxNumberBins {
			i = maxNumberBins - 1
		}
		counts[i]++
	}
	for i, c := range counts {
		from, to := lo+float64(i)*width, lo+float64(i+1)*width
		v.Buckets = append(v.Buckets, BucketView{Label: formatNumber(from) + "–" + formatNumber(to), Count: c})
	}
}

func (q *questionAgg) dateView(v *QuestionView) {
	if len(q.hours) > 0 { // a time-of-day question: hours
		for label, n := range q.hours {
			v.Buckets = append(v.Buckets, BucketView{Label: label, Count: n})
		}
		sort.Slice(v.Buckets, func(i, j int) bool { return v.Buckets[i].Label < v.Buckets[j].Label })
		return
	}
	if len(q.dates) == 0 {
		return
	}
	lo, hi := q.dates[0], q.dates[0]
	for _, t := range q.dates {
		if t.Before(lo) {
			lo = t
		}
		if t.After(hi) {
			hi = t
		}
	}
	layout := "2006-01-02"
	if hi.Sub(lo) > maxTimelineDays*24*time.Hour {
		layout = "2006-01"
	}
	counts := map[string]int64{}
	for _, t := range q.dates {
		counts[t.Format(layout)]++
	}
	for label, n := range counts {
		v.Buckets = append(v.Buckets, BucketView{Label: label, Count: n})
	}
	sort.Slice(v.Buckets, func(i, j int) bool { return v.Buckets[i].Label < v.Buckets[j].Label })
}

func cut(s string) string {
	if r := []rune(s); len(r) > maxValueRunes {
		return string(r[:maxValueRunes]) + "…"
	}
	return s
}

func formatNumber(n float64) string {
	return strconv.FormatFloat(math.Round(n*100)/100, 'f', -1, 64)
}
