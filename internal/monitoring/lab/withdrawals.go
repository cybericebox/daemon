package lab

import (
	"context"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
)

// WithdrawalSink hears the groups of labs that reached labMonitoringModel.RepeatedWithdrawals in a monitoring message.
// It must not block for long.
type WithdrawalSink func(ctx context.Context, withdrawals []labMonitoringModel.Withdrawal)

// WithWithdrawalSink hands the groups of labs withdrawn again and again to sink.
func (r *Runner) WithWithdrawalSink(sink WithdrawalSink) *Runner {
	r.withdrawals = sink
	return r
}

// repeatedWithdrawals are the groups of the message at exactly their RepeatedWithdrawals-th withdrawal in a row and
// still waiting for the retry, once each, in message order.
func repeatedWithdrawals(update *labpb.MonitoringUpdate) []labMonitoringModel.Withdrawal {
	var out []labMonitoringModel.Withdrawal
	seen := map[string]bool{}
	add := func(group string, pods []*labpb.PodScheduling) {
		var w labMonitoringModel.Withdrawal
		for _, p := range pods {
			if p.GetWithdrawals() > w.Attempt {
				w.Attempt, w.Series = p.GetWithdrawals(), p.GetRetryAfterUnixMs()
			}
		}
		if w.Attempt != labMonitoringModel.RepeatedWithdrawals || w.Series <= 0 || seen[group] {
			return
		}
		seen[group] = true
		w.Group = group
		out = append(out, w)
	}
	for _, lab := range update.GetLabs() {
		group := "lab " + lab.GetLabGroupName() + "/" + lab.GetName()
		if key := lab.GetStatus().GetScheduling().GetGroup(); key != "" {
			group = "deploy group " + key
		}
		var pods []*labpb.PodScheduling
		for _, d := range lab.GetStatus().GetDevices() {
			pods = append(pods, d.GetScheduling())
		}
		add(group, pods)
	}
	for _, g := range update.GetGroups() {
		var pods []*labpb.PodScheduling
		for _, p := range g.GetStatus().GetPods() {
			pods = append(pods, p.GetScheduling())
		}
		add("lab group "+g.GetName(), pods)
	}
	return out
}
