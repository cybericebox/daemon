package labagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	labclient "github.com/cybericebox/laboratory/pkg/agent/client"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

// deployVariantID names the one variant a single-lab deploy sends. A deploy
// creates no VPN client: callers ask for their own with EnsureLabClient.
const deployVariantID = "v"

// namespaceWait bounds the wait for the group namespace to be provisioned (the
// LabGroup reconciler creates it within a few seconds).
const (
	namespaceWait     = 30 * time.Second
	namespacePollTick = 500 * time.Millisecond
)

// errNotFound marks an object the agent does not know.
var errNotFound = infraModel.ErrAgentObjectNotFound

// requestLabels are the labels of every mutating call: the immutable platform-instance label.
func (c *Client) requestLabels() map[string]string {
	return map[string]string{infraModel.LabelInstance: c.instance}
}

// DeployLab creates the lab group, waits for its namespace, then creates the lab
// (spec + every device variable) inside it, plus a VPN client when the variant needs
// one. The lab is queued and provisioned asynchronously by the operator's scheduler;
// the caller polls LabStatus. The namespace wait is short (group namespace creation is
// fast), so this returns once the objects are created, not once the lab is Ready.
func (c *Client) DeployLab(
	ctx context.Context,
	group, lab string,
	meta infraModel.LabMeta,
	topo exerciseModel.Topology,
) error {
	if meta.InitialLifecycle != nil &&
		(meta.InitialLifecycle.OperationID == uuid.Nil || meta.InitialLifecycle.Revision != 1 || meta.BeforeCreate == nil) {
		return fmt.Errorf("managed initial lifecycle requires a persisted operation and revision1")
	}
	if err := c.ensureGroup(ctx, group, meta.GroupLabels, meta.GroupPartID); err != nil {
		return err
	}
	if err := c.awaitGroupNamespace(ctx, group); err != nil {
		return err
	}
	build, err := BuildLabFor(topo, meta.Checks, meta.AgentEnforcesVPNReverse)
	if err != nil {
		return fmt.Errorf("build lab spec: %w", err)
	}
	specJSON := build.SpecJSON
	if meta.InitialLifecycle != nil {
		var spec labSpec
		if err = json.Unmarshal(specJSON, &spec); err != nil {
			return err
		}
		spec.Lifecycle = &labInitialLifecycle{
			DesiredState: "Running",
			OperationID:  meta.InitialLifecycle.OperationID.String(),
			Revision:     meta.InitialLifecycle.Revision,
		}
		if specJSON, err = json.Marshal(spec); err != nil {
			return err
		}
	}
	expectedGroupUID := ""
	if meta.InitialLifecycle != nil {
		g, err := c.getGroup(ctx, group)
		if err != nil {
			return err
		}
		if g.GetUid() == "" {
			return fmt.Errorf("managed create group identity unavailable")
		}
		expectedGroupUID = g.GetUid()
		hash, err := labclient.CreationDefinitionHash(specJSON, meta.DeployGroup, nil)
		if err != nil {
			return err
		}
		if err = meta.BeforeCreate(
			ctx,
			infraModel.LabCreateDispatch{GroupUID: expectedGroupUID, DefinitionHash: hash},
		); err != nil {
			return err
		}
	}
	res, err := c.CreateLabs(ctx, &labpb.CreateLabsRequest{
		Labels: c.requestLabels(),
		Variants: []*labpb.LabVariant{
			{
				VariantId:           deployVariantID,
				SpecJson:            specJSON,
				RegistryCredentials: registryCredentials(meta.PullCredentials),
			},
		},
		Items: []*labpb.LabItem{
			{
				ExpectedGroupUid: expectedGroupUID,
				LabGroup:         group,
				Name:             lab,
				VariantId:        deployVariantID,
				Env:              build.Env,
				Bootstrap:        build.Bootstrap,
				Checks:           build.Checks,
				Labels: withVPNReverseLabel(
					meta.Labels,
					topo,
					build.ReverseSent,
				),
				DeployGroup:   meta.DeployGroup,
				ReservationId: meta.PartID,
			},
		},
	})
	if err != nil {
		return agentErr("create lab", err)
	}
	if err = oneResult("create lab", res.GetResults()); err != nil {
		return err
	}
	return nil
}

// wantsVPNReverse reports whether some VPN gateway of the topology asks to allow connections from the lab to the VPN clients.
func wantsVPNReverse(t exerciseModel.Topology) bool {
	for _, d := range t.Devices {
		if d.Type == exerciseModel.DeviceTypeVPNGateway && d.ReverseConnections {
			return true
		}
	}
	return false
}

// vpnReverseOf derives what is in force for connections the lab starts to the VPN clients through one VPN gateway. "blocked" is
// reported only when the laboratory says so (status.gateways[].lab_initiated_blocked: the switch is off and the group's VPN
// gateway has really applied the block). The key was sent: allowed. Not sent and confirmed: blocked. Not sent and not confirmed
// (an older agent, a VPN pod not rolled yet, not applied yet): not_enforced, except an older agent whose lab the exercise wanted
// open, which allows everything: allowed. There is no separate "pending" state. Empty when the lab carries no label for the
// gateway.
func vpnReverseOf(labels map[string]string, gateway string, confirmedBlocked, agentEnforces bool) string {
	wanted, ok := labels[infraModel.LabelVPNReversePrefix+gateway]
	if !ok {
		return ""
	}
	switch {
	case labels[infraModel.LabelVPNReverseSentPrefix+gateway] == "1":
		return exerciseModel.VPNReverseAllowed
	case confirmedBlocked:
		return exerciseModel.VPNReverseBlocked
	case wanted == "1" && !agentEnforces:
		return exerciseModel.VPNReverseAllowed
	}
	return exerciseModel.VPNReverseNotEnforced
}

// withVPNReverseLabel adds to the lab's labels what its exercise asks of connections to the VPN clients, one pair per VPN gateway
// (sent holds the gateways whose spec carries the key that allows them).
func withVPNReverseLabel(
	labels map[string]string,
	topo exerciseModel.Topology,
	sent map[string]bool,
) map[string]string {
	var out map[string]string
	for _, d := range topo.Devices {
		if d.Type != exerciseModel.DeviceTypeVPNGateway {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(labels)+2)
			for k, v := range labels {
				out[k] = v
			}
		}
		out[infraModel.LabelVPNReversePrefix+d.Name], out[infraModel.LabelVPNReverseSentPrefix+d.Name] = "0", "0"
		if d.ReverseConnections {
			out[infraModel.LabelVPNReversePrefix+d.Name] = "1"
		}
		if sent[d.Name] {
			out[infraModel.LabelVPNReverseSentPrefix+d.Name] = "1"
		}
	}
	if out == nil {
		return labels
	}
	return out
}

// RunLabChecks asks the laboratory to run the given checks of a lab now (all that allow a manual run when ids is empty).
// The answer says only that the request was recorded for the checker; the results come back in the Monitoring stream. A
// laboratory with the checks option off refuses the call as a whole.
func (c *Client) RunLabChecks(ctx context.Context, group, lab string, ids []string) error {
	res, err := c.RunChecks(
		ctx,
		&labpb.RunChecksRequest{
			Items: []*labpb.RunChecksItem{{LabGroup: group, Lab: lab, CheckIds: ids}},
		},
	)
	if err != nil {
		if status.Code(err) == codes.ResourceExhausted {
			// The laboratory's cooldown between manual runs: the same answer as our own rate limit, with the wait it gives.
			return tooOften(cooldownOf(status.Convert(err)))
		}
		if status.Code(err) == codes.FailedPrecondition {
			return exerciseModel.ErrLabOptionUnavailable.WithPublicContext("options", "checks").
				WithError(err).
				Err()
		}
		return agentErr("run checks", err)
	}
	// In a batch the status details are lost: the cooldown comes as a failed item with the reason COOLDOWN and its retry_after.
	if len(res.GetResults()) == 1 && res.GetResults()[0].GetReason() == labclient.ReasonCooldown {
		item := res.GetResults()[0]
		if item.GetRetryAfter() != nil {
			return tooOften(secondsUp(item.GetRetryAfter().AsDuration()), true)
		}
		return tooOften(textCooldownSeconds(item.GetError()))
	}
	if len(res.GetResults()) == 1 &&
		res.GetResults()[0].GetState() == labpb.ItemState_ITEM_STATE_NOT_FOUND {
		return fmt.Errorf("run checks: %w", errNotFound)
	}
	return oneResult("run checks", res.GetResults())
}

// tooOften is our "asked too often" answer, with the wait when it is known.
func tooOften(wait int64, known bool) error {
	failure := exerciseModel.ErrChecksRunTooOften
	if known {
		failure = failure.WithDetail(appErr.DetailRetryAfterSeconds, wait)
	}
	return failure.Err()
}

// secondsUp is a wait in whole seconds, rounded up (at least one).
func secondsUp(d time.Duration) int64 { return max(1, int64((d+time.Second-1)/time.Second)) }

// cooldownOf reads the wait of a RESOURCE_EXHAUSTED answer: the standard RetryInfo detail first, and the text the first agents
// sent ("wait N seconds before ...") only for an agent that sends no detail.
func cooldownOf(s *status.Status) (int64, bool) {
	for _, detail := range s.Details() {
		if info, ok := detail.(*errdetails.RetryInfo); ok && info.GetRetryDelay() != nil {
			return secondsUp(info.GetRetryDelay().AsDuration()), true
		}
	}
	return textCooldownSeconds(s.Message())
}

var cooldownRe = regexp.MustCompile(`wait (\d+) seconds`)

// textCooldownSeconds reads the wait the laboratory names in its cooldown text ("wait N seconds before the next manual run").
func textCooldownSeconds(message string) (int64, bool) {
	match := cooldownRe.FindStringSubmatch(message)
	if match == nil {
		return 0, false
	}
	seconds, err := strconv.ParseInt(match[1], 10, 64)
	return seconds, err == nil && seconds > 0
}

// EnsureVPNGroup creates the stable team group without requiring a Lab.
// A later DeployLab reuses exactly this group name.
func (c *Client) EnsureVPNGroup(ctx context.Context, group string) error {
	return c.ensureGroup(ctx, group, nil, "")
}

// EnsureVPNGroupUnder is EnsureVPNGroup for a group that runs under a reservation (its part id).
func (c *Client) EnsureVPNGroupUnder(ctx context.Context, group, reservationID string) error {
	return c.ensureGroup(ctx, group, nil, reservationID)
}

// groupItem is one group to create. The sizes of its own pods (the VPN by the group's users, the gateway by
// its internet labs; computed with this agent's formula and carried in ctx by the fleet) are passed
// explicitly here, so the pods are created at exactly the size the backend planned and reserved.
func groupItem(
	ctx context.Context,
	group string,
	labels map[string]string,
	reservationID string,
) *labpb.LabGroupItem {
	item := &labpb.LabGroupItem{Name: group, Labels: labels, ReservationId: reservationID}
	if sizes, ok := infraModel.GroupSizesFrom(ctx); ok {
		setGroupSizes(item, sizes)
	}
	return item
}

// ensureGroup creates the group when it is missing; labels it lacks are added to an existing one.
func (c *Client) ensureGroup(
	ctx context.Context,
	group string,
	labels map[string]string,
	reservationID string,
) error {
	res, err := c.CreateLabGroups(ctx, &labpb.CreateLabGroupsRequest{
		Labels: c.requestLabels(),
		Items:  []*labpb.LabGroupItem{groupItem(ctx, group, labels, reservationID)},
	})
	if err != nil {
		return agentErr("create lab group", err)
	}
	err = oneResult("create lab group", res.GetResults())
	if err == nil || !strings.Contains(err.Error(), groupSpecDiffers) {
		return err
	}
	// The group exists with another spec: its sizes were planned when it was created, and its
	// suspended / VPN-disabled flags are owned by the access sync (SetLabGroup*), so a create
	// request cannot match them and never will. Adopt it as it is, instead of failing forever.
	existing, getErr := c.getGroup(ctx, group)
	if getErr != nil {
		return err
	}

	if sizes, ok := infraModel.GroupSizesFrom(
		ctx,
	); ok &&
		sizes.Total() != (resourcesModel.Amount{}) {
		projected, hasProjection := any(existing).(interface {
			GetVpnSize() *labpb.PodSize
			GetGatewaySize() *labpb.PodSize
		})
		if !hasProjection || existing.GetUid() == "" || projected.GetVpnSize() == nil ||
			projected.GetGatewaySize() == nil {
			return fmt.Errorf("existing laboratory group sizes are unavailable: %w", err)
		}
		vpn, gateway := projected.GetVpnSize(), projected.GetGatewaySize()
		actual := infraModel.GroupSizes{
			VPN: resourcesModel.Amount{
				CPUMillicores: vpn.GetCpuMillicores(),
				MemoryBytes:   vpn.GetMemoryBytes(),
			},
			Gateway: resourcesModel.Amount{
				CPUMillicores: gateway.GetCpuMillicores(),
				MemoryBytes:   gateway.GetMemoryBytes(),
			},
		}
		if actual.VPN.CPUMillicores <= 0 || actual.VPN.MemoryBytes <= 0 ||
			actual.Gateway.CPUMillicores <= 0 ||
			actual.Gateway.MemoryBytes <= 0 ||
			!actual.Holds(sizes) {
			return fmt.Errorf("existing laboratory group is undersized: %w", err)
		}
	}
	return nil
}

// groupSpecDiffers is the agent's answer to a create of a group that exists with another spec.
const groupSpecDiffers = "already exists with a different spec"

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

// LabGroupExists says the agent still knows the group: a group being deleted still exists until its
// finalizers are done (its namespace is gone with it).
func (c *Client) LabGroupExists(ctx context.Context, group string) (bool, error) {
	if _, err := c.getGroup(ctx, group); err != nil {
		if errors.Is(err, errNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// LabExists says the agent still has the Lab; a missing group means it has not.
func (c *Client) LabExists(ctx context.Context, group, lab string) (bool, error) {
	exists, err := c.LabGroupExists(ctx, group)
	if err != nil || !exists {
		return false, err
	}
	list, err := c.ListLabs(
		ctx,
		&labpb.ListRequest{Items: []*labpb.ItemRef{{LabGroup: group, Name: lab}}},
	)
	if err != nil {
		return false, fmt.Errorf("get lab: %w", err)
	}
	return len(list.GetItems()) > 0, nil
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
// config. Callers derive the name from a participant or author id, so no two people ever
// share a credential. The agent returns the private key only in
// the answer of the create call: a client that already exists cannot give its config again.
//
// A client that already exists (its config was lost before the caller stored it) is deleted and
// created again, so its new private key is the one returned. The caller serializes calls per client.
func (c *Client) EnsureLabClient(ctx context.Context, group, client string) (string, error) {
	if err := c.awaitGroupNamespace(ctx, group); err != nil {
		return "", err
	}
	config, exists, err := c.createLabClient(ctx, group, client)
	if err != nil || !exists {
		return config, err
	}
	if err = c.DeleteLabClient(ctx, group, client); err != nil {
		return "", err
	}
	config, exists, err = c.createLabClient(ctx, group, client)
	if err == nil && exists {
		err = fmt.Errorf("VPN client %q still exists after it was deleted", client)
	}
	return config, err
}

// createLabClient creates the client once; exists says it was already there and gave no config.
func (c *Client) createLabClient(
	ctx context.Context,
	group, client string,
) (config string, exists bool, err error) {
	res, err := c.CreateLabGroupClients(ctx, &labpb.CreateLabGroupClientsRequest{
		Labels: c.requestLabels(),
		Items:  []*labpb.LabGroupClientItem{{LabGroup: group, Name: client}},
	})
	if err != nil {
		return "", false, agentErr("create lab group client", err)
	}
	if len(res.GetResults()) != 1 {
		return "", false, fmt.Errorf(
			"create lab group client: %d results for 1 item",
			len(res.GetResults()),
		)
	}
	item := res.GetResults()[0]
	switch item.GetResult().GetState() {
	case labpb.ItemState_ITEM_STATE_CREATED:
		if item.GetClient().GetStatus().GetConfig() == "" {
			return "", false, fmt.Errorf(
				"VPN client %q was created without a configuration",
				client,
			)
		}
		return item.GetClient().GetStatus().GetConfig(), false, nil
	case labpb.ItemState_ITEM_STATE_EXISTS, labpb.ItemState_ITEM_STATE_UPDATED:
		return "", true, nil
	default:
		return "", false, oneResult(
			"create lab group client",
			[]*labpb.ItemResult{item.GetResult()},
		)
	}
}

// LabClientHandshake is the time of a named client's last WireGuard handshake as the
// agent reports it; the zero time means the client never connected (or has no statistics yet).
func (c *Client) LabClientHandshake(ctx context.Context, group, client string) (time.Time, error) {
	list, err := c.ListLabGroupClients(
		ctx,
		&labpb.ListRequest{Items: []*labpb.ItemRef{{LabGroup: group, Name: client}}},
	)
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
	res, err := c.DeleteLabGroupClients(
		ctx,
		&labpb.DeleteRequest{Items: []*labpb.ItemRef{{LabGroup: group, Name: client}}},
	)
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
			return fmt.Errorf(
				"lab group %q namespace not provisioned within %s",
				group,
				namespaceWait,
			)
		case <-tick.C:
		}
	}
}

// LabStatus reports the deployed lab's runtime status. A group without a namespace yet reads as
// Provisioning rather than an error.
func (c *Client) LabStatus(
	ctx context.Context,
	group, lab string,
) (exerciseModel.LabDeployStatus, error) {
	return c.labStatus(ctx, group, lab, false)
}

// labStatus is LabStatus for an agent that does (agentEnforces) or does not enforce the block of connections the lab starts.
func (c *Client) labStatus(
	ctx context.Context,
	group, lab string,
	agentEnforces bool,
) (exerciseModel.LabDeployStatus, error) {
	g, err := c.getGroup(ctx, group)
	if err != nil {
		return exerciseModel.LabDeployStatus{}, err
	}
	if g.GetStatus().GetNamespace() == "" {
		return exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseProvisioning}, nil
	}
	list, err := c.ListLabs(
		ctx,
		&labpb.ListRequest{Items: []*labpb.ItemRef{{LabGroup: group, Name: lab}}},
	)
	if err != nil {
		return exerciseModel.LabDeployStatus{}, fmt.Errorf("get lab: %w", err)
	}
	if len(list.GetItems()) == 0 {
		return exerciseModel.LabDeployStatus{}, fmt.Errorf(
			"get lab %q of group %q: %w",
			lab,
			group,
			errNotFound,
		)
	}
	status := mapLabStatus(list.GetItems()[0])
	for i := range status.Gateways {
		g := &status.Gateways[i]
		if g.Kind != "vpn" {
			continue
		}
		labels := list.GetItems()[0].GetLabels()
		g.ReverseConnections = labels[infraModel.LabelVPNReversePrefix+g.Name] == "1"
		g.Reverse = vpnReverseOf(labels, g.Name, g.LabInitiatedBlocked, agentEnforces)
	}
	status.GroupImageWarning = g.GetStatus().GetImageWarning()
	return status, nil
}

// ListGroupLabs names the Labs of one group. A group the agent does not know has none.
func (c *Client) ListGroupLabs(ctx context.Context, group string) ([]string, error) {
	list, err := c.ListLabs(ctx, &labpb.ListRequest{LabGroup: group})
	if err != nil {
		return nil, fmt.Errorf("list labs of group %q: %w", group, err)
	}
	out := make([]string, 0, len(list.GetItems()))
	for _, l := range list.GetItems() {
		out = append(out, l.GetName())
	}
	return out, nil
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
	res, err := c.DeleteLabs(
		ctx,
		&labpb.DeleteRequest{Items: []*labpb.ItemRef{{LabGroup: group, Name: lab}}},
	)
	if err != nil {
		return fmt.Errorf("delete lab: %w", err)
	}
	return oneResult("delete lab", res.GetResults())
}

// ReconcileLabGroupAccess replaces the complete group policy through the
// Laboratory agent. The group policy is default-deny, so callers must always
// pass the complete desired access snapshot.
func (c *Client) ReconcileLabGroupAccess(
	ctx context.Context,
	group string,
	policies []labAccessModel.ClientPolicy,
) error {
	return c.reconcileLabGroupAccess(ctx, group, policies, nil)
}

func (c *Client) ReconcileLabGroupAccessRevision(
	ctx context.Context,
	group string,
	policies []labAccessModel.ClientPolicy,
	target eventLabModel.AccessTarget,
) error {
	if target.Group != group || target.ExpectedGroupUID == "" || target.OperationID == uuid.Nil ||
		target.Revision <= 0 {
		return fmt.Errorf("replace lab group access policy: incomplete or foreign revision target")
	}
	return c.reconcileLabGroupAccess(ctx, group, policies, &target)
}

func (c *Client) reconcileLabGroupAccess(
	ctx context.Context,
	group string,
	policies []labAccessModel.ClientPolicy,
	target *eventLabModel.AccessTarget,
) error {
	rules := make([]*labpb.LabGroupAccessRule, 0, len(policies))
	for _, policy := range policies {
		// An empty LabNames list means "all Labs" in the operator protocol.
		// Omit the rule instead so the group's default-deny policy revokes the
		// client when no challenge Lab is currently available.
		if len(policy.AllowedLabs) == 0 {
			continue
		}
		// The rule names a client and its labs, never a gateway: the laboratory gives the client every VPN gateway of each lab
		// (contract 2.0). A per-gateway choice would be added here, to this rule, when the laboratory's rule has a selector.
		rules = append(rules, &labpb.LabGroupAccessRule{
			Action:      labpb.LabGroupAccessAction_LAB_GROUP_ACCESS_ACTION_ALLOW,
			ClientNames: []string{policy.Name},
			LabNames:    append([]string(nil), policy.AllowedLabs...),
		})
	}
	wire := &labpb.LabGroupAccessPolicy{LabGroupName: group, Rules: rules}
	if target != nil {
		wire.OperationId = target.OperationID.String()
		wire.DesiredRevision = target.Revision
		wire.ExpectedGroupUid = target.ExpectedGroupUID
	}
	res, err := c.SetLabGroupAccess(ctx, &labpb.SetLabGroupAccessRequest{
		Labels:   c.requestLabels(),
		Policies: []*labpb.LabGroupAccessPolicy{wire},
	})
	if err != nil {
		return agentErr("replace lab group access policy", err)
	}
	return policyResult("replace lab group access policy", res.GetResults())
}

// policyResult reads the answer of a policy replacement. The group not being there (yet), or the policy
// being created by a concurrent writer, is a wait: the revision stays dirty and the next pass replaces the
// policy, so neither is recorded as a failure nor taken for an applied policy.
func policyResult(op string, results []*labpb.ItemResult) error {
	if len(results) == 1 {
		r := results[0]
		if r.GetState() == labpb.ItemState_ITEM_STATE_NOT_FOUND ||
			(r.GetState() == labpb.ItemState_ITEM_STATE_FAILED && strings.Contains(r.GetError(), "already exists")) {
			return fmt.Errorf("%s: %w: %s", op, infraModel.ErrGroupNotReady, r.GetError())
		}
	}
	return oneResult(op, results)
}

// SetLabGroupSuspended sets the group runtime state without deleting its
// configuration. The operator performs the corresponding scale-down or resume.
func (c *Client) SetLabGroupSuspended(ctx context.Context, group string, suspended bool) error {
	res, err := c.UpdateLabGroups(ctx, &labpb.UpdateLabGroupsRequest{
		Items: []*labpb.UpdateLabGroupItem{
			{Name: group, Changes: &labpb.LabGroupChanges{Suspended: &suspended}},
		},
	})
	if err != nil {
		return agentErr("set lab group suspended", err)
	}
	// A newly created team may not have requested any VPN group yet.
	if suspended && len(res.GetResults()) == 1 &&
		res.GetResults()[0].GetState() == labpb.ItemState_ITEM_STATE_NOT_FOUND {
		return nil
	}
	return oneResult("set lab group suspended", res.GetResults())
}

// SetLabGroupVPNDisabled stops only the team's WireGuard server. The gateway
// and Labs keep their own lifecycle, including for non-VPN challenges.
func (c *Client) SetLabGroupVPNDisabled(ctx context.Context, group string, disabled bool) error {
	probe := !disabled
	res, err := c.UpdateLabGroups(ctx, &labpb.UpdateLabGroupsRequest{
		Items: []*labpb.UpdateLabGroupItem{
			{
				Name: group,
				Changes: &labpb.LabGroupChanges{
					VpnDisabled:         &disabled,
					ProbeWhileSuspended: &probe,
				},
			},
		},
	})
	if err != nil {
		return agentErr("set lab group VPN disabled", err)
	}
	if disabled && len(res.GetResults()) == 1 &&
		res.GetResults()[0].GetState() == labpb.ItemState_ITEM_STATE_NOT_FOUND {
		return nil
	}
	return oneResult("set lab group VPN disabled", res.GetResults())
}

// ResetDevice discards the snapshots of one device and restarts it from its base image.
func (c *Client) ResetDevice(ctx context.Context, group, lab, device string) error {
	res, err := c.ResetDevices(
		ctx,
		&labpb.DevicesRequest{Items: []*labpb.ItemRef{{LabGroup: group, Lab: lab, Name: device}}},
	)
	if err != nil {
		return deviceActionErr("reset device", err)
	}
	return deviceResult("reset device", res.GetResults())
}

// RescueDevice switches one device to rescue mode (a shell from its latest snapshot) or back.
func (c *Client) RescueDevice(ctx context.Context, group, lab, device string, enable bool) error {
	res, err := c.RescueDevices(ctx, &labpb.RescueDevicesRequest{
		Devices: &labpb.DevicesRequest{
			Items: []*labpb.ItemRef{{LabGroup: group, Lab: lab, Name: device}},
		},
		Enable: enable,
	})
	if err != nil {
		return deviceActionErr("rescue device", err)
	}
	return deviceResult("rescue device", res.GetResults())
}

// PrewarmImages asks the platform image cache to fetch the images from their upstream registries
// before labs need them. It is asynchronous and idempotent: every call returns the current state of
// each image, so repeating it polls.
func (c *Client) PrewarmImages(
	ctx context.Context,
	images []string,
	creds []infraModel.RegistryCredential,
) ([]infraModel.ImagePrewarm, error) {
	res, err := c.Client.PrewarmImages(
		ctx,
		&labpb.PrewarmImagesRequest{
			Images:              images,
			RegistryCredentials: registryCredentials(creds),
		},
	)
	if err != nil {
		return nil, agentErr("prewarm images", err)
	}
	out := make([]infraModel.ImagePrewarm, 0, len(res.GetImages()))
	for _, image := range res.GetImages() {
		out = append(
			out,
			infraModel.ImagePrewarm{
				Image:  image.GetImage(),
				State:  prewarmState(image.GetState()),
				Error:  image.GetError(),
				Digest: image.GetDigest(),
			},
		)
	}
	return out, nil
}

// registryCredentials maps the pull credentials onto the wire; none maps to nil.
func registryCredentials(creds []infraModel.RegistryCredential) []*labpb.RegistryCredential {
	if len(creds) == 0 {
		return nil
	}
	out := make([]*labpb.RegistryCredential, 0, len(creds))
	for _, c := range creds {
		out = append(
			out,
			&labpb.RegistryCredential{
				Registry: c.Registry,
				Username: c.Username,
				Password: c.Password,
			},
		)
	}
	return out
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
			Err:        reasonErr(r),
		}
	}
	if infraModel.ReasonError(r.GetReason()) != nil {
		return fmt.Errorf("%s: %w", op, reasonErr(r))
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
		return exerciseModel.LabDeployStatus{
			LabUID:        l.GetUid(),
			LabGeneration: l.GetGeneration(),
			Phase:         exerciseModel.DeployPhasePending,
		}
	}
	out := exerciseModel.LabDeployStatus{
		LabUID: l.GetUid(), LabGeneration: l.GetGeneration(),
		Phase:        st.GetPhase(),
		Ready:        st.GetReady(),
		ImageWarning: st.GetImageWarning(),
	}
	for _, g := range st.GetGateways() {
		out.Gateways = append(out.Gateways, exerciseModel.LabDeployedGateway{
			Name: g.GetName(), Kind: g.GetKind(), CIDR: g.GetCidr(), Ready: g.GetReady(),
			LabInitiatedBlocked: g.GetLabInitiatedBlocked(),
		})
	}
	if q := st.GetScheduling(); q != nil {
		out.Queue = &exerciseModel.LabQueue{
			Position: q.GetPosition(),
			Length:   q.GetLength(),
			Reason:   q.GetReason(),
			Message:  q.GetMessage(),
			Group:    q.GetGroup(),
			Pods:     q.GetPods(),
			Pending:  q.GetPending(),
		}
	}
	for _, d := range st.GetDevices() {
		if d.GetUsageAvailable() {
			out.UsageAvailable = true
			out.CPUMillicores += d.GetCpuMillicores()
			out.MemoryBytes += d.GetMemoryBytes()
		}
		device := exerciseModel.LabDeployedDevice{
			Name:                d.GetName(),
			Ready:               d.GetReady(),
			Reason:              d.GetPodReason(),
			CPUThrottledPercent: d.GetCpuThrottledPercent(),
		}
		if sn := d.GetSnapshot(); sn != nil {
			device.Snapshot = &exerciseModel.DeviceSnapshot{
				LastSnapshotAt: unixMilli(
					sn.GetLastSnapshotUnixMs(),
				),
				RestoredAt: unixMilli(sn.GetRestoredUnixMs()),
				SizeBytes:  sn.GetSizeBytes(),
				Warning:    sn.GetWarning(),
				Rescue:     sn.GetRescue(),
			}
		}
		device.Scheduling = mapPodScheduling(d.GetScheduling())
		device.Bootstrap = mapBootstrap(d.GetBootstrap())
		out.Devices = append(out.Devices, device)
	}
	for _, a := range st.GetAccess() {
		out.Access = append(out.Access, exerciseModel.LabAccess{
			Device: a.GetDevice(), Port: a.GetPort(), Protocol: a.GetProtocol(), URL: a.GetUrl(),
		})
	}
	out.FillQueueDetails()
	return out
}

// mapBootstrap maps the bootstrap state of a device; nil when the device has none (or the laboratory has not seen it yet).
func mapBootstrap(b *labpb.BootstrapStatus) *exerciseModel.DeviceBootstrapStatus {
	var phase string
	switch b.GetPhase() {
	case labpb.BootstrapPhase_BOOTSTRAP_PHASE_PENDING:
		phase = exerciseModel.BootstrapPhasePending
	case labpb.BootstrapPhase_BOOTSTRAP_PHASE_DONE:
		phase = exerciseModel.BootstrapPhaseDone
	case labpb.BootstrapPhase_BOOTSTRAP_PHASE_FAILED:
		phase = exerciseModel.BootstrapPhaseFailed
	case labpb.BootstrapPhase_BOOTSTRAP_PHASE_NONE:
		return nil
	default:
		return nil
	}
	return &exerciseModel.DeviceBootstrapStatus{
		Phase:    phase,
		Attempts: b.GetAttempts(),
		Reason:   b.GetReason(),
	}
}

// mapPodScheduling maps the scheduler state of one device pod; nil when the scheduler does not track it.
func mapPodScheduling(p *labpb.PodScheduling) *exerciseModel.PodScheduling {
	if p == nil {
		return nil
	}
	out := &exerciseModel.PodScheduling{
		State:    podState(p.GetState()),
		QueuedAt: unixMilli(p.GetQueuedUnixMs()),
		DispatchedAt: unixMilli(
			p.GetDispatchedUnixMs(),
		),
		StartedAt:   unixMilli(p.GetStartedUnixMs()),
		Withdrawals: p.GetWithdrawals(),
		RetryAfter:  unixMilli(p.GetRetryAfterUnixMs()),
	}
	if f := p.GetFailure(); f != nil {
		out.Failure = &exerciseModel.PodFailure{
			Reason:       f.GetReason(),
			Message:      f.GetMessage(),
			RestartCount: f.GetRestartCount(),
			At:           unixMilli(f.GetAtUnixMs()),
		}
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
