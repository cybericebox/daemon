//go:build liveagent

// Live integration check for the deploy adapter against a running LabManager
// agent (see the port-forward + cert extraction in the session notes). Excluded
// from normal builds by the liveagent tag. Run with:
//
//	LIVE_AGENT_ENDPOINT=passthrough:///127.0.0.1:8443 \
//	LIVE_AGENT_SNI=agent.lab.test \
//	LIVE_AGENT_CERTS=<dir with client.crt/client.key/ca.crt> \
//	go test -tags liveagent -run TestLiveDeployFlow -v ./internal/delivery/infrastructure/labagent/
package labagent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// rawClient adapts a raw gRPC LabManager client to the labclient.Client shape the
// adapter embeds, so the real adapter methods run against the live agent.
type rawClient struct {
	labpb.LabManagerClient
	conn *grpc.ClientConn
}

func (r rawClient) Close() error { return r.conn.Close() }

func TestLiveDeployFlow(t *testing.T) {
	endpoint := os.Getenv("LIVE_AGENT_ENDPOINT")
	sni := os.Getenv("LIVE_AGENT_SNI")
	certDir := os.Getenv("LIVE_AGENT_CERTS")
	if endpoint == "" || sni == "" || certDir == "" {
		t.Skip("LIVE_AGENT_* env not set")
	}

	cert, err := tls.LoadX509KeyPair(certDir+"/client.crt", certDir+"/client.key")
	if err != nil {
		t.Fatalf("load client keypair: %v", err)
	}
	caPEM, err := os.ReadFile(certDir + "/ca.crt")
	if err != nil {
		t.Fatalf("read ca: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("append ca")
	}
	creds := credentials.NewTLS(&tls.Config{
		ServerName:   sni,
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	})
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(creds))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ag := &Client{Client: rawClient{LabManagerClient: labpb.NewLabManagerClient(conn), conn: conn}, instance: "livetest"}
	defer ag.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	dev := uuid.Must(uuid.NewV7())
	sw := uuid.Must(uuid.NewV7())
	topo := exerciseModel.Topology{
		VPN: exerciseModel.NetworkSpec{Enabled: true},
		Devices: []exerciseModel.Device{
			{ID: dev, Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx:alpine",
				Interfaces: []exerciseModel.Interface{{Name: "eth0", IP: exerciseModel.IPConfig{
					Type: exerciseModel.IPConfigTypeStatic, Addresses: []string{"10.10.0.2/24"}}}}},
			{ID: sw, Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch},
		},
		Connections: []exerciseModel.Connection{
			{Endpoints: []exerciseModel.Endpoint{
				{Kind: exerciseModel.EndpointDevice, DeviceID: dev, Interface: "eth0"},
				{Kind: exerciseModel.EndpointDevice, DeviceID: sw, Interface: "GigabitEthernet0/1"},
			}},
			{Endpoints: []exerciseModel.Endpoint{
				{Kind: exerciseModel.EndpointVPN, Interface: "eth0"},
				{Kind: exerciseModel.EndpointDevice, DeviceID: sw, Interface: "GigabitEthernet0/2"},
			}},
		},
	}

	group := "livetest-" + uuid.Must(uuid.NewV7()).String()
	t.Logf("deploying group %s", group)
	if err := ag.DeployLab(ctx, group, "lab", infraModel.LabMeta{}, topo); err != nil {
		t.Fatalf("DeployLab: %v", err)
	}
	defer func() {
		if err := ag.DestroyLabGroup(context.Background(), group); err != nil {
			t.Errorf("DestroyLabGroup: %v", err)
		} else {
			t.Logf("destroyed group %s", group)
		}
	}()

	var last exerciseModel.LabDeployStatus
	deadline := time.After(3 * time.Minute)
poll:
	for {
		st, err := ag.LabStatus(ctx, group, "lab")
		if err != nil {
			t.Fatalf("LabStatus: %v", err)
		}
		last = st
		t.Logf("phase=%s ready=%v devices=%d vpnCidr=%s", st.Phase, st.Ready, len(st.Devices), st.VPNCIDR)
		if st.Ready {
			break
		}
		select {
		case <-deadline:
			break poll
		case <-time.After(5 * time.Second):
		}
	}

	if !last.Ready {
		t.Fatalf("lab did not become ready; last=%+v", last)
	}
	if last.VPNCIDR == "" {
		t.Errorf("expected a VPN CIDR on a VPN-enabled lab: %+v", last)
	}
}
