package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// metrics collects every request the run makes: latency per endpoint, outcome counters, and one
// bucket per minute so a trend (a latency climb, an error burst) is visible in the report.
type metrics struct {
	start time.Time

	mu        sync.Mutex
	endpoints map[string]*endpointStats
	minutes   map[int]*minuteStats
	labs      []labTime
	notes     map[string]int
}

type endpointStats struct {
	durations []time.Duration
	status    map[int]int
	netErrors int
	// failed counts the answers that are errors for the run: 4xx/5xx the flow did not expect, and network errors.
	failed int
}

type minuteStats struct {
	requests  int
	errors    int
	throttled int
	server    int
	durations []time.Duration
}

// labTime is the wait of one team's lab from the start of the run to the first Ready answer.
type labTime struct {
	Team      int
	Challenge string
	Phase     string
	Wait      time.Duration
	Ready     bool
}

func newMetrics(start time.Time) *metrics {
	return &metrics{start: start, endpoints: map[string]*endpointStats{}, minutes: map[int]*minuteStats{}, notes: map[string]int{}}
}

// observe records one request. status 0 means the request did not complete (network error, timeout).
// benign marks a 4xx the flow expects (joining a team twice, an already solved task): counted in the status
// table but not as an error.
func (m *metrics) observe(label string, status int, took time.Duration, benign bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.endpoints[label]
	if e == nil {
		e = &endpointStats{status: map[int]int{}}
		m.endpoints[label] = e
	}
	e.durations = append(e.durations, took)
	minute := int(time.Since(m.start) / time.Minute)
	b := m.minutes[minute]
	if b == nil {
		b = &minuteStats{}
		m.minutes[minute] = b
	}
	b.requests++
	b.durations = append(b.durations, took)
	switch {
	case status == 0:
		e.netErrors++
		e.failed++
		b.errors++
	case status == 429:
		e.status[status]++
		e.failed++
		b.throttled++
	case status >= 500:
		e.status[status]++
		e.failed++
		b.errors++
		b.server++
	default:
		e.status[status]++
		if status >= 400 && !benign {
			e.failed++
			b.errors++
		}
	}
}

func (m *metrics) note(what string) {
	m.mu.Lock()
	m.notes[what]++
	m.mu.Unlock()
}

func (m *metrics) recordLab(l labTime) {
	m.mu.Lock()
	m.labs = append(m.labs, l)
	m.mu.Unlock()
}

// window returns the totals of the last d, for the live progress line and the abort rule.
func (m *metrics) window(d time.Duration) (requests, errors, throttled int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := int(time.Since(m.start) / time.Minute)
	span := max(int(d/time.Minute), 1)
	for minute := current - span + 1; minute <= current; minute++ {
		if b := m.minutes[minute]; b != nil {
			requests += b.requests
			errors += b.errors
			throttled += b.throttled
		}
	}
	return
}

// percentile of an ascending slice, nearest rank.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(p/100*float64(len(sorted))+0.999999) - 1
	return sorted[min(max(rank, 0), len(sorted)-1)]
}

func sortedCopy(in []time.Duration) []time.Duration {
	out := append([]time.Duration(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// EndpointReport is one row of the per-endpoint table.
type EndpointReport struct {
	Endpoint  string      `json:"endpoint"`
	Count     int         `json:"count"`
	P50ms     float64     `json:"p50Ms"`
	P95ms     float64     `json:"p95Ms"`
	P99ms     float64     `json:"p99Ms"`
	MaxMs     float64     `json:"maxMs"`
	Status    map[int]int `json:"status"`
	NetErrors int         `json:"netErrors"`
	ErrorRate float64     `json:"errorRate"`
}

// MinuteReport is one row of the per-minute table.
type MinuteReport struct {
	Minute    int     `json:"minute"`
	Requests  int     `json:"requests"`
	Errors    int     `json:"errors"`
	Throttled int     `json:"throttled429"`
	Server5xx int     `json:"server5xx"`
	P95ms     float64 `json:"p95Ms"`
}

// LabReport summarises the lab start times of the run.
type LabReport struct {
	Labs     int             `json:"labs"`
	Ready    int             `json:"ready"`
	NotReady int             `json:"notReady"`
	P50s     float64         `json:"p50Seconds"`
	P95s     float64         `json:"p95Seconds"`
	MaxS     float64         `json:"maxSeconds"`
	Items    []labItemReport `json:"items"`
}

type labItemReport struct {
	Team      int     `json:"team"`
	Challenge string  `json:"challenge"`
	Phase     string  `json:"phase"`
	Seconds   float64 `json:"seconds"`
	Ready     bool    `json:"ready"`
}

// Report is the whole output of a run.
type Report struct {
	Started   time.Time        `json:"started"`
	Duration  string           `json:"duration"`
	Requests  int              `json:"requests"`
	Errors    int              `json:"errors"`
	Endpoints []EndpointReport `json:"endpoints"`
	Minutes   []MinuteReport   `json:"minutes"`
	Labs      LabReport        `json:"labs"`
	Notes     map[string]int   `json:"notes"`
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func (m *metrics) report(now time.Time) Report {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := Report{Started: m.start, Duration: now.Sub(m.start).Round(time.Second).String(), Notes: map[string]int{}}
	for k, v := range m.notes {
		r.Notes[k] = v
	}
	for label, e := range m.endpoints {
		s := sortedCopy(e.durations)
		failed := e.failed
		row := EndpointReport{Endpoint: label, Count: len(s), P50ms: ms(percentile(s, 50)), P95ms: ms(percentile(s, 95)), P99ms: ms(percentile(s, 99)),
			Status: e.status, NetErrors: e.netErrors}
		if len(s) > 0 {
			row.MaxMs = ms(s[len(s)-1])
			row.ErrorRate = float64(failed) / float64(len(s))
		}
		r.Requests += row.Count
		r.Errors += failed
		r.Endpoints = append(r.Endpoints, row)
	}
	sort.Slice(r.Endpoints, func(i, j int) bool { return r.Endpoints[i].Endpoint < r.Endpoints[j].Endpoint })

	var minutes []int
	for k := range m.minutes {
		minutes = append(minutes, k)
	}
	sort.Ints(minutes)
	for _, k := range minutes {
		b := m.minutes[k]
		r.Minutes = append(r.Minutes, MinuteReport{Minute: k, Requests: b.requests, Errors: b.errors, Throttled: b.throttled, Server5xx: b.server, P95ms: ms(percentile(sortedCopy(b.durations), 95))})
	}

	var waits []time.Duration
	for _, l := range m.labs {
		r.Labs.Labs++
		if l.Ready {
			r.Labs.Ready++
			waits = append(waits, l.Wait)
		} else {
			r.Labs.NotReady++
		}
		r.Labs.Items = append(r.Labs.Items, labItemReport{Team: l.Team, Challenge: l.Challenge, Phase: l.Phase, Seconds: l.Wait.Seconds(), Ready: l.Ready})
	}
	sort.Slice(r.Labs.Items, func(i, j int) bool { return r.Labs.Items[i].Seconds > r.Labs.Items[j].Seconds })
	waits = sortedCopy(waits)
	r.Labs.P50s = percentile(waits, 50).Seconds()
	r.Labs.P95s = percentile(waits, 95).Seconds()
	if len(waits) > 0 {
		r.Labs.MaxS = waits[len(waits)-1].Seconds()
	}
	return r
}

// printText writes the human-readable report.
func (r Report) printText(w io.Writer) {
	_, _ = fmt.Fprintf(w, "\nRun started %s, lasted %s: %d requests, %d failed (%.2f%%)\n\n", r.Started.Format(time.RFC3339), r.Duration, r.Requests, r.Errors, pct(r.Errors, r.Requests))
	_, _ = fmt.Fprintf(w, "%-62s %7s %8s %8s %8s %8s %7s  %s\n", "endpoint", "count", "p50 ms", "p95 ms", "p99 ms", "max ms", "err %", "status")
	for _, e := range r.Endpoints {
		var codes []string
		keys := make([]int, 0, len(e.Status))
		for c := range e.Status {
			keys = append(keys, c)
		}
		sort.Ints(keys)
		for _, c := range keys {
			codes = append(codes, fmt.Sprintf("%d:%d", c, e.Status[c]))
		}
		if e.NetErrors > 0 {
			codes = append(codes, fmt.Sprintf("net:%d", e.NetErrors))
		}
		_, _ = fmt.Fprintf(w, "%-62s %7d %8.0f %8.0f %8.0f %8.0f %6.1f%%  %s\n", e.Endpoint, e.Count, e.P50ms, e.P95ms, e.P99ms, e.MaxMs, e.ErrorRate*100, strings.Join(codes, " "))
	}
	_, _ = fmt.Fprintf(w, "\nPer minute (since the start of the run)\n%-7s %9s %7s %7s %7s %9s\n", "minute", "requests", "errors", "429", "5xx", "p95 ms")
	for _, mm := range r.Minutes {
		_, _ = fmt.Fprintf(w, "%-7d %9d %7d %7d %7d %9.0f\n", mm.Minute, mm.Requests, mm.Errors, mm.Throttled, mm.Server5xx, mm.P95ms)
	}
	_, _ = fmt.Fprintf(w, "\nLabs: %d tracked, %d ready, %d not ready; start time p50 %.1fs, p95 %.1fs, max %.1fs\n", r.Labs.Labs, r.Labs.Ready, r.Labs.NotReady, r.Labs.P50s, r.Labs.P95s, r.Labs.MaxS)
	for i, l := range r.Labs.Items {
		if i >= 10 {
			break
		}
		state := "ready"
		if !l.Ready {
			state = "NOT READY (" + l.Phase + ")"
		}
		_, _ = fmt.Fprintf(w, "  slowest %2d: team %02d  %-40s %7.1fs  %s\n", i+1, l.Team, l.Challenge, l.Seconds, state)
	}
	if len(r.Notes) > 0 {
		keys := make([]string, 0, len(r.Notes))
		for k := range r.Notes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		_, _ = fmt.Fprintln(w, "\nOutcomes")
		for _, k := range keys {
			_, _ = fmt.Fprintf(w, "  %-40s %d\n", k, r.Notes[k])
		}
	}
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}

func (r Report) writeJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
