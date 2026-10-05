package labagent

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// sweepSelector picks the groups the orphan sweep may judge: created by this platform instance and of
// a kind it owns. Groups without the instance label (another platform on the same cluster, or made by
// hand) never match. The agent adds the tenant of the client certificate on its own side, so a
// listing never reaches another tenant's groups whatever the selector says.
func (c *Client) sweepSelector() string {
	return fmt.Sprintf("%s=%s,%s in (%s,%s)", infraModel.LabelInstance, c.instance,
		infraModel.LabelKind, infraModel.KindStand, infraModel.KindTest)
}

// ListSweepableGroups lists the stand and test groups of this platform instance on every agent.
func (c *Client) ListSweepableGroups(ctx context.Context) ([]infraModel.LabGroupInfo, error) {
	list, err := c.ListLabGroups(ctx, &labpb.ListRequest{Selector: c.sweepSelector()})
	if err != nil {
		return nil, fmt.Errorf("list lab groups: %w", err)
	}
	out := make([]infraModel.LabGroupInfo, 0, len(list.GetItems()))
	for _, g := range list.GetItems() {
		info := infraModel.LabGroupInfo{Name: g.GetName(), Labels: g.GetLabels(), Phase: g.GetStatus().GetPhase()}
		if ms := g.GetCreatedUnixMs(); ms > 0 {
			info.CreatedAt = time.UnixMilli(ms)
		}
		out = append(out, info)
	}
	return out, nil
}

// ListSweepableGroups asks every agent; an agent that fails is logged and counted, not fatal, so one
// unreachable cluster neither hides the others nor causes a deletion.
func (f *Fleet) ListSweepableGroups(ctx context.Context) ([]infraModel.LabGroupInfo, int, error) {
	members := f.Members()
	if len(members) == 0 {
		return nil, 0, infraModel.ErrInfrastructureUnavailable.Err()
	}
	var (
		all    []infraModel.LabGroupInfo
		failed int
	)
	for _, m := range members {
		if m.Client == nil {
			failed++
			continue
		}
		groups, err := m.Client.ListSweepableGroups(ctx)
		if err != nil {
			failed++
			log.Warn().Err(err).Str("agent", m.Name).Msg("Lab group sweep: could not list the groups of an agent")
			continue
		}
		for i := range groups {
			groups[i].Agent = m.ID
		}
		all = append(all, groups...)
	}
	return all, failed, nil
}

// DestroyLabGroupOn deletes a group on the agent it was listed from, whatever the placement store says
// (an orphan may have no placement row), and forgets its placement.
func (f *Fleet) DestroyLabGroupOn(ctx context.Context, agent uuid.UUID, group string) error {
	m := f.member(agent)
	if m == nil || m.Client == nil {
		return fmt.Errorf("agent %s is not in the fleet", agent)
	}
	if err := m.Client.DestroyLabGroup(ctx, group); err != nil {
		return err
	}
	if f.store != nil && len(f.Members()) > 1 {
		return f.store.Release(ctx, group)
	}
	return nil
}
