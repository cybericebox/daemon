package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/url"
	"strings"
	"sync"
	"time"
)

// account is one line of the credentials file the loadtest-users command writes.
type account struct {
	N        int    `json:"n"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Cookie   string `json:"cookie"`
}

// team is one load-test team. The first member is the captain: creates the team, shares the join
// code, closes the roster, and watches every lab of the team.
type team struct {
	index   int
	name    string
	members []*participant

	ready  chan struct{} // closed when the join code is known (or creation failed)
	code   string
	joined sync.WaitGroup // every member finished joining

	mu       sync.Mutex
	labsSeen map[string]bool // challenge ID -> first outcome recorded
}

// markLab records the outcome of a team's lab once, however many members observe it.
func (t *team) markLab(challenge string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.labsSeen[challenge] {
		return false
	}
	t.labsSeen[challenge] = true
	return true
}

// challenge is the part of an own-board entry the flow uses.
type challenge struct {
	ID             string
	Locked         bool
	Closed         bool
	Infrastructure bool
	SolvedAt       *string
	Snapshot       json.RawMessage
}

func (c challenge) name() string {
	var s struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(c.Snapshot, &s)
	return s.Name
}

type participant struct {
	r       *runner
	acct    account
	team    *team
	captain bool
	rnd     *rand.Rand
	rndMu   sync.Mutex // the poll loops of one participant run in their own goroutines

	cookie     string
	eventID    string
	joinedOnce sync.Once

	solved map[string]bool
	wrong  map[string]int
}

func (p *participant) eventCall(method, path, label string, body any, expect ...int) request {
	return request{Method: method, Path: path, Label: label, Origin: p.r.cfg.eventOrigin(), Cookie: p.cookie, Body: body, Expect: expect}
}

func (p *participant) think(ctx context.Context) bool {
	return sleep(ctx, p.r.cfg.thinkMin+time.Duration(p.int64N(int64(p.r.cfg.thinkMax-p.r.cfg.thinkMin)+1)))
}

func (p *participant) jitter(base time.Duration) time.Duration {
	return base/2 + time.Duration(p.int64N(int64(base)+1))
}

// session makes sure the participant holds a working session: the cookie of the credentials file,
// or a password sign-in when the file has none (a local stand without a captcha).
func (p *participant) session(ctx context.Context) error {
	p.cookie = p.acct.Cookie
	if p.cookie == "" {
		res := p.r.api.do(ctx, request{Method: "POST", Path: "/api/auth/sign-in", Label: "/api/auth/sign-in", Origin: p.r.cfg.idOrigin(),
			Body: map[string]string{"Email": p.acct.Email, "Password": p.acct.Password}})
		if !res.ok() {
			return fmt.Errorf("sign-in of %s: HTTP %d %s", p.acct.Email, res.Status, res.Message)
		}
		if p.cookie = cookieFrom(res.Header); p.cookie == "" {
			return fmt.Errorf("sign-in of %s returned no session cookie", p.acct.Email)
		}
	}
	res := p.r.api.do(ctx, request{Method: "GET", Path: "/api/auth/me", Label: "/api/auth/me", Origin: p.r.cfg.idOrigin(), Cookie: p.cookie})
	if !res.ok() {
		return fmt.Errorf("session of %s does not open: HTTP %d", p.acct.Email, res.Status)
	}
	return nil
}

type eventInfo struct {
	EventID    string
	StartTime  time.Time
	FinishTime time.Time
	Status     int
}

func (p *participant) info(ctx context.Context) (eventInfo, error) {
	res := p.r.api.do(ctx, p.eventCall("GET", "/api/events/self/info", "/api/events/self/info", nil))
	if !res.ok() {
		return eventInfo{}, fmt.Errorf("event info: HTTP %d", res.Status)
	}
	var info eventInfo
	if err := json.Unmarshal(res.Data, &info); err != nil || info.EventID == "" {
		return info, fmt.Errorf("event info: no EventID in the answer")
	}
	p.eventID = info.EventID
	p.r.setEvent(info)
	return info, nil
}

// prepare is everything before the event: session, event info, joining the event, the team.
func (p *participant) prepare(ctx context.Context) error {
	defer func() {
		if p.team != nil {
			// A member that failed must not hold the captain back from closing the roster.
			p.release()
		}
	}()
	if err := p.session(ctx); err != nil {
		p.fail("session")
		return err
	}
	if _, err := p.info(ctx); err != nil {
		p.fail("event info")
		return err
	}
	// 409-style answers mean «already joined» on a re-run.
	p.r.api.do(ctx, p.eventCall("POST", "/api/events/self/join", "/api/events/self/join", nil, 400, 409, 422))
	p.r.api.do(ctx, p.eventCall("GET", "/api/events/self/join/info", "/api/events/self/join/info", nil))

	t := p.team
	if p.captain {
		res := p.r.api.do(ctx, p.eventCall("POST", "/api/events/"+p.eventID+"/teams", "/api/events/{id}/teams",
			map[string]any{"Name": t.name, "Fields": p.r.cfg.teamFields}, 400, 409, 422))
		if !res.ok() {
			// A re-run: the team exists already, read its code below.
			p.r.m.note("team create refused")
		}
		code, err := p.teamCode(ctx)
		if err != nil {
			close(t.ready)
			p.fail("team create")
			return err
		}
		t.code = code
		close(t.ready)
		p.release()
		// Wait for the roster, then close it, as a captain does.
		waitGroup(ctx, &t.joined, 2*time.Minute)
		if p.r.cfg.formTeams {
			p.r.api.do(ctx, p.eventCall("POST", "/api/events/"+p.eventID+"/teams/mine/form", "/api/events/{id}/teams/mine/form", nil, 400, 409, 422))
		}
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.ready:
	}
	if t.code == "" {
		p.fail("team join")
		return fmt.Errorf("team %s has no join code", t.name)
	}
	res := p.r.api.do(ctx, p.eventCall("POST", "/api/events/"+p.eventID+"/teams/join", "/api/events/{id}/teams/join",
		map[string]string{"JoinCode": t.code}, 400, 409, 422))
	if !res.ok() {
		p.r.m.note("team join refused")
	}
	p.release()
	return nil
}

func (p *participant) fail(what string) { p.r.m.note("prepare failed: " + what) }

// release marks the participant as finished joining, however many exit paths it takes.
func (p *participant) release() {
	p.joinedOnce.Do(func() { p.team.joined.Done() })
}

func waitGroup(ctx context.Context, wg *sync.WaitGroup, limit time.Duration) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	case <-time.After(limit):
	}
}

// teamCode reads the join code of the participant's own team (the captain sees it).
func (p *participant) teamCode(ctx context.Context) (string, error) {
	res := p.r.api.do(ctx, p.eventCall("GET", "/api/events/"+p.eventID+"/teams/mine", "/api/events/{id}/teams/mine", nil))
	if !res.ok() {
		return "", fmt.Errorf("team read: HTTP %d", res.Status)
	}
	var mine struct{ JoinCode string }
	if err := json.Unmarshal(res.Data, &mine); err != nil || mine.JoinCode == "" {
		return "", fmt.Errorf("team read: no JoinCode")
	}
	return mine.JoinCode, nil
}

// board reads the team board and returns its challenges.
func (p *participant) board(ctx context.Context) ([]challenge, bool) {
	res := p.r.api.do(ctx, p.eventCall("GET", "/api/events/"+p.eventID+"/teams/challenges/mine", "/api/events/{id}/teams/challenges/mine", nil))
	if !res.ok() {
		return nil, false
	}
	var board struct {
		Challenges []struct {
			ID               string
			EventChallengeID string
			Locked           bool
			Closed           bool
			Infrastructure   bool
			SolvedAt         *string
			Snapshot         json.RawMessage
		}
	}
	if err := json.Unmarshal(res.Data, &board); err != nil {
		return nil, false
	}
	out := make([]challenge, 0, len(board.Challenges))
	for _, c := range board.Challenges {
		id := c.EventChallengeID
		if id == "" {
			id = c.ID
		}
		out = append(out, challenge{ID: id, Locked: c.Locked, Closed: c.Closed, Infrastructure: c.Infrastructure, SolvedAt: c.SolvedAt, Snapshot: c.Snapshot})
	}
	return out, true
}

type labState struct {
	Phase string
	Ready bool
}

func (p *participant) labStatus(ctx context.Context, challengeID string) (labState, bool) {
	res := p.r.api.do(ctx, p.eventCall("GET", "/api/events/"+p.eventID+"/teams/challenges/"+challengeID+"/lab", "/api/events/{id}/teams/challenges/{id}/lab", nil))
	if !res.ok() {
		return labState{}, false
	}
	var st labState
	if json.Unmarshal(res.Data, &st) != nil {
		return labState{}, false
	}
	return st, true
}

// watchLabs polls every infrastructure challenge of the team until each is Ready, Failed, or the
// lab timeout passes; the captain does it so the measurement does not multiply by team size.
func (p *participant) watchLabs(ctx context.Context, base time.Time, challenges []challenge) {
	pending := map[string]challenge{}
	for _, c := range challenges {
		if c.Infrastructure {
			pending[c.ID] = c
		}
	}
	deadline := base.Add(p.r.cfg.labTimeout)
	last := map[string]string{}
	for len(pending) > 0 {
		for id, c := range pending {
			st, ok := p.labStatus(ctx, id)
			if ok {
				last[id] = st.Phase
			}
			switch {
			case ok && st.Ready:
				p.recordLab(c, base, st.Phase, true)
				delete(pending, id)
			case ok && st.Phase == "Failed":
				p.recordLab(c, base, st.Phase, false)
				delete(pending, id)
			}
		}
		if len(pending) == 0 {
			return
		}
		if time.Now().After(deadline) {
			for id, c := range pending {
				p.recordLab(c, base, last[id], false)
			}
			return
		}
		if !sleep(ctx, p.jitter(p.r.cfg.labPoll)) {
			// The run ended first: what is still pending counts as not ready.
			for id, c := range pending {
				p.recordLab(c, base, last[id], false)
			}
			return
		}
	}
}

func (p *participant) recordLab(c challenge, base time.Time, phase string, ready bool) {
	if !p.team.markLab(c.ID) {
		return
	}
	p.r.m.recordLab(labTime{Team: p.team.index, Challenge: c.name(), Phase: phase, Wait: max(time.Since(base), 0), Ready: ready})
}

// vpn waits for the team stand, then fetches the personal VPN config once.
func (p *participant) vpn(ctx context.Context, base time.Time) {
	deadline := base.Add(p.r.cfg.labTimeout)
	for {
		res := p.r.api.do(ctx, p.eventCall("GET", "/api/events/"+p.eventID+"/teams/labs/stand", "/api/events/{id}/teams/labs/stand", nil))
		var st struct{ VPNReady bool }
		if res.ok() && json.Unmarshal(res.Data, &st) == nil && st.VPNReady {
			break
		}
		if time.Now().After(deadline) || !sleep(ctx, p.jitter(p.r.cfg.labPoll)) {
			p.r.m.note("vpn never ready")
			return
		}
	}
	res := p.r.api.do(ctx, p.eventCall("GET", "/api/events/"+p.eventID+"/teams/labs/vpn", "/api/events/{id}/teams/labs/vpn", nil))
	var cfg struct{ Config string }
	if res.ok() && json.Unmarshal(res.Data, &cfg) == nil && strings.TrimSpace(cfg.Config) != "" {
		p.r.m.note("vpn config fetched")
	} else {
		p.r.m.note("vpn config failed")
	}
}

// pollResults is the scoreboard page: one full snapshot, then cheap change polls.
func (p *participant) pollResults(ctx context.Context, view string) {
	path := "/api/events/" + p.eventID + "/results"
	var revision int64
	freeze := ""
	needSnapshot := true
	for {
		if needSnapshot {
			q := ""
			if view != "" {
				q = "?view=" + url.QueryEscape(view)
			}
			res := p.r.api.do(ctx, request{Method: "GET", Path: path + q, Label: "/api/events/{id}/results", Origin: p.r.cfg.eventOrigin(), Cookie: p.cookie})
			if res.ok() {
				var snap struct{ Revision int64 }
				if json.Unmarshal(res.Data, &snap) == nil {
					revision, needSnapshot = snap.Revision, false
				}
			}
		} else {
			q := fmt.Sprintf("?since=%d&freeze=%s", revision, url.QueryEscape(freeze))
			res := p.r.api.do(ctx, request{Method: "GET", Path: path + "/changes" + q, Label: "/api/events/{id}/results/changes", Origin: p.r.cfg.eventOrigin(), Cookie: p.cookie, Expect: []int{304}})
			if res.ok() {
				var ch struct {
					Revision         int64
					FreezeKey        string
					SnapshotRequired bool
				}
				if json.Unmarshal(res.Data, &ch) == nil {
					revision, freeze, needSnapshot = ch.Revision, ch.FreezeKey, ch.SnapshotRequired
				}
			}
		}
		if !sleep(ctx, p.jitter(p.r.cfg.resultsPoll)) {
			return
		}
	}
}

// viewScreen is a live-screen viewer: it has no account, only the screen link token.
func (p *participant) viewScreen(ctx context.Context, token string) {
	for {
		p.r.api.do(ctx, request{Method: "GET", Path: "/api/events/self/live-screen?token=" + url.QueryEscape(token), Label: "/api/events/self/live-screen", Origin: p.r.cfg.eventOrigin()})
		if p.eventID != "" {
			p.r.api.do(ctx, request{Method: "GET", Path: "/api/events/" + p.eventID + "/results?view=live", Label: "/api/events/{id}/results", Origin: p.r.cfg.eventOrigin()})
		}
		if !sleep(ctx, p.jitter(p.r.cfg.resultsPoll)) {
			return
		}
	}
}

// warm is the participants sitting on the event page before it starts: light polling.
func (p *participant) warm(ctx context.Context, until time.Time) {
	for time.Now().Before(until) {
		p.r.api.do(ctx, p.eventCall("GET", "/api/events/self/info", "/api/events/self/info", nil))
		p.r.api.do(ctx, request{Method: "GET", Path: "/api/events/" + p.eventID + "/results", Label: "/api/events/{id}/results", Origin: p.r.cfg.eventOrigin(), Cookie: p.cookie})
		if !sleep(ctx, p.jitter(p.r.cfg.warmPoll)) {
			return
		}
	}
}

// play is the participant during the event until the deadline.
func (p *participant) play(ctx context.Context, base time.Time) {
	var wg sync.WaitGroup
	wg.Go(func() { p.pollResults(ctx, "") })
	defer wg.Wait()

	var challenges []challenge
	for ctx.Err() == nil {
		var ok bool
		if challenges, ok = p.board(ctx); ok && len(challenges) > 0 {
			break
		}
		if !sleep(ctx, p.jitter(p.r.cfg.labPoll)) {
			return
		}
	}
	if p.captain {
		wg.Go(func() { p.watchLabs(ctx, base, challenges) })
	}
	wg.Go(func() { p.vpn(ctx, base) })

	for ctx.Err() == nil {
		if !p.think(ctx) {
			return
		}
		switch roll := p.float(); {
		case roll < 0.25:
			if fresh, ok := p.board(ctx); ok {
				challenges = fresh
			}
		case roll < 0.35:
			if c, ok := p.pick(challenges, false); ok {
				p.r.api.do(ctx, p.eventCall("GET", "/api/events/"+p.eventID+"/teams/challenges/"+c.ID+"/solves", "/api/events/{id}/teams/challenges/{id}/solves", nil))
			}
		default:
			p.attempt(ctx, challenges)
		}
	}
}

// pick chooses a challenge the team can still work on.
func (p *participant) pick(challenges []challenge, unsolvedOnly bool) (challenge, bool) {
	var open []challenge
	for _, c := range challenges {
		if c.Locked || c.Closed || (unsolvedOnly && (c.SolvedAt != nil || p.solved[c.ID])) {
			continue
		}
		open = append(open, c)
	}
	if len(open) == 0 {
		return challenge{}, false
	}
	return open[p.intN(len(open))], true
}

// attempt opens a task, lets the participant think, and submits a flag: the real one when the
// flag file has it and the dice say so, a wrong one otherwise.
func (p *participant) attempt(ctx context.Context, challenges []challenge) {
	c, ok := p.pick(challenges, true)
	if !ok {
		return
	}
	p.r.api.do(ctx, p.eventCall("POST", "/api/events/"+p.eventID+"/teams/challenges/"+c.ID+"/open", "/api/events/{id}/teams/challenges/{id}/open", nil))
	if c.Infrastructure {
		if st, ok := p.labStatus(ctx, c.ID); !ok || !st.Ready {
			p.r.m.note("attempt skipped: lab not ready")
			return
		}
		p.r.api.do(ctx, p.eventCall("POST", "/api/events/"+p.eventID+"/teams/challenges/"+c.ID+"/lab/link", "/api/events/{id}/teams/challenges/{id}/lab/link", nil, 400, 404, 409, 422))
	}
	if !p.think(ctx) {
		return
	}
	answer, correct := "", false
	if flag, known := p.r.flagFor(c); known && p.float() < p.r.cfg.correctRatio {
		answer, correct = flag, true
	} else {
		if p.wrong[c.ID] >= p.r.cfg.maxWrong {
			return
		}
		p.wrong[c.ID]++
		answer = fmt.Sprintf("flag{loadtest-wrong-%08x}", p.u32())
	}
	res := p.r.api.do(ctx, request{Method: "POST", Path: "/api/events/" + p.eventID + "/teams/challenges/" + c.ID + "/submit", Label: "/api/events/{id}/teams/challenges/{id}/submit",
		Origin: p.r.cfg.eventOrigin(), Cookie: p.cookie, Body: map[string]string{"Answer": answer}, Header: map[string]string{"Idempotency-Key": newUUID()}, Expect: []int{400, 403, 409, 422}})
	var out struct{ Correct bool }
	switch {
	case !res.ok():
		p.r.m.note("submit refused")
		if correct {
			p.solved[c.ID] = true // most likely a teammate solved it first
		}
	case json.Unmarshal(res.Data, &out) == nil && out.Correct:
		p.solved[c.ID] = true
		p.r.m.note("flag accepted")
	case correct:
		p.r.m.note("known flag rejected")
	default:
		p.r.m.note("wrong flag answered")
	}
}

func (p *participant) int64N(n int64) int64 {
	p.rndMu.Lock()
	defer p.rndMu.Unlock()
	return p.rnd.Int64N(n)
}

func (p *participant) intN(n int) int {
	p.rndMu.Lock()
	defer p.rndMu.Unlock()
	return p.rnd.IntN(n)
}

func (p *participant) float() float64 {
	p.rndMu.Lock()
	defer p.rndMu.Unlock()
	return p.rnd.Float64()
}

func (p *participant) u32() uint32 {
	p.rndMu.Lock()
	defer p.rndMu.Unlock()
	return p.rnd.Uint32()
}
