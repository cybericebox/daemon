package errorJournalUseCase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

func openGroups(h *harness, kind errorJournal.Kind) int {
	n := 0
	for _, g := range h.repo.groups {
		if g.Kind == kind && g.Status == errorJournal.StatusOpen {
			n++
		}
	}
	return n
}

func TestStalledQueueIsReportedAndResolvedOnRecovery(t *testing.T) {
	cfg := DefaultConfig()
	h := newHarness(cfg)
	h.repo.queue = QueueStats{Waiting: 4, OldestWait: 10 * time.Minute}

	h.j.CheckQueue(ctx)
	assert.Equal(t, 1, openGroups(h, errorJournal.KindQueue))
	assert.Equal(t, 1, h.tg.count("100"))
	h.j.CheckQueue(ctx)
	assert.Equal(t, 1, h.tg.count("100"), "one incident is one message")

	h.repo.queue = QueueStats{Waiting: 0}
	h.j.CheckQueue(ctx)
	assert.Equal(t, 0, openGroups(h, errorJournal.KindQueue))

	h.repo.queue = QueueStats{Waiting: 4, OldestWait: 10 * time.Minute}
	h.j.CheckQueue(ctx)
	assert.Equal(t, 2, h.tg.count("100"), "a new incident is told again")
}

func TestGrowingQueueIsReported(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueBacklogLimit = 100
	h := newHarness(cfg)
	h.repo.queue = QueueStats{Waiting: 500, OldestWait: time.Second}
	h.j.CheckQueue(ctx)
	require.Equal(t, 1, openGroups(h, errorJournal.KindQueue))
	assert.Contains(t, h.tg.last("100"), "growing")
}

func TestHealthyQueueRecordsNothing(t *testing.T) {
	h := newHarness(DefaultConfig())
	h.repo.queue = QueueStats{Waiting: 3, OldestWait: time.Second}
	h.j.CheckQueue(ctx)
	assert.Empty(t, h.repo.groups)
}

func TestCertificateCloseToExpiryIsReportedOncePerHour(t *testing.T) {
	h := newHarness(DefaultConfig())
	soon := h.now.Add(3 * 24 * time.Hour)
	far := h.now.Add(90 * 24 * time.Hour)
	id1, id2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	h.j.agents = fakeAgents{certs: []AgentCertificate{{ID: id1, Name: "k0s", NotAfter: &soon}, {ID: id2, Name: "other", NotAfter: &far}}}

	h.j.CheckCertificates(ctx)
	h.j.CheckCertificates(ctx)
	assert.Equal(t, 1, openGroups(h, errorJournal.KindLabCertExpiry))
	assert.EqualValues(t, 1, firstGroup(h, errorJournal.KindLabCertExpiry).Occurrences)
	assert.Equal(t, 1, h.tg.count("100"))

	// renewed: the certificate is far again
	h.j.agents = fakeAgents{certs: []AgentCertificate{{ID: id1, Name: "k0s", NotAfter: &far}}}
	h.j.CheckCertificates(ctx)
	assert.Equal(t, 0, openGroups(h, errorJournal.KindLabCertExpiry))
}

func firstGroup(h *harness, k errorJournal.Kind) errorJournal.Group {
	for _, g := range h.repo.groups {
		if g.Kind == k {
			return *g
		}
	}
	return errorJournal.Group{}
}

func TestAgentIsOfflineOnlyAfterTheThresholdAndOncePerOutage(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AgentOfflineAfter = 2 * time.Minute
	h := newHarness(cfg)
	id := uuid.Must(uuid.NewV7())
	cause := errors.New("connection refused")

	h.j.AgentLinkDown(ctx, id, "k0s", cause)
	h.now = h.now.Add(time.Minute)
	h.j.AgentLinkDown(ctx, id, "k0s", cause)
	h.j.handleQueued(t)
	assert.Equal(t, 0, openGroups(h, errorJournal.KindLabAgentOffline), "a short blip is not an outage")

	h.now = h.now.Add(90 * time.Second)
	h.j.AgentLinkDown(ctx, id, "k0s", cause)
	h.j.AgentLinkDown(ctx, id, "k0s", cause)
	h.j.handleQueued(t)
	assert.Equal(t, 1, openGroups(h, errorJournal.KindLabAgentOffline))
	assert.EqualValues(t, 1, firstGroup(h, errorJournal.KindLabAgentOffline).Occurrences)
	assert.Equal(t, 1, h.tg.count("100"))

	h.j.AgentLinkUp(ctx, id, "k0s")
	assert.Equal(t, 0, openGroups(h, errorJournal.KindLabAgentOffline))

	// the next outage reopens the same group and is told again
	h.now = h.now.Add(time.Hour)
	h.j.AgentLinkDown(ctx, id, "k0s", cause)
	h.now = h.now.Add(3 * time.Minute)
	h.j.AgentLinkDown(ctx, id, "k0s", cause)
	h.j.handleQueued(t)
	assert.Equal(t, 2, h.tg.count("100"))
}

// handleQueued processes what Report queued (the writer goroutine is not running in the test).
func (j *Journal) handleQueued(t *testing.T) {
	t.Helper()
	for {
		select {
		case e := <-j.queue:
			j.handle(ctx, e)
		default:
			return
		}
	}
}

func TestAgentReportMapsComponentsDeploysAndCertificate(t *testing.T) {
	h := newHarness(DefaultConfig())
	now := h.now
	soon := now.Add(48 * time.Hour)
	report := AgentErrors{
		Components: []AgentComponent{{Component: "operator", Instance: "op-1", Groups: []AgentErrorGroup{
			{Fingerprint: "ab12cd34", Kind: "reconcile", Normalized: "reconcile failed for <obj>", Count: 7, Last: now, Samples: []string{"reconcile failed for lab x"}},
			{Fingerprint: "zero", Count: 0},
		}}},
		DeployFailures: []AgentDeployFailure{{LabGroup: "g1", Lab: "l1", ReasonCode: "ImagePull", Device: "web", Message: "pull denied", At: now}},
		CertNotAfter:   &soon,
	}
	h.j.ReportAgentErrors(ctx, "k0s", report)
	h.j.handleQueued(t)

	comp := firstGroup(h, errorJournal.KindLabComponent)
	assert.EqualValues(t, 7, comp.Occurrences, "the agent's count is kept")
	assert.Equal(t, "k0s/operator", comp.Source, "the group names the agent")
	assert.Len(t, h.repo.groups, 3)
	deploy := firstGroup(h, errorJournal.KindLabDeploy)
	assert.Equal(t, "k0s", deploy.Source)
	assert.Contains(t, h.repo.samples[deploy.ID][0].Message, "ImagePull: pull denied")
	assert.Equal(t, "web", h.repo.samples[deploy.ID][0].Details["device"])
	assert.Equal(t, 1, openGroups(h, errorJournal.KindLabCertExpiry))

	// the same agent fingerprint on another agent is another group
	h.j.ReportAgentErrors(ctx, "other", AgentErrors{Components: report.Components[:1]})
	h.j.handleQueued(t)
	assert.Len(t, h.repo.groups, 4)

	// a later cert report far from the end resolves the warning
	far := now.Add(90 * 24 * time.Hour)
	h.j.ReportAgentErrors(ctx, "k0s", AgentErrors{CertNotAfter: &far})
	assert.Equal(t, 0, openGroups(h, errorJournal.KindLabCertExpiry))
}

type fakeRecords struct{ records []infraModel.AgentRecord }

func (f fakeRecords) ListRecords(context.Context) ([]infraModel.AgentRecord, error) {
	return f.records, nil
}

func TestCertificatesOfReadsTheRegistryWithoutCallingAgents(t *testing.T) {
	end := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	id := uuid.Must(uuid.NewV7())
	rec := infraModel.AgentRecord{AgentRegistration: infraModel.AgentRegistration{ID: id, Name: "k0s", CertNotAfter: &end}}
	certs, err := CertificatesOf(fakeRecords{records: []infraModel.AgentRecord{rec, {AgentRegistration: infraModel.AgentRegistration{Name: "env"}}}}).AgentCertificates(context.Background())
	require.NoError(t, err)
	require.Len(t, certs, 2)
	assert.Equal(t, AgentCertificate{ID: id, Name: "k0s", NotAfter: &end}, certs[0])
	assert.Nil(t, certs[1].NotAfter)
}
