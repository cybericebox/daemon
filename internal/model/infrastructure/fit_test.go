package infrastructure

import (
	"context"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// The agent's defaults: a device up to 500m and 512Mi, planned at 100m and 256Mi when it sets none; a lab of
// at most 10 devices; a group of 5 labs, 2 CPU and 2Gi together.
var agentLimits = LimitsFeature{
	DeviceMaxCPUMillicores: 500, DeviceMaxMemoryBytes: 512 << 20, DeviceDefaultCPUMillicores: 100, DeviceDefaultMemoryBytes: 256 << 20,
	LabMaxDevices: 10, GroupMaxLabs: 5, GroupMaxCPUMillicores: 2000, GroupMaxMemoryBytes: 2 << 30,
}

func container(name string, r *exerciseModel.DeviceResources) exerciseModel.Device {
	return exerciseModel.Device{Name: name, Type: exerciseModel.DeviceTypeContainer, Image: "img", Resources: r}
}

func TestDemandOfCountsContainersAndTakesTheLimitThenTheRequest(t *testing.T) {
	topo := exerciseModel.Topology{Devices: []exerciseModel.Device{
		container("a", &exerciseModel.DeviceResources{CPURequest: "100m", CPULimit: "250m", MemoryRequest: "64Mi"}),
		container("b", nil),
		{Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch},
	}}
	d := DemandOf(topo)
	if len(d.Devices) != 2 {
		t.Fatalf("a switch runs no pod: %+v", d.Devices)
	}
	if cpu, mem := agentLimits.Effective(d.Devices[0]); cpu != 250 || mem != 64<<20 {
		t.Fatalf("limit wins over request, request over default: %d %d", cpu, mem)
	}
	if cpu, mem := agentLimits.Effective(d.Devices[1]); cpu != 100 || mem != 256<<20 {
		t.Fatalf("a device without resources gets the agent's default profile: %d %d", cpu, mem)
	}
}

func TestCheckMirrorsTheAgentsCaps(t *testing.T) {
	big := func(cpu, mem string) Demand {
		return DemandOf(exerciseModel.Topology{Devices: []exerciseModel.Device{container("web", &exerciseModel.DeviceResources{CPULimit: cpu, MemoryLimit: mem})}})
	}
	cases := []struct {
		name     string
		demand   Demand
		resource string
		req, max int64
	}{
		{"fits", big("500m", "512Mi"), "", 0, 0},
		{"device cpu", big("1", "128Mi"), FitCPU, 1000, 500},
		{"device memory", big("100m", "1Gi"), FitMemory, 1 << 30, 512 << 20},
	}
	for _, c := range cases {
		v := agentLimits.Check(c.demand)
		if c.resource == "" {
			if v != nil {
				t.Errorf("%s: %+v", c.name, v)
			}
			continue
		}
		if v == nil || v.Resource != c.resource || v.Device != "web" || v.Requested != c.req || v.Max != c.max {
			t.Errorf("%s: %+v", c.name, v)
		}
	}
	var many Demand
	for i := 0; i < 11; i++ {
		many.Devices = append(many.Devices, DeviceNeed{Name: "d"})
	}
	if v := agentLimits.Check(many); v == nil || v.Resource != FitDevices || v.Requested != 11 || v.Max != 10 {
		t.Errorf("devices per lab: %+v", v)
	}
	var heavy Demand
	for i := 0; i < 5; i++ {
		heavy.Devices = append(heavy.Devices, DeviceNeed{Name: "d", CPUMillicores: 500, cpuSet: true})
	}
	if v := agentLimits.Check(heavy); v == nil || v.Resource != FitGroupCPU || v.Requested != 2500 || v.Max != 2000 {
		t.Errorf("group cpu: %+v", v)
	}
	// The group adds its labs up: labs are added up: each is small, the group cap is not.
	lab := Demand{Devices: []DeviceNeed{{Name: "d", CPUMillicores: 400, cpuSet: true}}}
	if agentLimits.Fits(PlacementNeed{Labs: []Demand{lab, lab, lab, lab}}) != nil {
		t.Error("four labs of 400m fit 2000m")
	}
	if v := agentLimits.Fits(PlacementNeed{Labs: []Demand{lab, lab, lab, lab, lab, lab}}); v == nil || v.Resource != FitGroupLabs {
		t.Errorf("six labs pass the labs-per-group cap: %+v", v)
	}
	fiveHundred := Demand{Devices: []DeviceNeed{{Name: "d", CPUMillicores: 500, cpuSet: true}}}
	if v := agentLimits.Fits(PlacementNeed{Labs: []Demand{fiveHundred, fiveHundred, fiveHundred, fiveHundred, fiveHundred}}); v == nil || v.Resource != FitGroupCPU || v.Requested != 2500 {
		t.Errorf("five labs of 500m pass the group cpu cap: %+v", v)
	}
	var six []Demand
	for i := 0; i < 6; i++ {
		six = append(six, Demand{})
	}
	if v := agentLimits.Fits(PlacementNeed{Labs: six}); v == nil || v.Resource != FitGroupLabs || v.Requested != 6 || v.Max != 5 {
		t.Errorf("labs per group: %+v", v)
	}
	if (LimitsFeature{}).Check(heavy) != nil || (LimitsFeature{}).Check(many) != nil {
		t.Error("zero means no limit")
	}
}

func TestPlacementNeedTravelsInTheContextAndMustFitEveryLab(t *testing.T) {
	small := Demand{Devices: []DeviceNeed{{Name: "a", CPUMillicores: 100, cpuSet: true}}}
	huge := Demand{Devices: []DeviceNeed{{Name: "b", CPUMillicores: 4000, cpuSet: true}}}
	ctx := WithPlacementNeed(context.Background(), PlacementNeed{Labs: []Demand{small, huge}})
	need := PlacementNeedFrom(ctx)
	if len(need.Labs) != 2 || len(PlacementNeedFrom(context.Background()).Labs) != 0 {
		t.Fatal("the need rides on the context")
	}
	if v := agentLimits.Fits(need); v == nil || v.Device != "b" {
		t.Fatalf("one lab that passes a cap fails the group: %+v", v)
	}
	if agentLimits.Fits(PlacementNeed{Labs: []Demand{small}}) != nil {
		t.Fatal("small fits")
	}
}
