package agentfleet

import (
	"context"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"google.golang.org/grpc"
)

// closedClient satisfies the agent client with nothing behind it, except the key upkeep calls.
type closedClient struct {
	labpb.LabManagerClient
	renewed, rotated, removed *[]string
}

func (closedClient) Close() error { return nil }

func (c closedClient) RenewCertificate(_ context.Context, in *labpb.RenewCertificateRequest, _ ...grpc.CallOption) (*labpb.CertificateResponse, error) {
	if c.renewed != nil {
		*c.renewed = append(*c.renewed, in.GetCsrPem())
	}
	return &labpb.CertificateResponse{CertificatePem: "NEWCERT"}, nil
}

func (c closedClient) RotateAccessKey(_ context.Context, in *labpb.RotateAccessKeyRequest, _ ...grpc.CallOption) (*labpb.Empty, error) {
	if c.rotated != nil {
		*c.rotated = append(*c.rotated, in.GetKeyId())
	}
	return &labpb.Empty{}, nil
}

func (c closedClient) RemoveAccessKey(_ context.Context, in *labpb.RemoveAccessKeyRequest, _ ...grpc.CallOption) (*labpb.Empty, error) {
	if c.removed != nil {
		*c.removed = append(*c.removed, in.GetKeyId())
	}
	return &labpb.Empty{}, nil
}
