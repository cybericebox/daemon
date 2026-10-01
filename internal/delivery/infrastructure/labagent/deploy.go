package labagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	labclient "github.com/cybericebox/laboratory/pkg/agent/client"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
)

// Conventional names within a deploy: the lab group name is the caller's deploy
// id, and the lab and VPN-client names are fixed so status/teardown reconstruct
// the whole handle from the group alone.
const (
	deployLabName   = "lab"
	deployVPNClient = "tester"
	// deployVariantID names the one variant a single-lab deploy sends.
	deployVariantID = "v"
)

// namespaceWait bounds the wait for the group namespace to be provisioned (the
// LabGroup reconciler creates it within a few seconds).
const (
	namespaceWait     = 30 * time.Second
	namespacePollTick = 500 * time.Millisecond
)

// errNotFound marks an object the agent does not know.
var errNotFound = errors.New("not found")

// requestLabels are the labels of every mutating call: the immutable platform-instance label.
func (c *Client) requestLabels() map[string]string {
	return map[string]string{infraModel.LabelInstance: c.instance}
}

// DeployLab creates the lab group, waits for its namespace, then creates the lab
// (spec + every device variable) inside it, plus a VPN client when the variant needs
// one. The lab is queued and provisioned asynchronously by the operator's scheduler;
// the caller polls LabStatus. The namespace wait is short (group namespace creation is
// fast), so this returns once the objects are created, not once the lab is Ready.
func (c *Client) DeployLab(ctx context.Context, group, lab string, meta infraModel.LabMeta, topo exerciseModel.Topology) error {
	if err := c.ensureGroup(ctx, group, meta.GroupLabels); err != nil {
		return err
	}
	if err := c.awaitGroupNamespace(ctx, group); err != nil {
		return err
	}
	specJSON, env, err := BuildLabSpec(topo)
	if err != nil {
		return fmt.Errorf("build lab spec: %w", err)
	}
	res, err := c.CreateLabs(ctx, &labpb.CreateLabsRequest{
		Labels:   c.requestLabels(),
		Variants: []*labpb.LabVariant{{VariantId: deployVariantID, SpecJson: specJSON}},
		Items: []*labpb.LabItem{{
			LabGroup: group, Name: lab, VariantId: deployVariantID, Env: env,
			Labels: meta.Labels, DeployGroup: meta.DeployGroup,
		}},
	})
	if err != nil {
		return agentErr("create lab", err)
	}
	if err = oneResult("create lab", res.GetResults()); err != nil {
		return err
	}
	if topo.VPN.Enabled {
		cl, clErr := c.CreateLabGroupClients(ctx, &labpb.CreateLabGroupClientsRequest{
			Labels: c.requestLabels(),
			Items:  []*labpb.LabGroupClientItem{{LabGroup: group, Name: deployVPNClient}},
		})
		if clErr != nil {
			return agentErr("create vpn client", clErr)
		}
		if len(cl.GetResults()) != 1 {
			return fmt.Errorf("create vpn client: %d results for 1 item", len(cl.GetResults()))
		}
		if err = oneResult("create vpn client", []*labpb.ItemResult{cl.GetResults()[0].GetResult()}); err != nil {
			return err
		}
	}
	return nil
}

// EnsureVPNGroup creates the stable team group without requiring a Lab.
// A later DeployLab reuses exactly this group name.
func (c *Client) EnsureVPNGroup(ctx context.Context, group string) error {
	return c.ensureGroup(ctx, group, nil)
}

// ensureGroup creates the group when it is missing; labels it lacks are added to an existing one.
func (c *Client) ensureGroup(ctx context.Context, group string, labels map[string]string) error {
	res, err := c.CreateLabGroups(ctx, &labpb.CreateLabGroupsRequest{
		Labels: c.requestLabels(),
		Items:  []*labpb.LabGroupItem{{Name: group, Labels: labels}},
	})
	if err != nil {
		return agentErr("create lab group", err)
	}
	return oneResult("create lab group", res.GetResults())
}

// getGroup reads one group; a group the agent does not know is errNotFound.
func (c *Client) getGroup(ctx context.Context, group string) (*labpb.LabGroup, error) {
	list, err := c.ListLabGroups(ctx, &labpb.ListRequest{Items: []*labpb.ItemRef{{Name: group}}})
	if err != nil {
		return nil, fmt.Errorf("get lab group: %w", err)
	}
	if len(list.GetItems()) == 0 {
		return nil, fmt.Errorf("get lab group %q: %w", group, errNotFound)
	}
	return list.GetItems()[0], nil
}

// GetVPNClientSubnet reads the operator's published subnet for the team's
// tunnel-only connection check. No public interface address is inferred.
func (c *Client) GetVPNClientSubnet(ctx context.Context, group string) (string, error) {
	g, err := c.getGroup(ctx, group)
	if err != nil {
		return "", fmt.Errorf("get lab group VPN status: %w", err)
	}
	if g.GetStatus().GetVpnClientSubnet() == "" {
		return "", fmt.Errorf("lab group %q has no VPN client subnet yet", group)
	}
	return g.GetStatus().GetVpnClientSubnet(), nil
}

// EnsureLabClient creates one named WireGuard client in an existing lab group and returns its
// config. Event runtime callers derive the name from a participant id, so no two people ever
// receive the shared test-deploy "tester" credential. The agent returns the private key only in
// the answer of the create call: a client that already exists cannot give its config again.
func (c *Client) EnsureLabClient(ctx context.Context, group, client string) (string, error) {
	if err := c.awaitGroupNamespace(ctx, group); err != nil {
		return "", err
	}
	res, err := c.CreateLabGroupClients(ctx, &labpb.CreateLabGroupClientsRequest{
		Labels: c.requestLabels(),
		Items:  []*labpb.LabGroupClientItem{{LabGroup: group, Name: client}},
	})
	if err != nil {
		return "", agentErr("create lab group client", err)
	}
	if len(res.GetResults()) != 1 {
		return "", fmt.Errorf("create lab group client: %d results for 1 item", len(res.GetResults()))
	}
	item := res.GetResults()[0]
	switch item.GetResult().GetState() {
	case labpb.ItemState_ITEM_STATE_CREATED:
		if item.GetClient().GetStatus().GetConfig() == "" {
			return "", fmt.Errorf("VPN client %q was created without a configuration", client)
		}
		return item.GetClient().GetStatus().GetConfig(), nil
	case labpb.ItemState_ITEM_STATE_EXISTS, labpb.ItemState_ITEM_STATE_UPDATED:
		return "", fmt.Errorf("VPN client %q already exists and its private configuration cannot be recovered", client)
	default:
		return "", oneResult("create lab group client", []*labpb.ItemResult{item.GetResult()})
	}
}

// LabClientHandshake is the time of a named client's last WireGuard handshake as the
// agent reports it; the zero time means the client never connected (or has no statistics yet).
func (c *Client) LabClientHandshake(ctx context.Context, group, client string) (time.Time, error) {
	list, err := c.ListLabGroupClients(ctx, &labpb.ListRequest{Items: []*labpb.ItemRef{{LabGroup: group, Name: client}}})
	if err != nil {
		return time.Time{}, fmt.Errorf("get lab group client: %w", err)
	}
	if len(list.GetItems()) == 0 {
		return time.Time{}, nil
	}
	unix := list.GetItems()[0].GetStatus().GetStatistics().GetLastHandshakeUnix()
	if unix <= 0 {
		return time.Time{}, nil
	}
	return time.Unix(unix, 0), nil
}

// DeleteLabClient removes one named client from a lab group. A missing group or
// client already is the desired state.
func (c *Client) DeleteLabClient(ctx context.Context, group, client string) error {
	res, err := c.DeleteLabGroupClients(ctx, &labpb.DeleteRequest{Items: []*labpb.ItemRef{{LabGroup: group, Name: client}}})
	if err != nil {
		return fmt.Errorf("delete lab group client: %w", err)
	}
	return oneResult("delete lab group client", res.GetResults())
}

// awaitGroupNamespace polls the group until its reconciler publishes the
// provisioned namespace, bounded by namespaceWait.
func (c *Client) awaitGroupNamespace(ctx context.Context, group string) error {
	deadline := time.NewTimer(namespaceWait)
	defer deadline.Stop()
	tick := time.NewTicker(namespacePollTick)
	defer tick.Stop()
	for {
		g, err := c.getGroup(ctx, group)
		if err != nil {
			return err
		}
		if g.GetStatus().GetNamespace() != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("lab group %q namespace not provisioned within %s", group, namespaceWait)
		case <-tick.C:
		}
	}
}

// LabStatus reports the deployed lab's runtime status. A group without a namespace yet reads as
// Provisioning rather than an error.
func (c *Client) LabStatus(ctx context.Context, group, lab string) (exerciseModel.LabDeployStatus, error) {
	g, err := c.getGroup(ctx, group)
	if err != nil {
		return exerciseModel.LabDeployStatus{}, err
	}
	if g.GetStatus().GetNamespace() == "" {
		return exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseProvisioning}, nil
	}
	list, err := c.ListLabs(ctx, &labpb.ListRequest{Items: []*labpb.ItemRef{{LabGroup: group, Name: lab}}})
	if err != nil {
		return exerciseModel.LabDeployStatus{}, fmt.Errorf("get lab: %w", err)
	}
	if len(list.GetItems()) == 0 {
		return exerciseModel.LabDeployStatus{}, fmt.Errorf("get lab %q of group %q: %w", lab, group, errNotFound)
	}
	status := mapLabStatus(list.GetItems()[0])
	status.GroupImageWarning = g.GetStatus().GetImageWarning()
	return status, nil
}

// DestroyLabGroup tears the whole deploy down: deleting the group cascades to its
// lab, VPN clients and namespace. A group that is already gone is the desired state.
func (c *Client) DestroyLabGroup(ctx context.Context, group string) error {
	res, err := c.DeleteLabGroups(ctx, &labpb.DeleteRequest{Items: []*labpb.ItemRef{{Name: group}}})
	if err != nil {
		return fmt.Errorf("delete lab group: %w", err)
	}
	return oneResult("delete lab group", res.GetResults())
}

// DeleteLab removes one Lab from its group and keeps the group (gateway and
// VPN clients). A missing group or Lab already is the desired state.
func (c *Client) DeleteLab(ctx context.Context, group, lab string) error {
	res, err := c.DeleteLabs(ctx, &labpb.DeleteRequest{Items: []*labpb.ItemRef{{LabGroup: group, Name: lab}}})
	if err != nil {
		return fmt.Errorf("delete lab: %w", err)
	}
	return oneResult("delete lab", res.GetResults())
}

// ReconcileLabGroupAccess replaces the complete group policy through the
// Laboratory agent. The group policy is default-deny, so callers must always
// pass the complete desired access snapshot.
func (c *Client) ReconcileLabGroupAccess(ctx context.Context, group string, policies []labAccessModel.ClientPolicy) error {
	rules := make([]*labpb.LabGroupAccessRule, 0, len(policies))
	for _, policy := range policies {
		// An empty LabNames list means "all Labs" in the operator protocol.
		// Omit the rule instead so the group's default-deny policy revokes the
		// client when no challenge Lab is currently available.
		if len(policy.AllowedLabs) == 0 {
			continue
		}
		rules = append(rules, &labpb.LabGroupAccessRule{
			Action:      labpb.LabGroupAccessAction_LAB_GROUP_ACCESS_ACTION_ALLOW,
			ClientNames: []string{policy.Name},
			LabNames:    append([]string(nil), policy.AllowedLabs...),
		})
	}
	res, err := c.SetLabGroupAccess(ctx, &labpb.SetLabGroupAccessRequest{
		Labels:   c.requestLabels(),
		Policies: []*labpb.LabGroupAccessPolicy{{LabGroupName: group, Rules: rules}},
	})
	if err != nil {
		return agentErr("replace lab group access policy", err)
	}
	return oneResult("replace lab group access policy", res.GetResults())
}

// SetLabGroupSuspended sets the group runtime state without deleting its
// configuration. The operator performs the corresponding scale-down or resume.
func (c *Client) SetLabGroupSuspended(ctx context.Context, group string, suspended bool) error {
	res, err := c.UpdateLabGroups(ctx, &labpb.UpdateLabGroupsRequest{
		Items: []*labpb.UpdateLabGroupItem{{Name: group, Changes: &labpb.LabGroupChanges{Suspended: &suspended}}},
	})
	if err != nil {
		return agentErr("set lab group suspended", err)
	}
	// A newly created team may not have requested any VPN group yet.
	if suspended && len(res.GetResults()) == 1 && res.GetResults()[0].GetState() == labpb.ItemState_ITEM_STATE_NOT_FOUND {
		return nil
	}
	return oneResult("set lab group suspended", res.GetResults())
}

// SetLabGroupVPNDisabled stops only the team's WireGuard server. The gateway
// and Labs keep their own lifecycle, including for non-VPN challenges.
func (c *Client) SetLabGroupVPNDisabled(ctx context.Context, group string, disabled bool) error {
	probe := !disabled
	res, err := c.UpdateLabGroups(ctx, &labpb.UpdateLabGroupsRequest{
		Items: []*labpb.UpdateLabGroupItem{{Name: group, Changes: &labpb.LabGroupChanges{VpnDisabled: &disabled, ProbeWhileSuspended: &probe}}},
	})
	if err != nil {
		return agentErr("set lab group VPN disabled", err)
	}
	if disabled && len(res.GetResults()) == 1 && res.GetResults()[0].GetState() == labpb.ItemState_ITEM_STATE_NOT_FOUND {
		return nil
	}
	return oneResult("set lab group VPN disabled", res.GetResults())
}

// ResetDevice discards the snapshots of one device and restarts it from its base image.
func (c *Client) ResetDevice(ctx context.Context, group, lab, device string) error {
	res, err := c.ResetDevices(ctx, &labpb.DevicesRequest{Items: []*labpb.ItemRef{{LabGroup: group, Lab: lab, Name: device}}})
	if err != nil {
		return deviceActionErr("reset device", err)
	}
	return deviceResult("reset device", res.GetResults())
}

// RescueDevice switches one device to rescue mode (a shell from its latest snapshot) or back.
func (c *Client) RescueDevice(ctx context.Context, group, lab, device string, enable bool) error {
	res, err := c.RescueDevices(ctx, &labpb.RescueDevicesRequest{
		Devices: &labpb.DevicesRequest{Items: []*labpb.ItemRef{{LabGroup: group, Lab: lab, Name: device}}},
		Enable:  enable,
	})
	if err != nil {
		return deviceActionErr("rescue device", err)
	}
	return deviceResult("rescue device", res.GetResults())
}

// PrewarmImages asks the platform image cache to fetch the images from their upstream registries
// before labs need them. It is asynchronous and idempotent: every call returns the current state of
// each image, so repeating it polls.
func (c *Client) PrewarmImages(ctx context.Context, images []string) ([]infraModel.ImagePrewarm, error) {
	res, err := c.Client.PrewarmImages(ctx, &labpb.PrewarmImagesRequest{Images: images})
	if err != nil {
		return nil, agentErr("prewarm images", err)
	}
	out := make([]infraModel.ImagePrewarm, 0, len(res.GetImages()))
	for _, image := range res.GetImages() {
		out = append(out, infraModel.ImagePrewarm{Image: image.GetImage(), State: prewarmState(image.GetState()), Error: image.GetError(), Digest: image.GetDigest()})
	}
	return out, nil
}

func prewarmState(s labpb.PrewarmState) string {
	switch s {
	case labpb.PrewarmState_PREWARM_STATE_QUEUED:
		return infraModel.PrewarmQueued
	case labpb.PrewarmState_PREWARM_STATE_WARMING:
		return infraModel.PrewarmWarming
	case labpb.PrewarmState_PREWARM_STATE_DONE:
		return infraModel.PrewarmDone
	case labpb.PrewarmState_PREWARM_STATE_SKIPPED:
		return infraModel.PrewarmSkipped
	default:
		return infraModel.PrewarmFailed
	}
}

// oneResult reads the answer of a single-item mutation: CREATED, EXISTS, UPDATED, DELETED and
// NOT_FOUND are all the desired state of the callers here, FAILED is an error. A failure the
// agent marks retryable (the object is still being deleted, the namespace is not ready) becomes
// a TerminatingError so workers retry instead of recording a failure.
func oneResult(op string, results []*labpb.ItemResult) error {
	if len(results) != 1 {
		return fmt.Errorf("%s: %d results for 1 item", op, len(results))
	}
	r := results[0]
	if r.GetState() != labpb.ItemState_ITEM_STATE_FAILED {
		return nil
	}
	if r.GetRetryable() {
		return &infraModel.TerminatingError{
			Message:    fmt.Sprintf("%s: %s", op, r.GetError()),
			RetryAfter: infraModel.TerminatingRetryAfter,
			Err:        errors.New(r.GetError()),
		}
	}
	return fmt.Errorf("%s: %s", op, r.GetError())
}

// deviceResult maps the per-item answer of a device call onto API errors: NOT_FOUND is a missing
// lab or device, a FAILED item that says the device has no state persistence is that, and a
// retryable one is transient.
func deviceResult(op string, results []*labpb.ItemResult) error {
	if len(results) != 1 {
		return fmt.Errorf("%s: %d results for 1 item", op, len(results))
	}
	r := results[0]
	switch {
	case r.GetState() == labpb.ItemState_ITEM_STATE_NOT_FOUND:
		return infraModel.ErrDeviceNotFound.WithError(errors.New(r.GetError())).Err()
	case r.GetState() != labpb.ItemState_ITEM_STATE_FAILED:
		return nil
	case r.GetRetryable():
		return infraModel.ErrDeviceActionRetry.WithError(errors.New(r.GetError())).Err()
	case strings.Contains(r.GetError(), "no state persistence"):
		return infraModel.ErrDeviceNotPersistent.WithError(errors.New(r.GetError())).Err()
	default:
		return fmt.Errorf("%s: %s", op, r.GetError())
	}
}

// deviceActionErr maps a whole-call failure of a device call.
func deviceActionErr(op string, err error) error {
	if labclient.IsTerminating(err) {
		return infraModel.ErrDeviceActionRetry.WithError(err).Err()
	}
	return fmt.Errorf("%s: %w", op, err)
}

// mapLabStatus projects the agent's wire Lab status onto the domain view.
func mapLabStatus(l *labpb.Lab) exerciseModel.LabDeployStatus {
	st := l.GetStatus()
	if st == nil {
		return exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhasePending}
	}
	out := exerciseModel.LabDeployStatus{
		Phase:        st.GetPhase(),
		Ready:        st.GetReady(),
		VPNCIDR:      st.GetVpnCidr(),
		InternetCIDR: st.GetInternetCidr(),
		ImageWarning: st.GetImageWarning(),
	}
	if q := st.GetScheduling(); q != nil {
		out.Queue = &exerciseModel.LabQueue{
			Position: q.GetPosition(), Length: q.GetLength(), Reason: q.GetReason(), Message: q.GetMessage(),
			Group: q.GetGroup(), Pods: q.GetPods(), Pending: q.GetPending(),
		}
	}
	for _, d := range st.GetDevices() {
		if d.GetUsageAvailable() {
			out.UsageAvailable = true
			out.CPUMillicores += d.GetCpuMillicores()
			out.MemoryBytes += d.GetMemoryBytes()
		}
		device := exerciseModel.LabDeployedDevice{Name: d.GetName(), Ready: d.GetReady(), Reason: d.GetPodReason()}
		if sn := d.GetSnapshot(); sn != nil {
			device.Snapshot = &exerciseModel.DeviceSnapshot{
				LastSnapshotAt: unixMilli(sn.GetLastSnapshotUnixMs()), RestoredAt: unixMilli(sn.GetRestoredUnixMs()),
				SizeBytes: sn.GetSizeBytes(), Warning: sn.GetWarning(), Rescue: sn.GetRescue(),
			}
		}
		device.Scheduling = mapPodScheduling(d.GetScheduling())
		out.Devices = append(out.Devices, device)
	}
	for _, a := range st.GetAccess() {
		out.Access = append(out.Access, exerciseModel.LabAccess{
			Device: a.GetDevice(), Port: a.GetPort(), Protocol: a.GetProtocol(), URL: a.GetUrl(),
		})
	}
	return out
}

// mapPodScheduling maps the scheduler state of one device pod; nil when the scheduler does not track it.
func mapPodScheduling(p *labpb.PodScheduling) *exerciseModel.PodScheduling {
	if p == nil {
		return nil
	}
	out := &exerciseModel.PodScheduling{
		State: podState(p.GetState()), QueuedAt: unixMilli(p.GetQueuedUnixMs()),
		DispatchedAt: unixMilli(p.GetDispatchedUnixMs()), StartedAt: unixMilli(p.GetStartedUnixMs()),
	}
	if f := p.GetFailure(); f != nil {
		out.Failure = &exerciseModel.PodFailure{Reason: f.GetReason(), Message: f.GetMessage(), RestartCount: f.GetRestartCount(), At: unixMilli(f.GetAtUnixMs())}
	}
	return out
}

func podState(s labpb.PodState) string {
	switch s {
	case labpb.PodState_POD_STATE_QUEUED:
		return exerciseModel.PodStateQueued
	case labpb.PodState_POD_STATE_STARTING:
		return exerciseModel.PodStateStarting
	case labpb.PodState_POD_STATE_STARTED:
		return exerciseModel.PodStateStarted
	case labpb.PodState_POD_STATE_FAILED:
		return exerciseModel.PodStateFailed
	default:
		return ""
	}
}

// agentErr wraps an agent error with its operation. The agent's retryable
// "still being deleted" refusal becomes a domain TerminatingError so workers
// retry instead of recording a failure.
func agentErr(op string, err error) error {
	if labclient.IsTerminating(err) {
		return &infraModel.TerminatingError{
			Message:    fmt.Sprintf("%s: %s", op, err),
			RetryAfter: infraModel.TerminatingRetryAfter,
			Err:        err,
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

// unixMilli is the zero time for 0 (the agent's "never"/"not yet").
func unixMilli(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
