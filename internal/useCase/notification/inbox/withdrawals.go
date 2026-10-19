package inboxUseCase

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gofrs/uuid"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
)

// LabWithdrawalAlert is a group of labs a lab cluster withdrew again and again: not every pod of it got a node within
// the node timeout. Series is the retry time of the group's withdrawal that crossed the threshold (unix ms): the same for
// every lab of the group, so the series is reported once whichever lab is seen first.
type LabWithdrawalAlert struct {
	AgentID   uuid.UUID
	AgentName string
	// Group is the group of labs started together: the deploy group, or the lab or lab group itself.
	Group   string
	Attempt int32
	Series  int64
	At      time.Time
}

// LabWithdrawalsRepeated asks every platform admin to look at the cluster capacity, once per group and withdrawal
// series: a series already reported (open or closed by an admin) is not sent again.
func (r *RequestRouter) LabWithdrawalsRepeated(ctx context.Context, a LabWithdrawalAlert) error {
	ref := inboxModel.LabWithdrawalRef(a.AgentID, a.Group, a.Series)
	sent, err := r.inbox.SubjectExists(ctx, ref)
	if err != nil {
		return fmt.Errorf("inbox requests: check the withdrawal alert: %w", err)
	}
	if sent {
		return nil
	}
	admins, err := r.queries.ListPlatformAdminUserIDs(ctx)
	if err != nil {
		return fmt.Errorf("inbox requests: list platform admins: %w", err)
	}
	vars := map[string]any{"agent_name": a.AgentName, "group": a.Group, "attempt": strconv.Itoa(int(a.Attempt)), "admin_url": r.adminURL}
	meta := inboxModel.NewMeta(inboxModel.TypeLabWithdrawalsRepeated, inboxModel.RoleAdmin, ref).Raised(a.At)
	return r.fanOut(ctx, admins, inboxModel.TypeLabWithdrawalsRepeated, vars, meta)
}
