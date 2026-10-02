package errorJournalUseCase

import (
	"context"
	"strconv"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

const (
	msgQueueStalled = "Job queue is stalled: ready jobs are not taken by workers"
	msgQueueGrowing = "Job queue is growing: more jobs wait than the limit"
	msgCertEnds     = "Laboratory agent certificate ends soon"
	msgAgentOffline = "Laboratory agent is offline"
	// certRepeat is how often a certificate that stays close to its end is written again.
	certRepeat = time.Hour
)

// offlineState is one agent's outage: since when it fails to answer, and whether it was reported.
type offlineState struct {
	since    time.Time
	reported bool
}

// Watch runs the periodic checks (queue, certificates) until ctx ends. They run in the daemon, not in the job
// queue: a stalled queue could not report itself from a job.
func (j *Journal) Watch(ctx context.Context) {
	ticker := time.NewTicker(j.cfg.WatchEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.CheckQueue(ctx)
			j.CheckCertificates(ctx)
		}
	}
}

// CheckQueue reports a stalled or growing job queue, and resolves the groups when it recovers.
func (j *Journal) CheckQueue(ctx context.Context) {
	stats, err := j.repo.QueueStats(ctx, j.now())
	if err != nil {
		log.Warn().Err(err).Msg("Failed to read the job queue statistics")
		return
	}
	queue := stats.Queue
	if queue == "" {
		queue = "default"
	}
	details := map[string]string{
		"queue": queue, "waiting": strconv.FormatInt(stats.Waiting, 10),
		"oldest_wait_seconds": strconv.Itoa(int(stats.OldestWait.Seconds())),
	}
	stalled := stats.Waiting > 0 && stats.OldestWait >= j.cfg.QueueStallAfter
	growing := stats.Waiting >= int64(j.cfg.QueueBacklogLimit)
	j.condition(ctx, errorJournal.Event{Kind: errorJournal.KindQueue, Source: queue, Message: msgQueueStalled, Details: details, Notify: errorJournal.NotifyNew}, stalled)
	j.condition(ctx, errorJournal.Event{Kind: errorJournal.KindQueue, Source: queue, Message: msgQueueGrowing, Details: details, Notify: errorJournal.NotifyNew}, growing)
}

// condition records e while the condition holds and resolves its group when it stops holding.
func (j *Journal) condition(ctx context.Context, e errorJournal.Event, holds bool) {
	if holds {
		if _, err := j.Record(ctx, e); err != nil {
			log.Warn().Err(err).Str("kind", string(e.Kind)).Msg("Failed to record an error in the journal")
		}
		return
	}
	if err := j.repo.ResolveByFingerprint(ctx, errorJournal.Fingerprint(e), j.now()); err != nil {
		log.Warn().Err(err).Msg("Failed to resolve an error group")
	}
}

// CheckCertificates reports agent certificates that end within the warning period (and resolves the group of a
// renewed one), reading the agent registry. A certificate that stays near its end is written once an hour, not
// every minute.
func (j *Journal) CheckCertificates(ctx context.Context) {
	if j.agents == nil {
		return
	}
	certs, err := j.agents.AgentCertificates(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to list the agent certificates")
		return
	}
	for _, c := range certs {
		j.certCondition(ctx, c.Name, c.NotAfter)
	}
}

// certCondition records the warning for one agent's certificate, or resolves it when the certificate is far from
// its end. The registry check and the agent's own report of its certificate meet here: one group per agent.
func (j *Journal) certCondition(ctx context.Context, name string, notAfter *time.Time) {
	now := j.now()
	e := errorJournal.Event{Kind: errorJournal.KindLabCertExpiry, Source: name, Message: msgCertEnds, Notify: errorJournal.NotifyNew}
	if notAfter == nil || notAfter.Sub(now) > j.cfg.CertExpiryWarn {
		j.mu.Lock()
		delete(j.certLast, name)
		j.mu.Unlock()
		j.condition(ctx, e, false)
		return
	}
	j.mu.Lock()
	recent := now.Sub(j.certLast[name]) < certRepeat
	if !recent {
		j.certLast[name] = now
	}
	j.mu.Unlock()
	if recent {
		return
	}
	left := notAfter.Sub(now)
	e.Details = map[string]string{"agent": name, "not_after": notAfter.UTC().Format(time.RFC3339), "hours_left": strconv.Itoa(int(left.Hours()))}
	if left <= 0 {
		e.Message = "Laboratory agent certificate has expired"
	}
	j.condition(ctx, e, true)
}

// AgentLinkDown tells the journal that the monitoring link to an agent failed. The agent is reported offline once
// it has been unreachable for AgentOfflineAfter; one outage is one report.
func (j *Journal) AgentLinkDown(ctx context.Context, id uuid.UUID, name string, cause error) {
	now := j.now()
	key := id.String()
	j.mu.Lock()
	st := j.offline[key]
	if st == nil {
		st = &offlineState{since: now}
		j.offline[key] = st
	}
	report := !st.reported && now.Sub(st.since) >= j.cfg.AgentOfflineAfter
	if report {
		st.reported = true
	}
	since := st.since
	j.mu.Unlock()
	if !report {
		return
	}
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	j.Report(errorJournal.Event{
		Kind: errorJournal.KindLabAgentOffline, Source: name, Message: msgAgentOffline,
		Details: map[string]string{"agent": name, "since": since.UTC().Format(time.RFC3339), "reason": reason},
		At:      now,
	})
}

// AgentLinkUp tells the journal that the link works; an agent that was reported offline is resolved.
func (j *Journal) AgentLinkUp(ctx context.Context, id uuid.UUID, name string) {
	key := id.String()
	j.mu.Lock()
	st := j.offline[key]
	delete(j.offline, key)
	j.mu.Unlock()
	if st == nil || !st.reported {
		return
	}
	e := errorJournal.Event{Kind: errorJournal.KindLabAgentOffline, Source: name, Message: msgAgentOffline}
	if err := j.repo.ResolveByFingerprint(ctx, errorJournal.Fingerprint(e), j.now()); err != nil {
		log.Warn().Err(err).Msg("Failed to resolve the agent offline group")
	}
}

// AgentErrors is what a laboratory agent reports in a monitoring message: errors of its own components (only for
// the platform's tenant), lab deploys of this tenant that failed, and the end of the client certificate. Counts and
// short scrubbed messages only; no tenant data.
type AgentErrors struct {
	Components     []AgentComponent
	DeployFailures []AgentDeployFailure
	// CertNotAfter is the end of the platform's client certificate at this agent; nil when not reported.
	CertNotAfter *time.Time
}

type AgentComponent struct {
	Component string
	Instance  string
	Groups    []AgentErrorGroup
}

// AgentErrorGroup is one distinct error of a component: Fingerprint is the agent's own, stable across restarts
// and replicas.
type AgentErrorGroup struct {
	Fingerprint string
	Kind        string
	Normalized  string
	Count       int64
	First, Last time.Time
	Samples     []string
}

type AgentDeployFailure struct {
	LabGroup, Lab, ReasonCode, Device, Message string
	At                                         time.Time
}

// ReportAgentErrors maps an agent's report to the journal: component groups become lab_component errors (the
// fingerprint is prefixed with the agent name through the source), failed deploys lab_deploy, the certificate end
// the lab_cert_expiry check.
func (j *Journal) ReportAgentErrors(ctx context.Context, agent string, in AgentErrors) {
	for _, comp := range in.Components {
		for _, g := range comp.Groups {
			if g.Count <= 0 {
				continue
			}
			msg := g.Normalized
			if len(g.Samples) > 0 {
				msg = g.Samples[0]
			}
			j.Report(errorJournal.Event{
				Kind: errorJournal.KindLabComponent, Source: agent + "/" + comp.Component, Key: g.Fingerprint,
				Message: msg, Count: int(min(g.Count, 1_000_000)), At: g.Last,
				Details: map[string]string{
					"agent": agent, "component": comp.Component, "instance": comp.Instance, "kind": g.Kind,
					"pattern": g.Normalized, "count": strconv.FormatInt(g.Count, 10),
				},
			})
		}
	}
	for _, d := range in.DeployFailures {
		msg := d.ReasonCode
		if d.Message != "" {
			msg += ": " + d.Message
		}
		j.Report(errorJournal.Event{
			Kind: errorJournal.KindLabDeploy, Source: agent, Message: msg, At: d.At,
			Details: map[string]string{
				"agent": agent, "lab_group": d.LabGroup, "lab": d.Lab, "device": d.Device, "reason_code": d.ReasonCode,
			},
		})
	}
	if in.CertNotAfter != nil {
		j.certCondition(ctx, agent, in.CertNotAfter)
	}
}

// AgentRecords is the registry of enrolled agents (the part the certificate check reads).
type AgentRecords interface {
	ListRecords(ctx context.Context) ([]infraModel.AgentRecord, error)
}

type recordCertificates struct{ store AgentRecords }

// CertificatesOf reads the certificate ends from the agent registry. It does not call the agents: it is cheap
// enough to run every minute.
func CertificatesOf(store AgentRecords) AgentCertificates { return recordCertificates{store: store} }

func (r recordCertificates) AgentCertificates(ctx context.Context) ([]AgentCertificate, error) {
	records, err := r.store.ListRecords(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentCertificate, 0, len(records))
	for _, rec := range records {
		out = append(out, AgentCertificate{ID: rec.ID, Name: rec.Name, NotAfter: rec.CertNotAfter})
	}
	return out, nil
}
