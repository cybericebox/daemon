package labagent

import (
	"context"
	"fmt"
	eventLab "github.com/cybericebox/daemon/internal/model/eventLab"
	exercise "github.com/cybericebox/daemon/internal/model/exercise"
	infra "github.com/cybericebox/daemon/internal/model/infrastructure"
	resources "github.com/cybericebox/daemon/internal/model/resources"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

type boundedServiceAgent struct {
	*fakeAgent
	maxVPN resources.Amount
}

func (a boundedServiceAgent) CreateLabGroups(ctx context.Context, in *labpb.CreateLabGroupsRequest, opts ...grpc.CallOption) (*labpb.BatchResult, error) {
	for _, item := range in.Items {
		s := item.GetVpnSize()
		if s.GetMemoryBytes() > a.maxVPN.MemoryBytes || s.GetCpuMillicores() > a.maxVPN.CPUMillicores {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("vpn_size %d over producer max %d", s.GetMemoryBytes(), a.maxVPN.MemoryBytes))
		}
	}
	return a.fakeAgent.CreateLabGroups(ctx, in, opts...)
}
func serviceLimits(defaultMemory int64) infra.LimitsFeature {
	return infra.LimitsFeature{VPN: infra.GroupPodSizing{Base: resources.Amount{CPUMillicores: 20, MemoryBytes: 64 << 20}, PerUnit: resources.Amount{CPUMillicores: 6, MemoryBytes: 20 << 20}, MaxUnits: 20}, Gateway: infra.GroupPodSizing{Base: resources.Amount{CPUMillicores: 5, MemoryBytes: 16 << 20}, PerUnit: resources.Amount{CPUMillicores: 2, MemoryBytes: 4 << 20}, MaxUnits: 50}, DefaultVPN: resources.Amount{CPUMillicores: 100, MemoryBytes: defaultMemory}, DefaultGateway: resources.Amount{CPUMillicores: 10, MemoryBytes: 32 << 20}}
}
func TestFleetDeployUsesExactSelectedServiceSizes(t *testing.T) {
	f := newFleetFixture(t)
	f.bm.Enabled = false
	f.am.Features = NewFeatureCell(&infra.AgentFeatures{Limits: serviceLimits(320 << 20)})
	f.am.Client.Client = pingClient{pingAgent{fakeAgent: f.a}}
	// Preserve Ping while enforcing the public CreateLabGroups request boundary.
	f.am.Client.Client = serviceContractClient{pingClient{pingAgent{fakeAgent: f.a}}, boundedServiceAgent{f.a, resources.Amount{CPUMillicores: 140, MemoryBytes: 464 << 20}}}
	ctx := infra.WithPlacementNeed(context.Background(), infra.PlacementNeed{Plan: infra.GroupPlan{MaxUsers: 1}})
	if err := f.fleet.DeployLab(ctx, "initial-contract", "lab", infra.LabMeta{}, exercise.Topology{}); err != nil {
		t.Fatalf("published default must be accepted by producer: %v", err)
	}
	item := f.a.createGroup.Items[0]
	if item.VpnSize.CpuMillicores != 100 || item.VpnSize.MemoryBytes != 320<<20 || item.GatewaySize.CpuMillicores != 10 || item.GatewaySize.MemoryBytes != 32<<20 {
		t.Fatalf("service sizes were device-preset rounded: %+v", item)
	}
}

type serviceContractClient struct {
	pingClient
	boundedServiceAgent
}

func (c serviceContractClient) CreateLabGroups(ctx context.Context, in *labpb.CreateLabGroupsRequest, opts ...grpc.CallOption) (*labpb.BatchResult, error) {
	return c.boundedServiceAgent.CreateLabGroups(ctx, in, opts...)
}
func TestFleetDeployDoesNotSendOtherAgentsWorstCaseSizes(t *testing.T) {
	f := newFleetFixture(t)
	f.am.Features = NewFeatureCell(&infra.AgentFeatures{Limits: serviceLimits(320 << 20)})
	large := serviceLimits(400 << 20)
	f.bm.Features = NewFeatureCell(&infra.AgentFeatures{Limits: large})
	f.am.Client.Client = serviceContractClient{pingClient{pingAgent{fakeAgent: f.a}}, boundedServiceAgent{f.a, resources.Amount{CPUMillicores: 140, MemoryBytes: 464 << 20}}}
	ctx := infra.WithPlacementNeed(context.Background(), infra.PlacementNeed{Plan: infra.GroupPlan{MaxUsers: 1}})
	plan, known := f.fleet.GroupSizes(infra.GroupPlan{MaxUsers: 1})
	if !known || plan.VPN.MemoryBytes != 400<<20 {
		t.Fatalf("global reservation must hold reported worst case: %+v", plan)
	}
	if err := f.fleet.DeployLab(ctx, "selected-small", "lab", infra.LabMeta{}, exercise.Topology{}); err != nil {
		t.Fatal(err)
	}
	if got := f.a.createGroup.Items[0].VpnSize.MemoryBytes; got != 320<<20 {
		t.Fatalf("selected owner got another agent's service size: %d", got)
	}
}

func TestRawServiceBoundsRejectAboveCPUMemoryMax(t *testing.T) {
	f := newFleetFixture(t)
	for _, tc := range []struct {
		name     string
		cpu, mem int64
		want     string
	}{{"at", 140, 464 << 20, ""}, {"cpu", 141, 464 << 20, infra.FitCPU}, {"memory", 140, (464 << 20) + 1, infra.FitMemory}} {
		t.Run(tc.name, func(t *testing.T) {
			l := serviceLimits(tc.mem)
			l.DefaultVPN.CPUMillicores = tc.cpu
			f.am.Features = NewFeatureCell(&infra.AgentFeatures{Limits: l})
			f.bm.Enabled = false
			v := f.fleet.NeedFit(infra.PlacementNeed{Plan: infra.GroupPlan{MaxUsers: 1}})
			if tc.want == "" {
				if v != nil {
					t.Fatal(v)
				}
			} else if v == nil || v.Resource != tc.want {
				t.Fatalf("raw max did not refuse: %+v", v)
			}
		})
	}
}

type absenceClient struct {
	pingClient
	tenant     string
	featureErr error
}

func (c absenceClient) GetFeatures(context.Context, *labpb.Empty, ...grpc.CallOption) (*labpb.FeaturesResponse, error) {
	if c.featureErr != nil {
		return nil, c.featureErr
	}
	return &labpb.FeaturesResponse{Tenant: c.tenant, Lifecycle: &labpb.LifecycleFeature{ConfirmedRuntime: true, RetainedRestart: true}}, nil
}
func TestInitialAbsenceChecksDisabledHistoricalOwners(t *testing.T) {
	f := newFleetFixture(t)
	f.am.Tenant = "a"
	f.bm.Tenant = "b"
	f.am.Client.Client = absenceClient{pingClient: pingClient{pingAgent{fakeAgent: f.a}}, tenant: "a"}
	f.bm.Enabled = false
	f.b.groups = readyGroup()
	f.b.groups[0].Name = "late-group"
	f.bm.Client.Client = absenceClient{pingClient: pingClient{pingAgent{fakeAgent: f.b}}, tenant: "b"}
	absent, e := f.fleet.InitialDeploymentAbsent(context.Background(), eventLab.Ref{Group: "late-group", Lab: "lab"})
	if e != nil || absent {
		t.Fatalf("disabled owner's birth was lost: %v %v", absent, e)
	}
	f.b.groups = nil
	f.bm.Client.Client = absenceClient{pingClient: pingClient{pingAgent{fakeAgent: f.b}}, tenant: "b", featureErr: fmt.Errorf("down")}
	if absent, e = f.fleet.InitialDeploymentAbsent(context.Background(), eventLab.Ref{Group: "late-group", Lab: "lab"}); e == nil || absent {
		t.Fatal("unavailable owner certified absence")
	}
}

func TestLegacyPartialAndDefaultOnlyServiceReportsRemainExact(t *testing.T) {
	for _, tc := range []struct {
		name                string
		limits              infra.LimitsFeature
		wantCPU, wantMemory int64
	}{{"partial", infra.LimitsFeature{VPN: infra.GroupPodSizing{Base: resources.Amount{CPUMillicores: 20, MemoryBytes: 64 << 20}, MaxUnits: 20}}, 20, 64 << 20}, {"default-only", infra.LimitsFeature{DefaultVPN: resources.Amount{CPUMillicores: 100, MemoryBytes: 320 << 20}}, 100, 320 << 20}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFleetFixture(t)
			f.bm.Enabled = false
			f.am.Features = NewFeatureCell(&infra.AgentFeatures{Limits: tc.limits})
			if e := f.fleet.EnsureVPNGroup(context.Background(), "legacy"); e != nil {
				t.Fatal(e)
			}
			i := f.a.createGroup.Items[0]
			if i.GetVpnSize().GetCpuMillicores() != tc.wantCPU || i.GetVpnSize().GetMemoryBytes() != tc.wantMemory || i.GatewaySize != nil {
				t.Fatalf("partial report changed: %+v", i)
			}
		})
	}
}
func TestInitialAbsenceUnavailableClientFailsClosed(t *testing.T) {
	f := newFleetFixture(t)
	f.am.Client = nil
	if absent, e := f.fleet.InitialDeploymentAbsent(context.Background(), eventLab.Ref{Group: "unknown", Lab: "lab"}); e == nil || absent {
		t.Fatal("missing client certified absence")
	}
}
