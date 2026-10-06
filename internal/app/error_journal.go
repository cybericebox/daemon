package app

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

// errorJournalConfig maps the ERROR_JOURNAL_* tunables to the journal's configuration. Links in messages point at
// the admin site.
func errorJournalConfig(cfg *config.Config) errorJournalUseCase.Config {
	ej := cfg.ErrorJournal
	return errorJournalUseCase.Config{
		SamplesPerGroup: ej.SamplesPerGroup, Retention: ej.Retention, NotifyCooldown: ej.NotifyCooldown,
		SpikeThreshold: ej.SpikeThreshold, SpikeWindow: ej.SpikeWindow, BufferSize: ej.BufferSize,
		NotFoundFlushEvery: ej.NotFoundFlushInterval, QueueStallAfter: ej.QueueStallAfter,
		QueueBacklogLimit: ej.QueueBacklogLimit, CertExpiryWarn: ej.CertExpiryWarn,
		AgentOfflineAfter: ej.AgentOfflineAfter, WatchEvery: ej.WatchInterval,
		AdminURL: config.URL(cfg.Auth.Hosts.Admin, ""), Environment: cfg.Environment,
	}
}

// agentLink tells the error journal whether the monitoring link of one agent works (the monitoring runner's
// LinkSink).
type agentLink struct {
	journal *errorJournalUseCase.Journal
	id      uuid.UUID
	name    string
}

func (l agentLink) Down(cause error) {
	l.journal.AgentLinkDown(context.Background(), l.id, l.name, cause)
}
func (l agentLink) Up() { l.journal.AgentLinkUp(context.Background(), l.id, l.name) }

// agentErrors maps the error journal part of a monitoring message of one agent to the journal's own shape.
func agentErrors(report *labpb.ErrorJournal) errorJournalUseCase.AgentErrors {
	out := errorJournalUseCase.AgentErrors{}
	for _, c := range report.GetComponents() {
		comp := errorJournalUseCase.AgentComponent{Component: c.GetComponent(), Instance: c.GetInstance()}
		for _, g := range c.GetGroups() {
			comp.Groups = append(comp.Groups, errorJournalUseCase.AgentErrorGroup{
				Fingerprint: g.GetFingerprint(), Kind: g.GetKind(), Normalized: g.GetNormalized(), Count: g.GetCount(),
				First: time.UnixMilli(g.GetFirstUnixMs()).UTC(), Last: time.UnixMilli(g.GetLastUnixMs()).UTC(), Samples: g.GetSamples(),
			})
		}
		out.Components = append(out.Components, comp)
	}
	for _, d := range report.GetDeployFailures() {
		out.DeployFailures = append(out.DeployFailures, errorJournalUseCase.AgentDeployFailure{
			LabGroup: d.GetLabGroup(), Lab: d.GetLab(), ReasonCode: d.GetReasonCode(), Device: d.GetDevice(),
			Message: d.GetMessage(), At: time.UnixMilli(d.GetAtUnixMs()).UTC(),
		})
	}
	if unix := report.GetClientCertNotAfterUnix(); unix > 0 {
		end := time.Unix(unix, 0).UTC()
		out.CertNotAfter = &end
	}
	return out
}
