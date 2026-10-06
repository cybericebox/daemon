package app

import (
	"testing"
	"time"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

func TestAgentErrorsMapsTheLaboratoryReport(t *testing.T) {
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	got := agentErrors(&labpb.ErrorJournal{
		Components: []*labpb.ComponentErrors{{Component: "operator", Instance: "op-1", Groups: []*labpb.ErrorGroup{
			{Fingerprint: "ab12cd34", Kind: "reconcile", Normalized: "failed <obj>", Count: 3, FirstUnixMs: at.UnixMilli(), LastUnixMs: at.Add(time.Minute).UnixMilli(), Samples: []string{"failed x"}},
		}}},
		DeployFailures:         []*labpb.DeployFailure{{LabGroup: "g", Lab: "l", ReasonCode: "CrashLoop", Device: "vpn", Message: "m", AtUnixMs: at.UnixMilli()}},
		ClientCertNotAfterUnix: at.Unix(),
	})
	g := got.Components[0].Groups[0]
	if got.Components[0].Component != "operator" || g.Fingerprint != "ab12cd34" || g.Count != 3 || !g.Last.Equal(at.Add(time.Minute)) || g.Samples[0] != "failed x" {
		t.Fatalf("component mapping = %+v", got.Components)
	}
	d := got.DeployFailures[0]
	if d.ReasonCode != "CrashLoop" || d.Device != "vpn" || !d.At.Equal(at) {
		t.Fatalf("deploy failure mapping = %+v", d)
	}
	if got.CertNotAfter == nil || !got.CertNotAfter.Equal(at) {
		t.Fatalf("cert = %v", got.CertNotAfter)
	}
	if agentErrors(&labpb.ErrorJournal{}).CertNotAfter != nil {
		t.Fatal("no certificate reported must stay nil")
	}
}
