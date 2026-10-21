package eventself

import (
	"encoding/json"
	"strings"
	"testing"

	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestPublicResultsFreezeCarriesConfiguredBannerAndLegacyDefaults(t *testing.T) {
	for _, tc := range []struct{ input, text, level string }{
		{`{}`, "", "warning"},
		{`{"Applied":true,"BannerText":"Підсумки згодом.","BannerLevel":"critical"}`, "Підсумки згодом.", "critical"},
		{`{"Active":false,"Applied":false,"BannerText":"Плановий банер.","BannerLevel":"critical"}`, "", "warning"},
		{`{"Active":true,"Applied":false,"BannerText":"Плановий банер.","BannerLevel":"info"}`, "", "warning"},
	} {
		var freeze eventUseCase.ResultsFreezeView
		if err := json.Unmarshal([]byte(tc.input), &freeze); err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(toResultsSnapshotResponse(eventUseCase.ResultsSnapshotView{Freeze: freeze}))
		if err != nil {
			t.Fatal(err)
		}
		text, _ := json.Marshal(tc.text)
		if !strings.Contains(string(body), `"BannerText":`+string(text)) || !strings.Contains(string(body), `"BannerLevel":"`+tc.level+`"`) {
			t.Errorf("public freeze banner lost: %s", body)
		}
		if strings.Contains(string(body), `"FreezeEndMode"`) {
			t.Errorf("moderator setting leaked: %s", body)
		}
	}
}
