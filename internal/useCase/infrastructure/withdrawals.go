package infrastructure

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
)

// WithdrawalNotifier tells the platform admins about a group of labs a cluster withdrew again and again (the inbox
// request router); it sends one request per group and withdrawal series.
type WithdrawalNotifier interface {
	LabWithdrawalsRepeated(ctx context.Context, a inboxUseCase.LabWithdrawalAlert) error
}

// SetWithdrawalNotifier wires the alerts after the use cases exist; nil sends none.
func (u *AgentsUseCase) SetWithdrawalNotifier(n WithdrawalNotifier) { u.withdrawals = n }

// ReportWithdrawals alerts the platform admins about the groups of labs the agent's cluster withdrew again and again
// (reported from its monitoring stream). A failure is logged, never raised: the stream goes on.
func (u *AgentsUseCase) ReportWithdrawals(ctx context.Context, agentID uuid.UUID, agentName string, withdrawals []labMonitoringModel.Withdrawal) {
	if u.withdrawals == nil {
		return
	}
	for _, w := range withdrawals {
		alert := inboxUseCase.LabWithdrawalAlert{AgentID: agentID, AgentName: agentName, Group: w.Group, Attempt: w.Attempt, Series: w.Series, At: u.now().UTC()}
		if err := u.withdrawals.LabWithdrawalsRepeated(ctx, alert); err != nil {
			log.Warn().Err(err).Str("agent", agentName).Str("group", w.Group).Msg("Failed to alert about a group of labs withdrawn again and again")
		}
	}
}
