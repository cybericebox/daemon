package exercise

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// prodFrontendDraft is a draft save in the exact shape the production
// exercises-frontend (v1.6.24, src/api/exercises/versions.ts SaveDraftInput)
// sends: no Weight, no ImageOverrides; every optional field it may set.
func prodFrontendDraft(envVars int) string {
	vars := make([]string, envVars)
	for i := range vars {
		vars[i] = fmt.Sprintf(`{"Name":"V%d","Value":"x","Secret":%t,"HasValue":false}`, i, i%2 == 0)
	}
	return `{"AdminNote":"note","Variants":[
	{"ID":"0192f0a0-0000-7000-8000-000000000a01","Index":0,"Note":"base",
	 "Tasks":[{"ID":"0192f0a0-0000-7000-8000-000000000b01","Name":"Find","Description":{"root":{"children":[]}},"Difficulty":"easy",
	   "Flag":["ICE{x}"],"LinkedDeviceID":"0192f0a0-0000-7000-8000-000000000c01","DeviceFlagVar":"FLAG",
	   "Attachments":[{"FileID":"0192f0a0-0000-7000-8000-000000000d01","Name":"a.txt"}],"Placeholders":[],
	   "Hints":[{"ID":"0192f0a0-0000-7000-8000-000000000e01","Text":"h","Level":"nudge"}]}],
	 "Topology":{"VPN":{"Enabled":true,"DHCP":true,"DHCPRanges":[{"Start":10,"End":20}],"DNS":"1.1.1.1"},"Internet":{"Enabled":false,"DHCP":false},
	   "Devices":[
	     {"ID":"0192f0a0-0000-7000-8000-000000000c01","Name":"web","Type":"container","SecurityPreset":"standard","Image":"nginx:1.27","ResourcePreset":"small",
	      "Interfaces":[{"Name":"eth0","MAC":"","IP":{"Type":"dhcp","AddressRef":null,"GatewayRef":null}}],
	      "EnvVars":[` + strings.Join(vars, ",") + `],
	      "External":{"Port":80,"Protocol":"http"},"Persistence":{"Enabled":true,"Debounce":"5s"},"WaitReadyFile":true},
	     {"ID":"0192f0a0-0000-7000-8000-000000000c02","Name":"sw","Type":"unmanaged-switch"}],
	   "Connections":[{"Endpoints":[{"Kind":"device","DeviceID":"0192f0a0-0000-7000-8000-000000000c01","Interface":"eth0"},{"Kind":"device","DeviceID":"0192f0a0-0000-7000-8000-000000000c02"}]}],
	   "VisualRender":{"nodes":[]}}},
	{"Index":1,"Note":"","LinkedTo":"0192f0a0-0000-7000-8000-000000000a01","CopyOf":"0192f0a0-0000-7000-8000-000000000a01",
	 "Tasks":[],"Topology":{"VPN":{"Enabled":true,"DHCP":true},"Internet":{"Enabled":false,"DHCP":false}}},
	{"Index":2,"Tasks":[],"Topology":{"VPN":{"Enabled":false,"DHCP":false},"Internet":{"Enabled":false,"DHCP":false},"Devices":[]}}
	]}`
}

// bindDraft runs the request through gin's real ShouldBindJSON, as SaveDraft does.
func bindDraft(t *testing.T, body string) (saveDraftRequest, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/exercises/x/draft", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	var req saveDraftRequest
	err := ctx.ShouldBindJSON(&req)
	return req, err
}

func TestSaveDraftRequest_ProductionFrontendPayloadStillBinds(t *testing.T) {
	for _, envVars := range []int{0, 1, 63, 64} {
		req, err := bindDraft(t, prodFrontendDraft(envVars))
		if err != nil {
			t.Fatalf("%d env vars: the production frontend draft is refused: %v", envVars, err)
		}
		if len(req.Variants) != 3 {
			t.Fatalf("variants = %d", len(req.Variants))
		}
		for i, v := range req.Variants {
			domain := v.toDomain()
			if domain.Weight != 0 || domain.EffectiveWeight() != 1 {
				t.Fatalf("variant %d: weight %d / effective %d, want 0 / 1", i, domain.Weight, domain.EffectiveWeight())
			}
		}
	}
	// Above the new cap the same payload is refused with a 400 (the only new refusal).
	if _, err := bindDraft(t, prodFrontendDraft(65)); err == nil {
		t.Fatal("65 env vars on one device must be refused by the new cap")
	}
}

// What the editor reads back (GET draft) for an unweighted stored version, sent
// back unchanged as the production frontend would, binds and stays unweighted.
func TestSaveDraftRequest_RoundTripOfUnweightedVersionBinds(t *testing.T) {
	variants := []exerciseModel.Variant{
		{Index: 0, Tasks: []exerciseModel.Task{}, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "web", Type: exerciseModel.DeviceTypeContainer, EnvVars: []exerciseModel.EnvVar{{Name: "A", Value: "1"}, {Name: "S", Secret: true, Value: "s"}}}}}},
		{Index: 1, Tasks: []exerciseModel.Task{}},
	}
	encoded, err := json.Marshal(map[string]any{"AdminNote": "", "Variants": variantsToDTO(variants)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"Weight"`) {
		t.Fatalf("an unweighted version sends no Weight: %s", encoded)
	}
	req, err := bindDraft(t, string(encoded))
	if err != nil {
		t.Fatalf("round trip refused: %v\n%s", err, encoded)
	}
	for i, v := range req.Variants {
		if v.toDomain().EffectiveWeight() != 1 {
			t.Fatalf("variant %d not weight 1 after the round trip", i)
		}
	}
}

func TestSaveDraftRequest_NullAndEmptyVariantsBind(t *testing.T) {
	for _, body := range []string{`{"AdminNote":""}`, `{"AdminNote":"","Variants":null}`, `{"AdminNote":"","Variants":[]}`} {
		if _, err := bindDraft(t, body); err != nil {
			t.Fatalf("%s refused by binding: %v", body, err)
		}
	}
}
