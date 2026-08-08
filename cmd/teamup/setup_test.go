package main

import (
	"testing"
)

// docker-compose enables auto-setup but only sets the org/admin/OIDC variables;
// MinIO and LiveKit come from the ordinary runtime config. Auto-setup has to
// fall back to those or it builds an org with no storage and no calls.
func TestSetupDataFallsBackToRuntimeServiceConfig(t *testing.T) {
	cfg := Config{
		SetupOrgName:       "Demo Org",
		SetupOrgSubdomain:  "demo",
		SetupAdminEmail:    "admin@demo.local",
		SetupAdminPassword: "admin1234567890",
		SetupOIDCProvider:  "custom",

		MinIOEndpoint:    "minio:9000",
		MinIOAccessKey:   "minioadmin",
		MinIOSecretKey:   "minioadmin",
		MinIOBucket:      "teamup-files",
		LiveKitURL:       "http://livekit:7880",
		LiveKitAPIKey:    "devkey",
		LiveKitAPISecret: "devsecret",
	}

	data := setupDataFromConfig(cfg)

	if data.MinIOEndpoint != "minio:9000" || data.MinIOBucket != "teamup-files" {
		t.Errorf("minio config not carried over: %+v", data)
	}
	if data.LiveKitURL != "http://livekit:7880" || data.LiveKitAPISecret != "devsecret" {
		t.Errorf("livekit config not carried over: %+v", data)
	}
	if err := data.Validate(); err != nil {
		t.Fatalf("compose-style auto-setup should produce valid data, got: %v", err)
	}
}

// Explicit TEAMUP_SETUP_* values win over the runtime config.
func TestSetupDataPrefersExplicitSetupVars(t *testing.T) {
	cfg := Config{
		SetupOrgName:       "Demo Org",
		SetupOrgSubdomain:  "demo",
		SetupAdminEmail:    "admin@demo.local",
		SetupAdminPassword: "admin1234567890",
		SetupOIDCProvider:  "custom",
		SetupMinIOEndpoint: "setup-minio:9000",
		SetupMinIOBucket:   "setup-bucket",
		SetupLiveKitURL:    "http://setup-livekit:7880",
		SetupLiveKitAPIKey: "setupkey",

		MinIOEndpoint:    "minio:9000",
		MinIOAccessKey:   "minioadmin",
		MinIOSecretKey:   "minioadmin",
		MinIOBucket:      "teamup-files",
		LiveKitURL:       "http://livekit:7880",
		LiveKitAPIKey:    "devkey",
		LiveKitAPISecret: "devsecret",
	}

	data := setupDataFromConfig(cfg)

	if data.MinIOEndpoint != "setup-minio:9000" {
		t.Errorf("explicit setup endpoint should win, got %q", data.MinIOEndpoint)
	}
	if data.MinIOBucket != "setup-bucket" {
		t.Errorf("explicit setup bucket should win, got %q", data.MinIOBucket)
	}
	if data.LiveKitURL != "http://setup-livekit:7880" {
		t.Errorf("explicit setup livekit url should win, got %q", data.LiveKitURL)
	}
	// Unset setup vars still fall through to the runtime config.
	if data.MinIOAccessKey != "minioadmin" || data.LiveKitAPISecret != "devsecret" {
		t.Errorf("unset setup vars should fall back: %+v", data)
	}
}

// The admin's display name is NOT NULL in the schema and was never wired up.
func TestSetupDataAlwaysHasAnAdminName(t *testing.T) {
	cfg := Config{
		SetupOrgName:       "Demo Org",
		SetupOrgSubdomain:  "demo",
		SetupAdminEmail:    "admin@demo.local",
		SetupAdminPassword: "admin1234567890",
		SetupOIDCProvider:  "custom",
		MinIOEndpoint:      "minio:9000",
		MinIOAccessKey:     "minioadmin",
		MinIOSecretKey:     "minioadmin",
		MinIOBucket:        "teamup-files",
		LiveKitURL:         "http://livekit:7880",
		LiveKitAPIKey:      "devkey",
		LiveKitAPISecret:   "devsecret",
	}

	if got := setupDataFromConfig(cfg).AdminName; got == "" {
		t.Error("admin name must be defaulted when TEAMUP_SETUP_ADMIN_NAME is unset")
	}

	cfg.SetupAdminName = "Ada Lovelace"
	if got := setupDataFromConfig(cfg).AdminName; got != "Ada Lovelace" {
		t.Errorf("explicit admin name should be used, got %q", got)
	}
}

// A half-filled environment must fail loudly rather than create a broken org.
func TestSetupDataFromIncompleteConfigIsInvalid(t *testing.T) {
	cfg := Config{
		SetupOrgName:      "Demo Org",
		SetupOrgSubdomain: "demo",
		SetupOIDCProvider: "custom",
	}
	if err := setupDataFromConfig(cfg).Validate(); err == nil {
		t.Fatal("auto-setup with no admin credentials or services must not validate")
	}
}

func TestSetupDataCarriesOIDCCredentials(t *testing.T) {
	cfg := Config{
		SetupOIDCProvider:       "google",
		SetupGoogleClientID:     "gid",
		SetupGoogleClientSecret: "gsecret",
	}
	data := setupDataFromConfig(cfg)
	if data.GoogleClientID != "gid" || data.GoogleClientSecret != "gsecret" {
		t.Errorf("google OIDC credentials dropped: %+v", data)
	}
}

// Falls back to the runtime OIDC config the same way services do.
func TestSetupDataFallsBackToRuntimeOIDC(t *testing.T) {
	cfg := Config{
		SetupOIDCProvider:  "custom",
		CustomIssuer:       "https://id.example.test",
		CustomClientID:     "cid",
		CustomClientSecret: "csecret",
	}
	data := setupDataFromConfig(cfg)
	if data.CustomIssuer != "https://id.example.test" || data.CustomClientID != "cid" {
		t.Errorf("runtime OIDC config not used as fallback: %+v", data)
	}
}
