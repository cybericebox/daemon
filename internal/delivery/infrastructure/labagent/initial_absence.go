package labagent

import (
	"context"
	"fmt"
	eventLab "github.com/cybericebox/daemon/internal/model/eventLab"
	pb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

// InitialDeploymentAbsent is a current authenticated catalog observation, never
// inferred from missing placement or a stale monitoring snapshot.
func (f *Fleet) InitialDeploymentAbsent(ctx context.Context, ref eventLab.Ref) (bool, error) {
	if ref.Group == "" || ref.Lab == "" {
		return false, fmt.Errorf("initial deployment identity unavailable")
	}
	members := f.Members()
	if len(members) == 0 {
		return false, fmt.Errorf("initial deployment owner unavailable")
	}
	if f.store != nil {
		id, found, e := f.store.Get(ctx, ref.Group)
		if e != nil {
			return false, e
		}
		if found {
			m := f.member(id)
			if m == nil {
				return false, fmt.Errorf("initial deployment owner unavailable")
			}
			members = []*Member{m}
		}
	}
	checked := 0
	for _, m := range members {
		if m == nil || m.Client == nil {
			return false, fmt.Errorf("initial deployment owner client unavailable")
		}
		features, e := m.Client.GetFeatures(ctx, &pb.Empty{})
		if e != nil {
			return false, e
		}
		if m.Tenant == "" || features.GetTenant() != m.Tenant {
			return false, fmt.Errorf("initial deployment tenant identity unavailable")
		}
		life := features.GetLifecycle()
		if !life.GetConfirmedRuntime() || !life.GetRetainedRestart() {
			return false, fmt.Errorf("initial deployment absence feature unavailable")
		}
		groups, e := m.Client.ListLabGroups(ctx, &pb.ListRequest{Items: []*pb.ItemRef{{Name: ref.Group}}})
		if e != nil {
			return false, e
		}
		checked++
		for _, g := range groups.GetItems() {
			if g.GetName() != ref.Group {
				return false, fmt.Errorf("initial deployment catalog identity changed")
			}
			labs, e := m.Client.ListLabs(ctx, &pb.ListRequest{Items: []*pb.ItemRef{{LabGroup: ref.Group, Name: ref.Lab}}})
			if e != nil {
				return false, e
			}
			for _, l := range labs.GetItems() {
				if l.GetName() == ref.Lab && l.GetLabGroupName() == ref.Group {
					return false, nil
				}
				return false, fmt.Errorf("initial deployment catalog identity changed")
			}
			return false, nil
		}
	}
	if checked == 0 {
		return false, fmt.Errorf("initial deployment absence owner unavailable")
	}
	return true, nil
}
