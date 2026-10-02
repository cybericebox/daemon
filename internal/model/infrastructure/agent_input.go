package infrastructure

import (
	"net"
	"strconv"
	"strings"
)

const (
	maxAgentNameLength = 64
	maxAgentPriority   = 10000
	maxPEMLength       = 64 << 10
	maxTokenLength     = 1024
)

// AgentEnrollment is the admin form that adds an agent: where it is and the one-time enrollment token
// the cluster administrator gave. The platform generates every key itself.
type AgentEnrollment struct {
	Name     string
	Endpoint string
	// Token is the one-time enrollment token of the agent's tenant; it is never stored.
	Token string
	// CAPEM verifies the agent's server certificate; empty means the system roots.
	CAPEM    string
	Enabled  bool
	Priority int
}

// Normalize trims and validates the form.
func (in AgentEnrollment) Normalize() (AgentEnrollment, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Endpoint = strings.TrimSpace(in.Endpoint)
	in.Token = strings.TrimSpace(in.Token)
	in.CAPEM = strings.TrimSpace(in.CAPEM)
	bad := func(msg string) (AgentEnrollment, error) { return in, ErrAgentInvalid.WithMessage(msg).Err() }
	switch {
	case in.Name == "" || len([]rune(in.Name)) > maxAgentNameLength:
		return bad("Agent name must be 1-64 characters")
	case !validEndpoint(in.Endpoint):
		return bad("Agent endpoint must be host:port")
	case in.Token == "" || len(in.Token) > maxTokenLength:
		return bad("The enrollment token is required")
	case in.Priority < 0 || in.Priority > maxAgentPriority:
		return bad("Agent priority must be 0-10000")
	case len(in.CAPEM) > maxPEMLength:
		return bad("The CA certificate is too long")
	}
	return in, nil
}

// AgentUpdate is what an admin may change after enrollment; a nil field stays as it is. The endpoint
// identifies the agent and its certificate, so it never changes. CAPEM nil or empty keeps the stored server
// CA; ClearCA removes it.
type AgentUpdate struct {
	Name     *string
	CAPEM    *string
	ClearCA  bool
	Enabled  *bool
	Priority *int
}

// Normalize trims and validates the fields that are set.
func (in AgentUpdate) Normalize() (AgentUpdate, error) {
	bad := func(msg string) (AgentUpdate, error) { return in, ErrAgentInvalid.WithMessage(msg).Err() }
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len([]rune(name)) > maxAgentNameLength {
			return bad("Agent name must be 1-64 characters")
		}
		in.Name = &name
	}
	if in.Priority != nil && (*in.Priority < 0 || *in.Priority > maxAgentPriority) {
		return bad("Agent priority must be 0-10000")
	}
	if in.CAPEM != nil {
		ca := strings.TrimSpace(*in.CAPEM)
		if len(ca) > maxPEMLength {
			return bad("The CA certificate is too long")
		}
		if ca == "" {
			in.CAPEM = nil
		} else {
			in.CAPEM = &ca
		}
	}
	if in.ClearCA && in.CAPEM != nil {
		return bad("Set a CA certificate or clear it, not both")
	}
	return in, nil
}

// validEndpoint accepts host:port with no scheme or path.
func validEndpoint(endpoint string) bool {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || host == "" || len(host) > 253 || strings.ContainsAny(host, " /@") {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}
