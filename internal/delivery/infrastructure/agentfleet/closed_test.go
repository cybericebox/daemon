package agentfleet

import labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

// closedClient satisfies the agent client with nothing behind it.
type closedClient struct{ labpb.LabManagerClient }

func (closedClient) Close() error { return nil }
