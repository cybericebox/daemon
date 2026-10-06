package teamChallengeModel_test

import (
	"testing"
	"time"

	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
	"github.com/gofrs/uuid"
)

func TestNew_StartsPreparing(t *testing.T) {
	challenge, err := teamChallengeModel.New(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), 0, []byte(`{"name":"task"}`), "ICE{flag}", time.Now())
	if err != nil || challenge.Readiness != teamChallengeModel.ReadinessPreparing {
		t.Fatalf("invalid new team challenge: %+v, %v", challenge, err)
	}
}

func TestGenerateFlag(t *testing.T) {
	a, err := teamChallengeModel.GenerateFlag()
	if err != nil || len(a) != 45 || a[:4] != "ICE{" || a[len(a)-1:] != "}" {
		t.Fatalf("invalid generated flag %q: %v", a, err)
	}
}

func TestSelectVariant_IsStable(t *testing.T) {
	teamID, exerciseID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	a, err := teamChallengeModel.SelectVariant(teamID, exerciseID, 3)
	b, err2 := teamChallengeModel.SelectVariant(teamID, exerciseID, 3)
	if err != nil || err2 != nil || a != b || a < 0 || a > 2 {
		t.Fatalf("unstable variant selection: %d, %d, %v, %v", a, b, err, err2)
	}
}

func TestResolveExpectedFlag_UsesCatalogPolicy(t *testing.T) {
	fixed, err := teamChallengeModel.ResolveExpectedFlag([]string{"ICE{fixed}"})
	if err != nil || fixed != "ICE{fixed}" {
		t.Fatalf("fixed flag = %q, %v", fixed, err)
	}
	generated, err := teamChallengeModel.ResolveExpectedFlag(nil)
	if err != nil || len(generated) != len("ICE{")+40+len("}") {
		t.Fatalf("generated flag = %q, %v", generated, err)
	}
	selected, err := teamChallengeModel.ResolveExpectedFlag([]string{"ICE{first}", "ICE{second}"})
	if err != nil || (selected != "ICE{first}" && selected != "ICE{second}") {
		t.Fatalf("selected flag = %q, %v", selected, err)
	}
}

func TestResolveExpectedFlag_RendersTemplateInsteadOfStoringMarker(t *testing.T) {
	got, err := teamChallengeModel.ResolveExpectedFlag([]string{`template:ICE{prefix-\d}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len("ICE{prefix-0}") || got[:11] != "ICE{prefix-" || got[len(got)-1:] != "}" {
		t.Fatalf("template resolved to %q", got)
	}
}

func TestReadinessTransitions(t *testing.T) {
	c, _ := teamChallengeModel.New(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), 0, []byte(`{"name":"task"}`), "ICE{flag}", time.Now())
	if c.Publish() == nil || c.MarkReady() != nil || c.Publish() != nil || c.Readiness != teamChallengeModel.ReadinessPublished {
		t.Fatalf("invalid readiness transition")
	}
}
