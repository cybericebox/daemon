package eventAnalytics

import (
	"encoding/json"
	"testing"

	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

func TestUsageLabContractKeepsLabInitiativesAndUnknownDates(t *testing.T) {
	v := eventAnalyticsUseCase.UsageView{Available: true, Users: []eventAnalyticsUseCase.UsageUserView{{
		Labs: []eventAnalyticsUseCase.UsageLabView{{Surface: "vpn", LabInitiatedAttempts: 7, BytesIn: 300}},
	}}}
	encoded, err := json.Marshal(toUsageResponse(v))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Users []struct{ Labs []map[string]any }
	}
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	row := response.Users[0].Labs[0]
	if row["Attempts"] != float64(0) || row["LabInitiatedAttempts"] != float64(7) || row["FirstAt"] != nil || row["LastAt"] != nil {
		t.Fatalf("lab-only contract changed into participant activity: %s", encoded)
	}
}
