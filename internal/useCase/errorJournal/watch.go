package errorJournalUseCase

import (
	"context"
	"fmt"
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
	lastCert := map[uuid.UUID]time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.CheckQueue(ctx)
			j.CheckCertificates(ctx, lastCert)
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
// renewed one). last remembers when each agent was written, so a certificate that stays near its end is not
// written every minute.
func (j *Journal) CheckCertificates(ctx context.Context, last map[uuid.UUID]time.Time) {
	if j.agents == nil {
		return
	}
	certs, err := j.agents.AgentCertificates(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to list the agent certificates")
		return
	}
	now := j.now()
	for _, c := range certs {
		e := errorJournal.Event{
			Kind: errorJournal.KindLabCertExpiry, Source: c.Name, Message: msgCertEnds, Notify: errorJournal.NotifyNew,
		}
		if c.NotAfter == nil || c.NotAfter.Sub(now) > j.cfg.CertExpiryWarn {
			delete(last, c.ID)
			j.condition(ctx, e, false)
			continue
		}
		if now.Sub(last[c.ID]) < certRepeat {
			continue
		}
		last[c.ID] = now
		left := c.NotAfter.Sub(now)
		e.Details = map[string]string{"agent": c.Name, "not_after": c.NotAfter.UTC().Format(time.RFC3339), "hours_left": strconv.Itoa(int(left.Hours()))}
		if left <= 0 {
			e.Message = "Laboratory agent certificate has expired"
		}
		j.condition(ctx, e, true)
	}
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

// AgentComponentErrors is the entry for the errors a laboratory agent reports about its operator, node agent and
// proxy: counts and recent messages, no tenant data.
func (j *Journal) AgentComponentErrors(name, component string, count int64, messages []string) {
	if count <= 0 && len(messages) == 0 {
		return
	}
	msg := fmt.Sprintf("Laboratory %s reports errors", component)
	if len(messages) > 0 {
		msg = messages[0]
	}
	j.Report(errorJournal.Event{
		Kind: errorJournal.KindLabComponent, Source: name + "/" + component, Message: msg,
		Details: map[string]string{"agent": name, "component": component, "count": strconv.FormatInt(count, 10)},
	})
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
