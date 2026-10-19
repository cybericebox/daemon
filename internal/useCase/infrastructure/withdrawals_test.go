package infrastructure

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"

	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
)

type fakeWithdrawalNotifier struct {
	sent []inboxUseCase.LabWithdrawalAlert
	err  error
}

func (f *fakeWithdrawalNotifier) LabWithdrawalsRepeated(_ context.Context, a inboxUseCase.LabWithdrawalAlert) error {
	f.sent = append(f.sent, a)
	return f.err
}

// Every group withdrawn again and again goes to the notifier with the agent and the time; a failed alert does not stop
// the others.
func TestReportWithdrawalsAlertsPerGroup(t *testing.T) {
	f := newAgentsFixture(t, nil)
	notifier := &fakeWithdrawalNotifier{err: errors.New("down")}
	f.uc.SetWithdrawalNotifier(notifier)
	agent := uuid.Must(uuid.NewV7())
	f.uc.ReportWithdrawals(context.Background(), agent, "main", []labMonitoringModel.Withdrawal{
		{Group: "deploy group t-1", Attempt: 3, Series: 1000},
		{Group: "lab group team-1", Attempt: 3, Series: 2000},
	})
	if len(notifier.sent) != 2 {
		t.Fatalf("sent %+v", notifier.sent)
	}
	got := notifier.sent[1]
	if got.AgentID != agent || got.AgentName != "main" || got.Group != "lab group team-1" || got.Attempt != 3 || got.Series != 2000 || !got.At.Equal(f.now) {
		t.Fatalf("alert %+v", got)
	}
	// Without a notifier nothing happens.
	NewAgentsUseCase(AgentsDependencies{}).ReportWithdrawals(context.Background(), agent, "main", []labMonitoringModel.Withdrawal{{Group: "x", Attempt: 3, Series: 1}})
}
