package config_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/cybericebox/daemon/internal/config"
)

// TestSiteLinkVectors runs the shared value rule for the optional addresses. The file is an identical copy of
// tests/cfg-vectors.json of the ID frontend (its shell and TypeScript implementations run the same file); the
// "flag" vectors do not apply to the backend.
func TestSiteLinkVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/cfg-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		URL []struct {
			Value string `json:"value"`
			Valid bool   `json:"valid"`
		} `json:"url"`
	}
	if err = json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.URL) == 0 {
		t.Fatal("no url vectors")
	}
	for _, v := range vectors.URL {
		c := config.AuthConfig{MainSiteURL: v.Value, TermsURL: v.Value, PrivacyURL: v.Value}
		if err := c.ValidateSiteLinks(); (err == nil) != v.Valid {
			t.Errorf("%q: valid=%v, vectors say %v (err=%v)", v.Value, err == nil, v.Valid, err)
		}
	}
}
