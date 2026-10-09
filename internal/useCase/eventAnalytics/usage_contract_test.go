package eventAnalytics

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/gofrs/uuid"
)

func TestUsageMissingParticipantDatesAreNull(t *testing.T) {
	user := uuid.UUID{15: 1}
	view := buildUsage([]eventAnalyticsRepo.UsageUser{{UserID: user}}, nil, nil, nil,
		[]eventAnalyticsRepo.UsageTouch{{UserID: user, Surface: "vpn", LabInitiatedAttempts: 7, BytesIn: 300}}, time.Unix(100, 0).UTC())
	if len(view.Users) != 1 || len(view.Users[0].Labs) != 1 {
		t.Fatal(view)
	}
	encoded, err := json.Marshal(view.Users[0].Labs[0])
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(encoded, &row); err != nil {
		t.Fatal(err)
	}
	if row["FirstAt"] != nil || row["LastAt"] != nil {
		t.Fatalf("unknown activity serialized as dates: %s", encoded)
	}
	if row["Attempts"] != float64(0) || row["LabInitiatedAttempts"] != float64(7) {
		t.Fatalf("lab-initiated and participant counters combined: %s", encoded)
	}
	if view.Users[0].LastLabAt != nil || view.Users[0].VPN.Online {
		t.Fatal("lab traffic fabricated user access/online state")
	}
}
