package labagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	labclient "github.com/cybericebox/laboratory/pkg/agent/client"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
)

// fakeAgent is an in-memory LabManager: it records the requests and answers each item with the
// configured state.
type fakeAgent struct {
	labpb.LabManagerClient

	groups      []*labpb.LabGroup // what ListLabGroups knows
	labs        []*labpb.Lab
	clients     []*labpb.LabGroupClient
	itemState   labpb.ItemState // answer for every mutated item; default CREATED
	itemError   string
	retryable   bool
	clientCfg   string
	createGroup *labpb.CreateLabGroupsRequest
	createLabs  *labpb.CreateLabsRequest
	createCl    *labpb.CreateLabGroupClientsRequest
	access      *labpb.SetLabGroupAccessRequest
	updateGroup *labpb.UpdateLabGroupsRequest
	deleted     map[string]*labpb.DeleteRequest
	reset       *labpb.DevicesRequest
	rescue      *labpb.RescueDevicesRequest
	prewarm     *labpb.PrewarmImagesRequest
	prewarmOut  *labpb.PrewarmImagesResult
	callErr     error
	// strict makes the agent stateful about groups: only created ones are known (ready at once).
	strict bool
	// listErr fails ListLabGroups.
	listErr                                      error
	listSelector                                 string
	renewCSR, rotateKeyID, rotatePub, removedKey string
	keyErr                                       error
	// clientGone makes Create answer EXISTS (without a config) until the client is deleted.
	clientExists bool
}

func (f *fakeAgent) Close() error { return nil }

func (f *fakeAgent) state() labpb.ItemState {
	if f.itemState == labpb.ItemState_ITEM_STATE_UNSPECIFIED {
		return labpb.ItemState_ITEM_STATE_CREATED
	}
	return f.itemState
}

func (f *fakeAgent) result(ref *labpb.ItemRef) *labpb.ItemResult {
	return &labpb.ItemResult{Ref: ref, State: f.state(), Error: f.itemError, Retryable: f.retryable}
}

func (f *fakeAgent) CreateLabGroups(_ context.Context, in *labpb.CreateLabGroupsRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	f.createGroup = in
	out := &labpb.BatchResult{}
	for _, item := range in.GetItems() {
		if f.strict && f.callErr == nil {
			f.groups = append(f.groups, &labpb.LabGroup{Name: item.GetName(), Status: &labpb.LabGroupStatus{Namespace: "ns-" + item.GetName()}})
		}
		out.Results = append(out.Results, f.result(&labpb.ItemRef{Name: item.GetName()}))
	}
	return out, f.callErr
}

func (f *fakeAgent) ListLabGroups(_ context.Context, in *labpb.ListRequest, _ ...grpc.CallOption) (*labpb.LabGroupList, error) {
	f.listSelector = in.GetSelector()
	if f.listErr != nil {
		return nil, f.listErr
	}
	if !f.strict || len(in.GetItems()) == 0 {
		return &labpb.LabGroupList{Items: f.groups}, nil
	}
	out := &labpb.LabGroupList{}
	for _, g := range f.groups {
		if g.GetName() == in.GetItems()[0].GetName() {
			out.Items = append(out.Items, g)
		}
	}
	return out, nil
}

func (f *fakeAgent) CreateLabs(_ context.Context, in *labpb.CreateLabsRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	f.createLabs = in
	out := &labpb.BatchResult{}
	for _, item := range in.GetItems() {
		out.Results = append(out.Results, f.result(&labpb.ItemRef{LabGroup: item.GetLabGroup(), Name: item.GetName()}))
	}
	return out, f.callErr
}

func (f *fakeAgent) ListLabs(context.Context, *labpb.ListRequest, ...grpc.CallOption) (*labpb.LabList, error) {
	return &labpb.LabList{Items: f.labs}, nil
}

func (f *fakeAgent) CreateLabGroupClients(_ context.Context, in *labpb.CreateLabGroupClientsRequest, _ ...grpc.CallOption) (*labpb.CreateLabGroupClientsResponse, error) {
	f.createCl = in
	out := &labpb.CreateLabGroupClientsResponse{}
	for _, item := range in.GetItems() {
		r := &labpb.LabGroupClientResult{Result: f.result(&labpb.ItemRef{LabGroup: item.GetLabGroup(), Name: item.GetName()})}
		if f.clientExists {
			r.Result.State = labpb.ItemState_ITEM_STATE_EXISTS
			out.Results = append(out.Results, r)
			continue
		}
		if f.state() == labpb.ItemState_ITEM_STATE_CREATED {
			r.Client = &labpb.LabGroupClient{Status: &labpb.LabGroupClientStatus{Config: f.clientCfg}}
		}
		out.Results = append(out.Results, r)
	}
	return out, f.callErr
}

func (f *fakeAgent) ListLabGroupClients(context.Context, *labpb.ListRequest, ...grpc.CallOption) (*labpb.LabGroupClientList, error) {
	return &labpb.LabGroupClientList{Items: f.clients}, nil
}

func (f *fakeAgent) SetLabGroupAccess(_ context.Context, in *labpb.SetLabGroupAccessRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	f.access = in
	out := &labpb.BatchResult{}
	for _, p := range in.GetPolicies() {
		out.Results = append(out.Results, f.result(&labpb.ItemRef{Name: p.GetLabGroupName()}))
	}
	return out, f.callErr
}

func (f *fakeAgent) UpdateLabGroups(_ context.Context, in *labpb.UpdateLabGroupsRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	f.updateGroup = in
	out := &labpb.BatchResult{}
	for _, item := range in.GetItems() {
		out.Results = append(out.Results, f.result(&labpb.ItemRef{Name: item.GetName()}))
	}
	return out, f.callErr
}

func (f *fakeAgent) record(kind string, in *labpb.DeleteRequest) *labpb.BatchResult {
	if f.deleted == nil {
		f.deleted = map[string]*labpb.DeleteRequest{}
	}
	f.deleted[kind] = in
	out := &labpb.BatchResult{}
	for _, ref := range in.GetItems() {
		out.Results = append(out.Results, f.result(ref))
	}
	return out
}

func (f *fakeAgent) DeleteLabGroups(_ context.Context, in *labpb.DeleteRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	return f.record("group", in), f.callErr
}
func (f *fakeAgent) DeleteLabs(_ context.Context, in *labpb.DeleteRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	return f.record("lab", in), f.callErr
}
func (f *fakeAgent) DeleteLabGroupClients(_ context.Context, in *labpb.DeleteRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	f.clientExists = false
	return f.record("client", in), f.callErr
}

func (f *fakeAgent) ResetDevices(_ context.Context, in *labpb.DevicesRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	f.reset = in
	out := &labpb.BatchResult{}
	for _, ref := range in.GetItems() {
		out.Results = append(out.Results, f.result(ref))
	}
	return out, f.callErr
}

func (f *fakeAgent) RescueDevices(_ context.Context, in *labpb.RescueDevicesRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	f.rescue = in
	out := &labpb.BatchResult{}
	for _, ref := range in.GetDevices().GetItems() {
		out.Results = append(out.Results, f.result(ref))
	}
	return out, f.callErr
}

func (f *fakeAgent) PrewarmImages(_ context.Context, in *labpb.PrewarmImagesRequest, _ ...grpc.CallOption) (*labpb.PrewarmImagesResult, error) {
	f.prewarm = in
	return f.prewarmOut, f.callErr
}

func (f *fakeAgent) RenewCertificate(_ context.Context, in *labpb.RenewCertificateRequest, _ ...grpc.CallOption) (*labpb.CertificateResponse, error) {
	f.renewCSR = in.GetCsrPem()
	if f.keyErr != nil {
		return nil, f.keyErr
	}
	return &labpb.CertificateResponse{CertificatePem: "RENEWED"}, nil
}

func (f *fakeAgent) RotateAccessKey(_ context.Context, in *labpb.RotateAccessKeyRequest, _ ...grpc.CallOption) (*labpb.Empty, error) {
	f.rotateKeyID, f.rotatePub = in.GetKeyId(), in.GetPublicKeyPem()
	return &labpb.Empty{}, f.keyErr
}

func (f *fakeAgent) RemoveAccessKey(_ context.Context, in *labpb.RemoveAccessKeyRequest, _ ...grpc.CallOption) (*labpb.Empty, error) {
	f.removedKey = in.GetKeyId()
	return &labpb.Empty{}, f.keyErr
}

func newClient(f *fakeAgent) *Client {
	return &Client{Client: labclient.Client(f), instance: "prod"}
}

func readyGroup() []*labpb.LabGroup {
	return []*labpb.LabGroup{{Name: "event-team", Status: &labpb.LabGroupStatus{Namespace: "ns-team", VpnClientSubnet: "10.8.7.0/24", ImageWarning: "vpn:latest"}}}
}

func TestEveryMutatingCallCarriesTheInstanceLabel(t *testing.T) {
	f := &fakeAgent{groups: readyGroup(), clientCfg: "cfg"}
	c := newClient(f)
	ctx := context.Background()
	if err := c.EnsureVPNGroup(ctx, "event-team"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EnsureLabClient(ctx, "event-team", "p-user"); err != nil {
		t.Fatal(err)
	}
	if err := c.ReconcileLabGroupAccess(ctx, "event-team", nil); err != nil {
		t.Fatal(err)
	}
	for name, labels := range map[string]map[string]string{
		"group": f.createGroup.GetLabels(), "client": f.createCl.GetLabels(), "access": f.access.GetLabels(),
	} {
		if labels[infraModel.LabelInstance] != "prod" {
			t.Errorf("%s request labels = %v, want the instance label", name, labels)
		}
	}
	if c.MonitoringSelector() != "cybericebox.io/instance=prod" {
		t.Errorf("selector = %q", c.MonitoringSelector())
	}
}

func TestDeployLabSendsVariantOnceWithLabelsDeployGroupAndAllVariablesAsLabEnv(t *testing.T) {
	f := &fakeAgent{groups: readyGroup()}
	c := newClient(f)
	web := exerciseModel.Device{Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx",
		EnvVars: []exerciseModel.EnvVar{{Name: "FLAG", Value: "ICE{x}"}, {Name: "MODE", Value: "ctf"}}}
	topo := exerciseModel.Topology{VPN: exerciseModel.NetworkSpec{Enabled: true}, Devices: []exerciseModel.Device{web}}
	meta := infraModel.LabMeta{
		Labels:      map[string]string{infraModel.LabelTask: "task-1", infraModel.LabelTeam: "team-1"},
		GroupLabels: map[string]string{infraModel.LabelTeam: "team-1"},
		DeployGroup: "task-1",
	}
	if err := c.DeployLab(context.Background(), "event-team", "c-1", meta, topo); err != nil {
		t.Fatal(err)
	}
	if got := f.createGroup.GetItems()[0]; got.GetName() != "event-team" || got.GetLabels()[infraModel.LabelTeam] != "team-1" {
		t.Fatalf("group item = %+v", got)
	}
	req := f.createLabs
	if len(req.GetVariants()) != 1 || len(req.GetItems()) != 1 || req.GetLabels()[infraModel.LabelInstance] != "prod" {
		t.Fatalf("create labs = %+v", req)
	}
	variant, item := req.GetVariants()[0], req.GetItems()[0]
	if item.GetVariantId() != variant.GetVariantId() || item.GetLabGroup() != "event-team" || item.GetName() != "c-1" {
		t.Fatalf("item = %+v", item)
	}
	if item.GetDeployGroup() != "task-1" || item.GetLabels()[infraModel.LabelTask] != "task-1" {
		t.Fatalf("scheduling and labels = %+v", item)
	}
	if strings.Contains(string(variant.GetSpecJson()), "ICE{x}") || strings.Contains(string(variant.GetSpecJson()), "env") {
		t.Fatalf("variables must never be in the spec: %s", variant.GetSpecJson())
	}
	if len(variant.GetEnv()) != 0 || len(item.GetEnv()) != 1 || item.GetEnv()[0].GetDevice() != "web" || item.GetEnv()[0].GetVars()["FLAG"] != "ICE{x}" || item.GetEnv()[0].GetVars()["MODE"] != "ctf" {
		t.Fatalf("variables = variant %v item %v", variant.GetEnv(), item.GetEnv())
	}
	if len(f.createCl.GetItems()) != 1 || f.createCl.GetItems()[0].GetName() != deployVPNClient {
		t.Fatalf("a VPN topology gets the tester client: %+v", f.createCl)
	}
}

func TestDeployLabIsIdempotentWhenObjectsExist(t *testing.T) {
	f := &fakeAgent{groups: readyGroup(), itemState: labpb.ItemState_ITEM_STATE_EXISTS}
	if err := newClient(f).DeployLab(context.Background(), "event-team", "c-1", infraModel.LabMeta{}, exerciseModel.Topology{VPN: exerciseModel.NetworkSpec{Enabled: true}}); err != nil {
		t.Fatalf("existing objects are the desired state: %v", err)
	}
}

func TestRetryableFailureBecomesATerminatingError(t *testing.T) {
	f := &fakeAgent{itemState: labpb.ItemState_ITEM_STATE_FAILED, itemError: "LabGroup is still being deleted", retryable: true}
	err := newClient(f).EnsureVPNGroup(context.Background(), "event-team")
	terminating, ok := infraModel.AsTerminating(err)
	if !ok || terminating.RetryAfter != infraModel.TerminatingRetryAfter {
		t.Fatalf("expected TerminatingError, got %v", err)
	}
	f = &fakeAgent{itemState: labpb.ItemState_ITEM_STATE_FAILED, itemError: "different spec"}
	err = newClient(f).EnsureVPNGroup(context.Background(), "event-team")
	if err == nil || strings.Contains(err.Error(), "<nil>") {
		t.Fatal("expected a failure")
	}
	if _, ok = infraModel.AsTerminating(err); ok {
		t.Fatalf("a permanent failure must not look transient: %v", err)
	}
}

func TestGetVPNClientSubnetReadsGroupStatus(t *testing.T) {
	got, err := newClient(&fakeAgent{groups: readyGroup()}).GetVPNClientSubnet(context.Background(), "event-team")
	if err != nil || got != "10.8.7.0/24" {
		t.Fatalf("subnet=%q err=%v", got, err)
	}
	if _, err = newClient(&fakeAgent{}).GetVPNClientSubnet(context.Background(), "event-team"); err == nil {
		t.Fatal("a missing group has no subnet")
	}
}

func TestEnsureLabClientReturnsTheOneTimeConfigFromCreateOnly(t *testing.T) {
	f := &fakeAgent{groups: readyGroup(), clientCfg: "full-config-with-private-key"}
	config, err := newClient(f).EnsureLabClient(context.Background(), "event-team", "p-user")
	if err != nil || config != "full-config-with-private-key" {
		t.Fatalf("config=%q err=%v", config, err)
	}
}

func TestEnsureLabClientRecreatesAClientWhoseConfigWasLost(t *testing.T) {
	f := &fakeAgent{groups: readyGroup(), clientCfg: "fresh-config", clientExists: true}
	config, err := newClient(f).EnsureLabClient(context.Background(), "event-team", "p-user")
	if err != nil || config != "fresh-config" {
		t.Fatalf("config=%q err=%v", config, err)
	}
	if f.deleted["client"] == nil {
		t.Fatal("the stale client must be deleted before it is created again")
	}
}

func TestLabClientHandshakeReadsTheClientStatistics(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	f := &fakeAgent{clients: []*labpb.LabGroupClient{{Status: &labpb.LabGroupClientStatus{Statistics: &labpb.LabGroupClientStatistics{LastHandshakeUnix: at.Unix()}}}}}
	got, err := newClient(f).LabClientHandshake(context.Background(), "event-team", "p-user")
	if err != nil || !got.Equal(at) {
		t.Fatalf("handshake=%v err=%v", got, err)
	}
	for _, none := range []*fakeAgent{{}, {clients: []*labpb.LabGroupClient{{Status: &labpb.LabGroupClientStatus{}}}}} {
		if got, err = newClient(none).LabClientHandshake(context.Background(), "event-team", "p-user"); err != nil || !got.IsZero() {
			t.Fatalf("a client that never connected must read as zero: %v %v", got, err)
		}
	}
}

func TestReconcileLabGroupAccessSendsTheCompletePolicy(t *testing.T) {
	f := &fakeAgent{}
	err := newClient(f).ReconcileLabGroupAccess(context.Background(), "event-team", []labAccessModel.ClientPolicy{
		{Name: "p-participant", AllowedLabs: []string{"c-challenge"}},
		{Name: "p-nothing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := f.access.GetPolicies()[0]
	if policy.GetLabGroupName() != "event-team" || len(policy.GetRules()) != 1 {
		t.Fatalf("policy = %+v", policy)
	}
	rule := policy.GetRules()[0]
	if rule.GetAction() != labpb.LabGroupAccessAction_LAB_GROUP_ACCESS_ACTION_ALLOW || rule.GetClientNames()[0] != "p-participant" || rule.GetLabNames()[0] != "c-challenge" {
		t.Fatalf("rule = %+v", rule)
	}
}

func TestSuspendAndVPNDisableSendExplicitDesiredState(t *testing.T) {
	f := &fakeAgent{itemState: labpb.ItemState_ITEM_STATE_UPDATED}
	c := newClient(f)
	if err := c.SetLabGroupSuspended(context.Background(), "event-team", true); err != nil {
		t.Fatal(err)
	}
	if got := f.updateGroup.GetItems()[0]; got.GetName() != "event-team" || !got.GetChanges().GetSuspended() || got.GetChanges().VpnDisabled != nil {
		t.Fatalf("suspend request = %+v", got)
	}
	if err := c.SetLabGroupVPNDisabled(context.Background(), "event-team", false); err != nil {
		t.Fatal(err)
	}
	if got := f.updateGroup.GetItems()[0].GetChanges(); got.VpnDisabled == nil || *got.VpnDisabled || !got.GetProbeWhileSuspended() || got.Suspended != nil {
		t.Fatalf("vpn request = %+v", got)
	}
}

func TestSuspendingAMissingGroupIsIdempotentButResumingIsNot(t *testing.T) {
	f := &fakeAgent{itemState: labpb.ItemState_ITEM_STATE_NOT_FOUND}
	c := newClient(f)
	if err := c.SetLabGroupSuspended(context.Background(), "event-team", true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLabGroupVPNDisabled(context.Background(), "event-team", true); err != nil {
		t.Fatal(err)
	}
	// A not-found resume is reported by the caller's own result handling: the state is not FAILED, so
	// the group is simply absent; the adapter does not invent an error for it.
	f.itemState = labpb.ItemState_ITEM_STATE_FAILED
	f.itemError = "boom"
	if err := c.SetLabGroupSuspended(context.Background(), "event-team", false); err == nil {
		t.Fatal("a failed resume must be reported")
	}
}

func TestDeletesToleratePresentAndMissingObjectsButReportFailures(t *testing.T) {
	for _, state := range []labpb.ItemState{labpb.ItemState_ITEM_STATE_DELETED, labpb.ItemState_ITEM_STATE_NOT_FOUND} {
		f := &fakeAgent{itemState: state}
		c := newClient(f)
		ctx := context.Background()
		if err := c.DeleteLab(ctx, "e-t", "c-1"); err != nil {
			t.Fatalf("%v: %v", state, err)
		}
		if err := c.DestroyLabGroup(ctx, "e-t"); err != nil {
			t.Fatalf("%v: %v", state, err)
		}
		if err := c.DeleteLabClient(ctx, "e-t", "p-1"); err != nil {
			t.Fatalf("%v: %v", state, err)
		}
		if ref := f.deleted["lab"].GetItems()[0]; ref.GetLabGroup() != "e-t" || ref.GetName() != "c-1" {
			t.Fatalf("deleted lab %+v", ref)
		}
		if ref := f.deleted["group"].GetItems()[0]; ref.GetName() != "e-t" {
			t.Fatalf("deleted group %+v", ref)
		}
	}
	failing := &fakeAgent{callErr: errors.New("agent down")}
	if err := newClient(failing).DeleteLab(context.Background(), "e-t", "c-1"); err == nil {
		t.Fatal("agent failure must be reported")
	}
}

func TestDeviceActionsGoByExplicitItemAndMapErrors(t *testing.T) {
	f := &fakeAgent{itemState: labpb.ItemState_ITEM_STATE_UPDATED}
	c := newClient(f)
	ctx := context.Background()
	if err := c.ResetDevice(ctx, "g", "lab", "web"); err != nil {
		t.Fatal(err)
	}
	if ref := f.reset.GetItems()[0]; ref.GetLabGroup() != "g" || ref.GetLab() != "lab" || ref.GetName() != "web" {
		t.Fatalf("reset ref = %+v", ref)
	}
	if err := c.RescueDevice(ctx, "g", "lab", "web", true); err != nil || !f.rescue.GetEnable() {
		t.Fatalf("rescue err=%v req=%+v", err, f.rescue)
	}
	for _, tc := range []struct {
		state     labpb.ItemState
		msg       string
		retryable bool
		want      error
	}{
		{labpb.ItemState_ITEM_STATE_NOT_FOUND, "device not found", false, infraModel.ErrDeviceNotFound.Err()},
		{labpb.ItemState_ITEM_STATE_FAILED, "device web of lab lab has no state persistence", false, infraModel.ErrDeviceNotPersistent.Err()},
		{labpb.ItemState_ITEM_STATE_FAILED, "Device is still being deleted", true, infraModel.ErrDeviceActionRetry.Err()},
	} {
		f.itemState, f.itemError, f.retryable = tc.state, tc.msg, tc.retryable
		if err := c.ResetDevice(ctx, "g", "lab", "web"); !errors.Is(err, tc.want) {
			t.Errorf("%v %q: err = %v, want %v", tc.state, tc.msg, err, tc.want)
		}
	}
	f.itemState, f.itemError, f.retryable = labpb.ItemState_ITEM_STATE_FAILED, "boom", false
	err := c.RescueDevice(ctx, "g", "lab", "web", false)
	if err == nil || errors.Is(err, infraModel.ErrDeviceNotPersistent.Err()) {
		t.Fatalf("an unknown failure stays a plain error: %v", err)
	}
}

func TestPrewarmImagesMapsStates(t *testing.T) {
	f := &fakeAgent{prewarmOut: &labpb.PrewarmImagesResult{Images: []*labpb.PrewarmImageStatus{
		{Image: "a", State: labpb.PrewarmState_PREWARM_STATE_DONE, Digest: "sha256:1"},
		{Image: "b", State: labpb.PrewarmState_PREWARM_STATE_WARMING},
		{Image: "c", State: labpb.PrewarmState_PREWARM_STATE_FAILED, Error: "denied"},
		{Image: "d", State: labpb.PrewarmState_PREWARM_STATE_SKIPPED},
		{Image: "e", State: labpb.PrewarmState_PREWARM_STATE_QUEUED},
	}}}
	got, err := newClient(f).PrewarmImages(context.Background(), []string{"a", "b", "c", "d", "e"})
	if err != nil || len(f.prewarm.GetImages()) != 5 {
		t.Fatalf("err=%v request=%+v", err, f.prewarm)
	}
	want := []string{infraModel.PrewarmDone, infraModel.PrewarmWarming, infraModel.PrewarmFailed, infraModel.PrewarmSkipped, infraModel.PrewarmQueued}
	for i, w := range want {
		if got[i].State != w {
			t.Errorf("image %s state = %s, want %s", got[i].Image, got[i].State, w)
		}
	}
	if got[0].Digest != "sha256:1" || got[2].Error != "denied" {
		t.Errorf("digest/error not carried: %+v", got)
	}
}

func TestLabStatusReadsTheLabAndGroupWarning(t *testing.T) {
	f := &fakeAgent{groups: readyGroup(), labs: []*labpb.Lab{{Name: "c-1", Status: &labpb.LabStatus{Phase: "Queued", ImageWarning: "web:latest",
		Scheduling: &labpb.Scheduling{Group: "task-1", Position: 3, Length: 9, Reason: "WaitingForTurn", Pods: 2, Pending: 2}}}}}
	got, err := newClient(f).LabStatus(context.Background(), "event-team", "c-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != exerciseModel.DeployPhaseQueued || got.ImageWarning != "web:latest" || got.GroupImageWarning != "vpn:latest" {
		t.Fatalf("status = %+v", got)
	}
	if q := got.Queue; q == nil || q.Position != 3 || q.Length != 9 || q.Reason != "WaitingForTurn" || q.Group != "task-1" || q.Pods != 2 || q.Pending != 2 {
		t.Fatalf("queue = %+v", got.Queue)
	}
	noNamespace := &fakeAgent{groups: []*labpb.LabGroup{{Name: "event-team", Status: &labpb.LabGroupStatus{}}}}
	if got, err = newClient(noNamespace).LabStatus(context.Background(), "event-team", "c-1"); err != nil || got.Phase != exerciseModel.DeployPhaseProvisioning {
		t.Fatalf("a group without a namespace is provisioning: %+v %v", got, err)
	}
	if _, err = newClient(&fakeAgent{groups: readyGroup()}).LabStatus(context.Background(), "event-team", "c-1"); err == nil {
		t.Fatal("an unknown lab is an error")
	}
}

func TestMapLabStatusKeepsDeviceReasonAndSumsUsage(t *testing.T) {
	got := mapLabStatus(&labpb.Lab{Status: &labpb.LabStatus{Phase: "Provisioning", Devices: []*labpb.LabDeviceStatus{
		{Name: "a", UsageAvailable: true, CpuMillicores: 120, MemoryBytes: 1000, PodReason: "ImagePullBackOff"},
		{Name: "b", UsageAvailable: true, CpuMillicores: 30, MemoryBytes: 500},
		{Name: "c", UsageAvailable: false, CpuMillicores: 999, MemoryBytes: 999},
	}}})
	if !got.UsageAvailable || got.CPUMillicores != 150 || got.MemoryBytes != 1500 || got.Devices[0].Reason != "ImagePullBackOff" {
		t.Fatalf("status = %+v", got)
	}
	if none := mapLabStatus(&labpb.Lab{Status: &labpb.LabStatus{Devices: []*labpb.LabDeviceStatus{{Name: "a"}}}}); none.UsageAvailable || none.CPUMillicores != 0 {
		t.Fatalf("a lab nobody measured has no usage: %+v", none)
	}
}

func TestKeyUpkeepCallsTheAgentAndTreatsAGoneKeyAsRemoved(t *testing.T) {
	f := &fakeAgent{}
	c := newClient(f)
	ctx := context.Background()
	cert, err := c.RenewCertificate(ctx, "CSR2")
	if err != nil || cert != "RENEWED" || f.renewCSR != "CSR2" {
		t.Fatalf("renew = %q %v csr=%q", cert, err, f.renewCSR)
	}
	if err = c.RotateAccessKey(ctx, "k-2", "PUB2"); err != nil || f.rotateKeyID != "k-2" || f.rotatePub != "PUB2" {
		t.Fatalf("rotate = %v %q %q", err, f.rotateKeyID, f.rotatePub)
	}
	if err = c.RemoveAccessKey(ctx, "k-1"); err != nil || f.removedKey != "k-1" {
		t.Fatalf("remove = %v %q", err, f.removedKey)
	}
	f.keyErr = status.Error(codes.NotFound, "no such key")
	if err = c.RemoveAccessKey(ctx, "k-gone"); err != nil {
		t.Fatalf("an unknown key is already removed: %v", err)
	}
	f.keyErr = status.Error(codes.FailedPrecondition, "the last key of a tenant cannot be removed")
	if err = c.RemoveAccessKey(ctx, "k-last"); err == nil {
		t.Fatal("the last key cannot be removed")
	}
	f.keyErr = status.Error(codes.AlreadyExists, "the id has another key")
	if err = c.RotateAccessKey(ctx, "k-1", "OTHER"); err == nil {
		t.Fatal("the same id with another key is an error")
	}
	f.keyErr = status.Error(codes.Unavailable, "down")
	if _, err = c.RenewCertificate(ctx, "CSR"); err == nil {
		t.Fatal("a failed renewal is an error")
	}
}

// A group the stand engine created with other sizes, or the access sync suspended, differs from what
// every later ensure sends; ensure must adopt it, not fail forever.
func TestEnsureVPNGroupAdoptsAnExistingGroupWithAnotherSpec(t *testing.T) {
	f := &fakeAgent{groups: readyGroup(), itemState: labpb.ItemState_ITEM_STATE_FAILED, itemError: "LabGroup event-team already exists with a different spec"}
	if err := newClient(f).EnsureVPNGroup(context.Background(), "event-team"); err != nil {
		t.Fatalf("an existing group is adopted: %v", err)
	}
	if err := newClient(f).DeployLab(context.Background(), "event-team", "c-1", infraModel.LabMeta{}, exerciseModel.Topology{}); err == nil || !strings.Contains(err.Error(), "create lab") {
		// the group is adopted; only the lab create (which also answers FAILED here) fails
		t.Fatalf("DeployLab should get past the group: %v", err)
	}
	f = &fakeAgent{itemState: labpb.ItemState_ITEM_STATE_FAILED, itemError: "LabGroup event-team already exists with a different spec"}
	if err := newClient(f).EnsureVPNGroup(context.Background(), "event-team"); err == nil {
		t.Fatal("a group the agent does not list is not adopted")
	}
}

func TestLabGroupExistsFollowsTheAgentsKnowledge(t *testing.T) {
	f := &fakeAgent{groups: readyGroup(), strict: true}
	c := newClient(f)
	if ok, err := c.LabGroupExists(context.Background(), "event-team"); err != nil || !ok {
		t.Fatalf("a known group exists: %v %v", ok, err)
	}
	if ok, err := c.LabGroupExists(context.Background(), "gone"); err != nil || ok {
		t.Fatalf("an unknown group is gone: %v %v", ok, err)
	}
	if ok, err := c.LabExists(context.Background(), "gone", "l-1"); err != nil || ok {
		t.Fatalf("a Lab of a gone group is gone: %v %v", ok, err)
	}
	f.listErr = errors.New("agent down")
	if _, err := c.LabGroupExists(context.Background(), "event-team"); err == nil {
		t.Fatal("an unreachable agent is an error, not a gone group")
	}
}
