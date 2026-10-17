package imageRegistry

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gofrs/uuid"

	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

var attID = uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000c1"))

func (f *fakeUseCase) EventImagesAvailability(_ context.Context, eventID uuid.UUID) ([]imageRegistryUseCase.EventExerciseImages, error) {
	f.availabilityEvent = eventID
	return []imageRegistryUseCase.EventExerciseImages{{
		EventExerciseID: attID, ExerciseID: secUID, ExerciseName: "Web",
		Unavailable: []imageRegistryModel.Availability{{Image: "ghcr.io/acme/x:1", Status: imageRegistryModel.StatusUnavailable, Reason: imageRegistryModel.ReasonUnauthorized}},
	}}, nil
}

func TestEventImagesAvailabilityRoute(t *testing.T) {
	uc := &fakeUseCase{}
	w := do(secretsEngine(uc), http.MethodGet, "/api/events/"+evA.String()+"/manage/images/availability", "")
	if w.Code != http.StatusOK || uc.availabilityEvent != evA {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var body struct {
		Data []struct {
			EventExerciseID, ExerciseID, ExerciseName string
			Unavailable                               []map[string]string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].EventExerciseID != attID.String() || body.Data[0].ExerciseName != "Web" ||
		len(body.Data[0].Unavailable) != 1 || body.Data[0].Unavailable[0]["Reason"] != "unauthorized" || body.Data[0].Unavailable[0]["Image"] != "ghcr.io/acme/x:1" {
		t.Fatalf("body %s", w.Body)
	}
}

// noReadUseCase: the caller cannot read the event.
type noReadUseCase struct{ *fakeUseCase }

func (noReadUseCase) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error {
	return imageRegistryModel.ErrEventSecretNotFound.Err()
}

func TestEventImagesAvailabilityNeedsRead(t *testing.T) {
	uc := &fakeUseCase{}
	w := do(secretsEngine(noReadUseCase{uc}), http.MethodGet, "/api/events/"+evA.String()+"/manage/images/availability", "")
	if w.Code != http.StatusNotFound || uc.availabilityEvent != uuid.Nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := do(secretsEngine(uc), http.MethodGet, "/api/events/nope/manage/images/availability", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", w.Code)
	}
}
