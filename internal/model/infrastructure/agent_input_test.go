package infrastructure

import (
	"errors"
	"strings"
	"testing"
)

func TestAgentEnrollmentNormalize(t *testing.T) {
	valid := AgentEnrollment{Name: "  eu-1 ", Endpoint: " agent.example.com:443 ", Token: " tok ", Priority: 10, Enabled: true}
	got, err := valid.Normalize()
	if err != nil || got.Name != "eu-1" || got.Endpoint != "agent.example.com:443" || got.Token != "tok" {
		t.Fatalf("valid = %+v, %v", got, err)
	}
	for name, in := range map[string]AgentEnrollment{
		"no name":       {Endpoint: "a:443", Token: "t"},
		"long name":     {Name: strings.Repeat("x", 65), Endpoint: "a:443", Token: "t"},
		"no port":       {Name: "n", Endpoint: "agent.example.com", Token: "t"},
		"scheme":        {Name: "n", Endpoint: "https://a:443", Token: "t"},
		"bad port":      {Name: "n", Endpoint: "a:70000", Token: "t"},
		"no token":      {Name: "n", Endpoint: "a:443"},
		"negative prio": {Name: "n", Endpoint: "a:443", Token: "t", Priority: -1},
		"huge prio":     {Name: "n", Endpoint: "a:443", Token: "t", Priority: 10001},
	} {
		if _, err = in.Normalize(); !errors.Is(err, ErrAgentInvalid.Err()) {
			t.Errorf("%s: err = %v, want invalid", name, err)
		}
	}
}

func TestAgentUpdateNormalize(t *testing.T) {
	if got, err := (AgentUpdate{Name: " a ", Priority: 3}).Normalize(); err != nil || got.Name != "a" {
		t.Fatalf("valid = %+v, %v", got, err)
	}
	for name, in := range map[string]AgentUpdate{"no name": {}, "huge prio": {Name: "a", Priority: 10001}} {
		if _, err := in.Normalize(); !errors.Is(err, ErrAgentInvalid.Err()) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
