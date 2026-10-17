package exercise

import (
	"errors"
	"strings"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/gofrs/uuid"
)

const v2Header = `{"schema":"cib-exercise/1",`

func publicContext(t *testing.T, err error) map[string]any {
	t.Helper()
	var withContext interface{ PublicContext() map[string]any }
	if !errors.As(err, &withContext) {
		t.Fatalf("error %v has no public context", err)
	}
	return withContext.PublicContext()
}

func mustDecodeV2(t *testing.T, payload string) archiveDocV2 {
	t.Helper()
	doc, err := decodeArchiveV2([]byte(payload))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return doc
}

func TestDecodeArchiveV2_MapsEveryEnumByName(t *testing.T) {
	doc := mustDecodeV2(t, v2Header+`"exercise":{"name":"Sample"},"versions":[{"status":"published","variants":[{"tasks":[{"name":"t","difficulty":"hard","hints":[{"text":"h","level":"steps"}]}],"topology":{"devices":[{"name":"web","type":"container","security_preset":"net","interfaces":[{"name":"eth0","ip":{"type":"static","addresses":["10.0.0.2/24"]}}]}],"connections":[{"endpoints":[{"kind":"vpn"},{"kind":"device","device":"web","interface":"eth0"}]}]}}]}]}`)
	parsed, err := doc.toParsed(nil)
	if err != nil {
		t.Fatalf("toParsed: %v", err)
	}
	version := parsed.Versions[0]
	variant := version.Variants[0]
	device := variant.Topology.Devices[0]
	checks := []struct {
		name      string
		got, want string
	}{
		{"status", string(version.Status), string(exerciseModel.VersionStatusPublished)},
		{"difficulty", string(variant.Tasks[0].Difficulty), string(exerciseModel.DifficultyHard)},
		{"hint level", string(variant.Tasks[0].Hints[0].Level), string(exerciseModel.HintLevelSteps)},
		{"device type", string(device.Type), string(exerciseModel.DeviceTypeContainer)},
		{"security preset", string(device.SecurityPreset), string(exerciseModel.SecurityPresetNet)},
		{"ip type", string(device.Interfaces[0].IP.Type), string(exerciseModel.IPConfigTypeStatic)},
		{"endpoint kind", string(variant.Topology.Connections[0].Endpoints[0].Kind), string(exerciseModel.EndpointVPN)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestDecodeArchiveV2_RejectsIntegerEnum(t *testing.T) {
	_, err := decodeArchiveV2([]byte(v2Header + `"exercise":{"name":"x"},"versions":[{"variants":[{"tasks":[{"name":"t","difficulty":3}]}]}]}`))
	if !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		t.Fatalf("err = %v, want ErrArchiveInvalid", err)
	}
}

func TestDecodeArchiveV2_UnknownKeyNamed(t *testing.T) {
	_, err := decodeArchiveV2([]byte(v2Header + `"exercise":{"Name":"x"}}`))
	if !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		t.Fatalf("err = %v, want ErrArchiveInvalid", err)
	}
	if !strings.Contains(err.Error(), "Name") || !strings.Contains(publicContext(t, err)["reason"].(string), "Name") {
		t.Fatalf("the unknown key must be named: %v", err)
	}
}

func TestDecodeArchiveV2_StrictShape(t *testing.T) {
	for name, payload := range map[string]string{
		"trailing data":  v2Header + `"exercise":{"name":"x"}} {}`,
		"unknown nested": v2Header + `"exercise":{"name":"x"},"versions":[{"variants":[{"linked_to":1}]}]}`,
		"not json":       `schema: cib-exercise/1`,
		"wrong schema":   `{"schema":"cib-exercise/2","exercise":{"name":"x"}}`,
		"missing schema": `{"exercise":{"name":"x"}}`,
	} {
		if _, err := decodeArchiveV2([]byte(payload)); !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
			t.Errorf("%s: err = %v, want ErrArchiveInvalid", name, err)
		}
	}
}

func TestToParsed_ResolvesDeviceNames(t *testing.T) {
	const doc = v2Header + `"exercise":{"name":"x"},"versions":[{"variants":[{"tasks":[{"name":"t","difficulty":"easy","linked_device":"%s"}],"topology":{"devices":[{"name":"web","type":"container"}],"connections":[{"endpoints":[{"kind":"device","device":"web","interface":"eth0"},{"kind":"vpn"}]}]}}]}]}`
	parsed, err := mustDecodeV2(t, strings.Replace(doc, "%s", "web", 1)).toParsed(nil)
	if err != nil {
		t.Fatalf("toParsed: %v", err)
	}
	variant := parsed.Versions[0].Variants[0]
	deviceID := variant.Topology.Devices[0].ID
	if deviceID == uuid.Nil {
		t.Fatal("device id not generated")
	}
	if got := variant.Topology.Connections[0].Endpoints[0].DeviceID; got != deviceID {
		t.Fatalf("endpoint device = %v, want %v", got, deviceID)
	}
	linked := variant.Tasks[0].LinkedDeviceID
	if !linked.Valid || linked.UUID != deviceID {
		t.Fatalf("linked device = %+v, want %v", linked, deviceID)
	}
	if variant.ID == uuid.Nil {
		t.Fatal("id-less variant gets a fresh id")
	}
	_, err = mustDecodeV2(t, strings.Replace(doc, "%s", "nope", 1)).toParsed(nil)
	if !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		t.Fatalf("unknown device err = %v, want ErrArchiveInvalid", err)
	}
}

func TestToParsed_IgnoresTaskAndVersionIDs(t *testing.T) {
	const id = "0192f0a0-0000-7000-8000-000000000001"
	doc := mustDecodeV2(t, `{"schema":"cib-exercise/1","exercise":{"id":"`+id+`","name":"x"},"versions":[{"id":"`+id+`","variants":[{"id":"`+id+`","tasks":[{"id":"`+id+`","name":"t","difficulty":"easy"}]}]}]}`)
	parsed, err := doc.toParsed(nil)
	if err != nil {
		t.Fatalf("toParsed: %v", err)
	}
	if parsed.Exercise.ID != uuid.Nil || parsed.Versions[0].ID != uuid.Nil {
		t.Fatalf("exercise/version ids must be ignored: %v %v", parsed.Exercise.ID, parsed.Versions[0].ID)
	}
	variant := parsed.Versions[0].Variants[0]
	if variant.ID != uuid.FromStringOrNil(id) {
		t.Fatalf("variant id = %v, want the given one", variant.ID)
	}
	if variant.Tasks[0].ID != uuid.FromStringOrNil(id) {
		t.Fatalf("task id = %v, want the given one", variant.Tasks[0].ID)
	}
	bad := mustDecodeV2(t, `{"schema":"cib-exercise/1","exercise":{"name":"x"},"versions":[{"variants":[{"id":"not-a-uuid"}]}]}`)
	if _, err = bad.toParsed(nil); !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		t.Fatalf("bad variant id err = %v, want ErrArchiveInvalid", err)
	}
}

func TestToParsed_ImageFileFormReserved(t *testing.T) {
	build := func(image string) (archiveDocV2, error) {
		return decodeArchiveV2([]byte(v2Header + `"exercise":{"name":"x"},"versions":[{"variants":[{"topology":{"devices":[{"name":"web","type":"container","image":` + image + `}]}}]}]}`))
	}
	doc, err := build(`{"file":"images/web.tar"}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	_, err = doc.toParsed(nil)
	if !errors.Is(err, exerciseModel.ErrArchiveImageFileUnsupported.Err()) {
		t.Fatalf("err = %v, want ErrArchiveImageFileUnsupported", err)
	}
	if got := publicContext(t, err)["device"]; got != "web" {
		t.Fatalf("context device = %v, want web", got)
	}
	if _, err = build(`{"file":"x","tag":"y"}`); !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) || !strings.Contains(err.Error(), "image") {
		t.Fatalf("extra image key err = %v, want ErrArchiveInvalid naming image", err)
	}
	if _, err = build(`{"file":3}`); !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		t.Fatalf("non-string file err = %v, want ErrArchiveInvalid", err)
	}
	doc, err = build(`"nginx:1.27"`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	parsed, err := doc.toParsed(nil)
	if err != nil {
		t.Fatalf("toParsed: %v", err)
	}
	if got := parsed.Versions[0].Variants[0].Topology.Devices[0].Image; got != "nginx:1.27" {
		t.Fatalf("image = %q", got)
	}
}

func TestToParsed_VisualRenderOpaque(t *testing.T) {
	const render = `{"nodes":[{"x":1.50,"y":2}],  "z":null}`
	doc := mustDecodeV2(t, v2Header+`"exercise":{"name":"x"},"versions":[{"variants":[{"topology":{"visual_render":`+render+`}}]}]}`)
	parsed, err := doc.toParsed(nil)
	if err != nil {
		t.Fatalf("toParsed: %v", err)
	}
	if got := string(parsed.Versions[0].Variants[0].Topology.VisualRender); got != render {
		t.Fatalf("visual_render = %s, want %s", got, render)
	}
}

func TestArchiveImageV2_MarshalWritesString(t *testing.T) {
	out, err := archiveImageV2{Ref: "nginx:1"}.MarshalJSON()
	if err != nil || string(out) != `"nginx:1"` {
		t.Fatalf("marshal = %s, %v", out, err)
	}
}
