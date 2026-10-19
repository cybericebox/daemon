// Command loadtest drives a load test of an event on the Cyber ICE Box platform: teams of
// participants sign in, join the event and a team, open the tasks, wait for the labs, fetch the
// VPN config, submit flags and poll the scoreboard, then it reports latency per endpoint, error
// rates and lab start times.
//
// It is an operator tool: it never runs in CI and must only be pointed at a stand the owner has
// agreed to load. --confirm-domain must repeat --domain exactly. See docs/loadtest/RUNBOOK.md.
//
//	loadtest --domain stand.example.com --event-slug loadtest --creds creds.json --teams 14 --members 15
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type config struct {
	domain, confirmDomain, eventSlug string
	apiURL, apiHost                  string
	insecure                         bool

	credsPath          string
	teams              int
	membersMin         int
	membersMax         int
	accountOffset      int
	seed               uint64
	stage              string
	formTeams          bool
	teamFields         map[string]any
	teamFieldsJSON     string
	flagsPath          string
	liveToken          string
	liveViewers        int
	reportPath         string
	rampUp             time.Duration
	prewarm            time.Duration
	maxStartWait       time.Duration
	labTimeout         time.Duration
	runDuration        time.Duration
	thinkMin, thinkMax time.Duration
	labPoll            time.Duration
	resultsPoll        time.Duration
	warmPoll           time.Duration
	correctRatio       float64
	maxWrong           int
	abortErrorRate     float64
	dryRun             bool
}

func (c config) eventOrigin() string { return "https://" + c.eventSlug + "." + c.domain }
func (c config) idOrigin() string    { return "https://id." + c.domain }
func (c config) baseURL() string {
	if c.apiURL != "" {
		return c.apiURL
	}
	return "https://api." + c.domain
}

func parseFlags(args []string) (config, error) {
	var c config
	fs := flag.NewFlagSet("loadtest", flag.ContinueOnError)
	fs.StringVar(&c.domain, "domain", "", "base domain of the stand (DOMAIN of the backend), e.g. stand.example.com")
	fs.StringVar(&c.confirmDomain, "confirm-domain", "", "repeat --domain to confirm this stand may be loaded")
	fs.StringVar(&c.eventSlug, "event-slug", "", "tag of the test event (its site is <slug>.<domain>)")
	fs.StringVar(&c.apiURL, "api-url", "", "connect here instead of https://api.<domain> (a local stand, e.g. http://localhost:8080)")
	fs.StringVar(&c.apiHost, "api-host", "", "Host header for --api-url (default api.<domain>)")
	fs.BoolVar(&c.insecure, "insecure", false, "skip TLS verification")
	fs.StringVar(&c.credsPath, "creds", "", "credentials file written by loadtest-users create")
	fs.IntVar(&c.teams, "teams", 14, "number of teams")
	fs.IntVar(&c.membersMin, "members-min", 0, "fewest members per team (default: --members)")
	fs.IntVar(&c.membersMax, "members", 15, "most members per team")
	fs.IntVar(&c.accountOffset, "account-offset", 0, "skip this many accounts of the file")
	fs.Uint64Var(&c.seed, "seed", 1, "random seed: same seed, same team sizes and behaviour")
	fs.StringVar(&c.stage, "stage", "all", "prepare (sign in, join, teams), run (the event itself) or all")
	fs.BoolVar(&c.formTeams, "form-teams", true, "captains close the roster when everyone joined")
	fs.StringVar(&c.teamFieldsJSON, "team-fields", "", "JSON object of extra team fields the event requires")
	fs.StringVar(&c.flagsPath, "flags", "", "JSON {\"<task name or challenge id>\": \"<flag>\"} of flags to submit as correct")
	fs.StringVar(&c.liveToken, "live-token", "", "live-screen link token (enables --live-viewers)")
	fs.IntVar(&c.liveViewers, "live-viewers", 3, "anonymous live-screen viewers (needs --live-token)")
	fs.StringVar(&c.reportPath, "report", "", "write the JSON report here")
	fs.DurationVar(&c.rampUp, "ramp-up", 2*time.Minute, "spread the start of the participants over this time")
	fs.DurationVar(&c.prewarm, "prewarm", 0, "minimum time the participants sit on the event page before the start (light polling)")
	fs.DurationVar(&c.maxStartWait, "max-start-wait", 3*time.Hour, "longest wait for the event start")
	fs.DurationVar(&c.labTimeout, "lab-timeout", 20*time.Minute, "how long a lab may take to become Ready")
	fs.DurationVar(&c.runDuration, "duration", 30*time.Minute, "how long the participants play after the start")
	fs.DurationVar(&c.thinkMin, "think-min", 8*time.Second, "shortest think time between actions")
	fs.DurationVar(&c.thinkMax, "think-max", 45*time.Second, "longest think time between actions")
	fs.DurationVar(&c.labPoll, "lab-poll", 5*time.Second, "lab and stand poll interval")
	fs.DurationVar(&c.resultsPoll, "results-poll", 30*time.Second, "scoreboard poll interval (the event site polls every 30 s)")
	fs.DurationVar(&c.warmPoll, "warm-poll", 30*time.Second, "poll interval before the start")
	fs.Float64Var(&c.correctRatio, "correct-ratio", 0.35, "chance that an attempt on a task with a known flag submits it")
	fs.IntVar(&c.maxWrong, "max-wrong", 3, "wrong flags one participant sends per task")
	fs.Float64Var(&c.abortErrorRate, "abort-error-rate", 0.5, "stop when this share of the last minute fails (0 turns it off)")
	fs.BoolVar(&c.dryRun, "dry-run", false, "print the plan and exit")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	return c, c.validate()
}

func (c *config) validate() error {
	if c.membersMin == 0 {
		c.membersMin = c.membersMax
	}
	switch {
	case c.domain == "" || c.eventSlug == "":
		return errors.New("--domain and --event-slug are required")
	case c.confirmDomain != c.domain && !c.dryRun:
		return errors.New("--confirm-domain must repeat --domain exactly: this tool loads the stand it is pointed at")
	case c.credsPath == "":
		return errors.New("--creds is required")
	case c.stage != "all" && c.stage != "prepare" && c.stage != "run":
		return errors.New("--stage must be prepare, run or all")
	case c.teams < 1 || c.membersMax < 1 || c.membersMin < 1 || c.membersMin > c.membersMax:
		return errors.New("--teams and --members must be positive, --members-min not above --members")
	case c.thinkMin <= 0 || c.thinkMax < c.thinkMin:
		return errors.New("--think-min must be positive and not above --think-max")
	case c.correctRatio < 0 || c.correctRatio > 1:
		return errors.New("--correct-ratio is a share between 0 and 1")
	}
	if c.apiHost == "" && c.apiURL != "" {
		c.apiHost = "api." + c.domain
	}
	if c.teamFieldsJSON != "" {
		if err := json.Unmarshal([]byte(c.teamFieldsJSON), &c.teamFields); err != nil {
			return fmt.Errorf("--team-fields: %w", err)
		}
	}
	return nil
}

// runner holds what every participant shares.
type runner struct {
	cfg   config
	api   *api
	m     *metrics
	flags map[string]string

	mu    sync.Mutex
	event eventInfo
}

func (r *runner) setEvent(info eventInfo) {
	r.mu.Lock()
	r.event = info
	r.mu.Unlock()
}

func (r *runner) eventInfo() eventInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.event
}

func (r *runner) flagFor(c challenge) (string, bool) {
	if f, ok := r.flags[c.ID]; ok {
		return f, true
	}
	f, ok := r.flags[c.name()]
	return f, ok
}

// plan lays the accounts out into teams.
func plan(cfg config, accounts []account, r *runner) ([]*team, error) {
	rnd := rand.New(rand.NewPCG(cfg.seed, cfg.seed^0x9e3779b97f4a7c15))
	sizes := make([]int, cfg.teams)
	need := 0
	for i := range sizes {
		sizes[i] = cfg.membersMin
		if cfg.membersMax > cfg.membersMin {
			sizes[i] += rnd.IntN(cfg.membersMax - cfg.membersMin + 1)
		}
		need += sizes[i]
	}
	if len(accounts) < cfg.accountOffset+need {
		return nil, fmt.Errorf("the plan needs %d accounts after offset %d, the file has %d", need, cfg.accountOffset, len(accounts))
	}
	next := cfg.accountOffset
	teams := make([]*team, cfg.teams)
	for i := range teams {
		t := &team{index: i + 1, name: fmt.Sprintf("LT Team %02d", i+1), ready: make(chan struct{}), labsSeen: map[string]bool{}}
		t.joined.Add(sizes[i])
		for j := 0; j < sizes[i]; j++ {
			t.members = append(t.members, &participant{
				r: r, acct: accounts[next], team: t, captain: j == 0, solved: map[string]bool{}, wrong: map[string]int{},
				rnd: rand.New(rand.NewPCG(cfg.seed+uint64(next)+1, uint64(next)*7919+13)),
			})
			next++
		}
		teams[i] = t
	}
	return teams, nil
}

func readCreds(path string) ([]account, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct{ Accounts []account }
	if err = json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return file.Accounts, nil
}

func readFlags(path string) (map[string]string, error) {
	out := map[string]string{}
	if path == "" {
		return out, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return out, json.Unmarshal(raw, &out)
}

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = execute(ctx, cfg, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(1)
	}
}

// execute runs the stages and prints the report.
func execute(ctx context.Context, cfg config, out, logw io.Writer) error {
	accounts, err := readCreds(cfg.credsPath)
	if err != nil {
		return err
	}
	flags, err := readFlags(cfg.flagsPath)
	if err != nil {
		return fmt.Errorf("--flags: %w", err)
	}
	start := time.Now()
	m := newMetrics(start)
	r := &runner{cfg: cfg, m: m, flags: flags}
	teams, err := plan(cfg, accounts, r)
	if err != nil {
		return err
	}
	users := 0
	for _, t := range teams {
		users += len(t.members)
	}
	logf := func(format string, args ...any) {
		_, _ = fmt.Fprintf(logw, time.Now().Format("15:04:05 ")+format+"\n", args...)
	}
	logf("stand %s, event %s, stage %s: %d teams, %d participants, seed %d", cfg.domain, cfg.eventOrigin(), cfg.stage, len(teams), users, cfg.seed)
	if cfg.dryRun {
		for _, t := range teams {
			logf("  %s: %d members, captain %s", t.name, len(t.members), t.members[0].acct.Email)
		}
		return nil
	}
	r.api = newAPI(cfg.baseURL(), cfg.apiHost, cfg.insecure, m, users+16)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go r.monitor(ctx, cancel, logf)

	if cfg.stage != "run" {
		r.prepareAll(ctx, teams, logf)
	}
	if cfg.stage != "prepare" && ctx.Err() == nil {
		if cfg.stage == "run" {
			r.attachAll(ctx, teams, logf)
		}
		r.runAll(ctx, teams, logf)
	}

	report := m.report(time.Now())
	report.printText(out)
	if cfg.reportPath != "" {
		file, err := os.Create(cfg.reportPath)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		if err = report.writeJSON(file); err != nil {
			return err
		}
		logf("report written to %s", cfg.reportPath)
	}
	return nil
}

// prepareAll starts the participants over the ramp-up and waits until all have joined.
func (r *runner) prepareAll(ctx context.Context, teams []*team, logf func(string, ...any)) {
	total := 0
	for _, t := range teams {
		total += len(t.members)
	}
	logf("prepare: signing in %d participants over %s", total, r.cfg.rampUp)
	var wg sync.WaitGroup
	i := 0
	for _, t := range teams {
		for _, p := range t.members {
			delay := time.Duration(int64(r.cfg.rampUp) * int64(i) / int64(max(total, 1)))
			i++
			wg.Go(func() {
				if !sleep(ctx, delay) {
					p.release()
					return
				}
				if err := p.prepare(ctx); err != nil && ctx.Err() == nil {
					logf("participant %s: %v", p.acct.Email, err)
				}
			})
		}
	}
	wg.Wait()
	logf("prepare done")
}

// attachAll is the run stage on its own: sessions and the event id, for participants prepared earlier.
func (r *runner) attachAll(ctx context.Context, teams []*team, logf func(string, ...any)) {
	var wg sync.WaitGroup
	i := 0
	total := 0
	for _, t := range teams {
		total += len(t.members)
	}
	for _, t := range teams {
		for _, p := range t.members {
			delay := time.Duration(int64(r.cfg.rampUp) * int64(i) / int64(max(total, 1)))
			i++
			wg.Go(func() {
				if !sleep(ctx, delay) {
					return
				}
				if err := p.session(ctx); err != nil {
					logf("participant %s: %v", p.acct.Email, err)
					return
				}
				if _, err := p.info(ctx); err != nil {
					logf("participant %s: %v", p.acct.Email, err)
				}
			})
		}
	}
	wg.Wait()
}

// runAll waits for the event start (prewarming meanwhile), then lets everyone play.
func (r *runner) runAll(ctx context.Context, teams []*team, logf func(string, ...any)) {
	info := r.eventInfo()
	startAt := info.StartTime
	if wait := time.Until(startAt); wait > r.cfg.maxStartWait {
		logf("the event starts in %s, longer than --max-start-wait", wait.Round(time.Second))
		return
	}
	warmUntil := startAt
	if min := time.Now().Add(r.cfg.prewarm); warmUntil.Before(min) {
		warmUntil = min
	}
	if time.Now().Before(warmUntil) {
		logf("prewarm: participants wait on the event page until %s", warmUntil.Format("15:04:05"))
		var wg sync.WaitGroup
		for _, t := range teams {
			for _, p := range t.members {
				if p.eventID == "" {
					continue
				}
				wg.Go(func() { p.warm(ctx, warmUntil) })
			}
		}
		wg.Wait()
	}
	if ctx.Err() != nil {
		return
	}
	base := time.Now()
	if startAt.After(base.Add(-time.Hour)) && startAt.Before(base) {
		base = startAt // labs deploy from the event start, even when the run began late
	}
	logf("run: the event is on, participants play for %s", r.cfg.runDuration)
	playCtx, stop := context.WithTimeout(ctx, r.cfg.runDuration)
	defer stop()
	var wg sync.WaitGroup
	for _, t := range teams {
		for _, p := range t.members {
			if p.eventID == "" {
				continue
			}
			wg.Go(func() {
				// Everybody does not click at the same second.
				if sleep(playCtx, time.Duration(p.int64N(int64(min(10*time.Second, r.cfg.runDuration/10)+1)))) {
					p.play(playCtx, base)
				}
			})
		}
	}
	if r.cfg.liveToken != "" {
		for i := 0; i < r.cfg.liveViewers; i++ {
			viewer := &participant{r: r, eventID: info.EventID, rnd: rand.New(rand.NewPCG(r.cfg.seed, uint64(i)+99))}
			wg.Go(func() { viewer.viewScreen(playCtx, r.cfg.liveToken) })
		}
	}
	wg.Wait()
}

// monitor prints progress and applies the abort rule.
func (r *runner) monitor(ctx context.Context, cancel context.CancelFunc, logf func(string, ...any)) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		requests, errs, throttled := r.m.window(time.Minute)
		logf("recent minutes: %d requests, %d errors, %d throttled (429)", requests, errs, throttled)
		if r.cfg.abortErrorRate > 0 && requests >= 200 && float64(errs+throttled)/float64(requests) > r.cfg.abortErrorRate {
			logf("ABORT: %.0f%% of the recent requests failed (limit %.0f%%)", pct(errs+throttled, requests), r.cfg.abortErrorRate*100)
			cancel()
			return
		}
	}
}
