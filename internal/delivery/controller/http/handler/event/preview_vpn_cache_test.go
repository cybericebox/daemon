package event_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

type previewVPNUC struct {
	accessContractUC
	config string
	err    error
}

func (u *previewVPNUC) PreviewVPNConfig(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (string, error) {
	return u.config, u.err
}

// The preview VPN answer carries a WireGuard private key: no cache may keep it, error or not.
func TestPreviewVPNAnswerIsNeverCached(t *testing.T) {
	actor, event, selection := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	url := "/api/events/" + event.String() + "/manage/participant-preview/selections/" + selection.String() + "/vpn"
	for name, uc := range map[string]*previewVPNUC{
		"config": {config: "[Interface]\nPrivateKey = secret"},
		"error":  {err: errors.New("lab is gone")},
	} {
		t.Run(name, func(t *testing.T) {
			router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: uc}, actor)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, url, nil))
			require.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
			if name == "config" {
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Contains(t, recorder.Body.String(), "PrivateKey")
			}
		})
	}
}
