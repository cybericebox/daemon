package exercise_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	exerciseHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/exercise"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/internal/model/rbac"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

// imageUploadCalls records what the image upload routes passed to the use case.
type imageUploadCalls struct {
	calls      int
	exerciseID uuid.UUID
	digests    []string
	missing    []string
	pushed     imageRegistryUseCase.PushBlobInput
	created    imageRegistryUseCase.CreateImageInput
	reference  string
}

func (f *fakeUC) MissingBlobs(_ context.Context, exerciseID uuid.UUID, digests []string) ([]string, error) {
	f.imageUpload.calls++
	f.imageUpload.exerciseID, f.imageUpload.digests = exerciseID, digests
	return f.imageUpload.missing, f.err
}

func (f *fakeUC) PushStagedBlob(_ context.Context, exerciseID uuid.UUID, in imageRegistryUseCase.PushBlobInput) error {
	f.imageUpload.calls++
	f.imageUpload.exerciseID, f.imageUpload.pushed = exerciseID, in
	return f.err
}

func (f *fakeUC) CreateImage(_ context.Context, exerciseID uuid.UUID, in imageRegistryUseCase.CreateImageInput) (imageRegistryUseCase.CreateImageResult, error) {
	f.imageUpload.calls++
	f.imageUpload.exerciseID, f.imageUpload.created = exerciseID, in
	return imageRegistryUseCase.CreateImageResult{Reference: f.imageUpload.reference}, f.err
}

func post(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// errBoom is a registry transport failure (classified as unreachable).
var errBoom = errors.New("dial tcp: connection refused")

var (
	cfgDigest   = "sha256:" + strings.Repeat("1", 64)
	layerDigest = "sha256:" + strings.Repeat("2", 64)
)

func TestMissingBlobsRoute(t *testing.T) {
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{imageUpload: imageUploadCalls{missing: []string{layerDigest}}}
	w := post(newEngine(uc, uuid.Must(uuid.NewV7())), "/api/exercises/"+exID.String()+"/images/blobs/missing",
		`{"Digests":["`+cfgDigest+`","`+layerDigest+`"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if uc.imageUpload.exerciseID != exID || len(uc.imageUpload.digests) != 2 {
		t.Fatalf("calls %+v", uc.imageUpload)
	}
	if !strings.Contains(w.Body.String(), `"Data":{"Missing":["`+layerDigest+`"]}`) {
		t.Fatalf("body %s", w.Body.String())
	}
}

// Nothing missing is an empty list, never null.
func TestMissingBlobsRouteEmptyIsAList(t *testing.T) {
	uc := &fakeUC{}
	w := post(newEngine(uc, uuid.Must(uuid.NewV7())), "/api/exercises/"+uuid.Must(uuid.NewV7()).String()+"/images/blobs/missing", `{"Digests":["`+cfgDigest+`"]}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Data":{"Missing":[]}`) {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestPushBlobRoute(t *testing.T) {
	exID, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUC{}
	w := post(newEngine(uc, uuid.Must(uuid.NewV7())), "/api/exercises/"+exID.String()+"/images/blobs",
		`{"FileID":"`+fileID.String()+`","Digest":"`+layerDigest+`","Size":42}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	want := imageRegistryUseCase.PushBlobInput{FileID: fileID, Digest: layerDigest, Size: 42}
	if uc.imageUpload.exerciseID != exID || uc.imageUpload.pushed != want {
		t.Fatalf("pushed %+v", uc.imageUpload.pushed)
	}
}

func TestCreateImageRoute(t *testing.T) {
	exID := uuid.Must(uuid.NewV7())
	ref := "platform:web@sha256:" + strings.Repeat("3", 64)
	uc := &fakeUC{imageUpload: imageUploadCalls{reference: ref}}
	w := post(newEngine(uc, uuid.Must(uuid.NewV7())), "/api/exercises/"+exID.String()+"/images", `{"Name":"web",`+
		`"Config":{"MediaType":"application/vnd.oci.image.config.v1+json","Digest":"`+cfgDigest+`","Size":10},`+
		`"Layers":[{"MediaType":"application/vnd.oci.image.layer.v1.tar","Digest":"`+layerDigest+`","Size":20}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct{ Data struct{ Reference string } }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Data.Reference != ref {
		t.Fatalf("body %s", w.Body.String())
	}
	c := uc.imageUpload.created
	if uc.imageUpload.exerciseID != exID || c.Name != "web" ||
		c.Config != (imageRegistryUseCase.Descriptor{MediaType: "application/vnd.oci.image.config.v1+json", Digest: cfgDigest, Size: 10}) ||
		len(c.Layers) != 1 || c.Layers[0] != (imageRegistryUseCase.Descriptor{MediaType: "application/vnd.oci.image.layer.v1.tar", Digest: layerDigest, Size: 20}) {
		t.Fatalf("created %+v", c)
	}
}

func TestImageUploadRoutesRefuseBadBodies(t *testing.T) {
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	base := "/api/exercises/" + exID.String() + "/images"
	cases := map[string]string{
		base + "/blobs/missing": `{"Digests":[]}`,
		base + "/blobs":         `{"FileID":"nope","Digest":"` + layerDigest + `","Size":1}`,
		base:                    `{"Name":"web","Config":{"MediaType":"a","Digest":"` + cfgDigest + `","Size":1},"Layers":[]}`,
	}
	for path, body := range cases {
		if w := post(r, path, body); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d", path, w.Code)
		}
	}
	if uc.imageUpload.calls != 0 {
		t.Fatal("a bad body reached the use case")
	}
}

// Without edit access to the exercise none of the routes reach the use case.
func TestImageUploadRoutesNeedEditAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	uc := &fakeUC{}
	exerciseHandler.NewExerciseAPIHandler(uc, roleProt{role: rbac.RoleUser}).Init(r.Group("api"))
	base := "/api/exercises/" + uuid.Must(uuid.NewV7()).String() + "/images"
	for path, body := range map[string]string{
		base + "/blobs/missing": `{"Digests":["` + cfgDigest + `"]}`,
		base + "/blobs":         `{"FileID":"` + uuid.Must(uuid.NewV7()).String() + `","Digest":"` + layerDigest + `","Size":1}`,
		base:                    `{"Name":"web","Config":{"MediaType":"a","Digest":"` + cfgDigest + `","Size":1},"Layers":[{"MediaType":"b","Digest":"` + layerDigest + `","Size":1}]}`,
	} {
		if w := post(r, path, body); w.Code != http.StatusForbidden {
			t.Fatalf("%s: status %d", path, w.Code)
		}
	}
	if uc.imageUpload.calls != 0 {
		t.Fatal("the use case was reached")
	}
}

// The registry refusal reaches the editor as 409 with the reason in the public context.
func TestImageUploadRefusalCarriesTheReason(t *testing.T) {
	uc := &fakeUC{err: imageRegistryUseCase.Unavailable(errBoom, "push")}
	w := post(newEngine(uc, uuid.Must(uuid.NewV7())), "/api/exercises/"+uuid.Must(uuid.NewV7()).String()+"/images/blobs/missing", `{"Digests":["`+cfgDigest+`"]}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Status struct {
			Context map[string]string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Status.Context[imageRegistryModel.ContextReason] != "unreachable" || body.Status.Context[imageRegistryModel.ContextToken] != "push" {
		t.Fatalf("body %s", w.Body.String())
	}
}
