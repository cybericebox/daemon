// Package labagent adapts the laboratory LabManager gRPC client to the daemon.
// It is the single seam between our control plane and the cyber-range
// infrastructure agent. The agent is OPTIONAL: when it is not configured the
// daemon still runs, and this package is simply never constructed (nil) — the
// use-case layer then blocks any lab-deploying operation with an explicit
// ErrInfrastructureUnavailable instead of failing silently.
//
// The laboratory client speaks the operator's vocabulary (LabGroup / Lab /
// LabGroupClient / spec_json); translating that to our domain (events,
// exercises, participants) is the use-case layer's job, not this adapter's.
package labagent

import (
	"context"

	labclient "github.com/cybericebox/laboratory/pkg/agent/client"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// Client wraps the laboratory LabManager client. It embeds the typed client so
// callers get every LabManager RPC, plus Close for connection ownership.
type Client struct {
	labclient.Client
	// instance is the value of the immutable platform-instance label on every object this
	// client creates; the Monitoring stream is subscribed with it.
	instance string
}

// Health probes the agent connection with a gRPC Ping. It satisfies the
// use-case Agent port, keeping gRPC types out of the application layer.
func (c *Client) Health(ctx context.Context) error {
	_, err := c.Ping(ctx, &labpb.Empty{})
	return err
}

// Instance is the platform-instance label value of this client.
func (c *Client) Instance() string { return c.instance }

// MonitoringSelector is the label selector of the Monitoring subscription: only the objects
// this platform instance created.
func (c *Client) MonitoringSelector() string {
	return infraModel.LabelInstance + "=" + c.instance
}
