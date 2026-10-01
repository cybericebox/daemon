package config

import (
	"testing"
)

func TestRecaptchaConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     RecaptchaConfig
		wantErr bool
	}{
		{
			name:    "enterprise complete (ProjectID+APIKey+SiteKey)",
			cfg:     RecaptchaConfig{ProjectID: "proj", APIKey: "apikey", SiteKey: "sitekey"},
			wantErr: false,
		},
		{
			name:    "enterprise missing APIKey",
			cfg:     RecaptchaConfig{ProjectID: "proj", SiteKey: "sitekey"},
			wantErr: true,
		},
		{
			name:    "enterprise missing SiteKey",
			cfg:     RecaptchaConfig{ProjectID: "proj", APIKey: "apikey"},
			wantErr: true,
		},
		{
			name:    "classic (SecretKey only)",
			cfg:     RecaptchaConfig{SecretKey: "secret"},
			wantErr: false,
		},
		{
			name:    "all empty — neither mode configured",
			cfg:     RecaptchaConfig{},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
		})
	}
}
