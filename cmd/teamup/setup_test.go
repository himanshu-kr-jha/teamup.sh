package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/teamup/teamup/internal/wizard"
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

// fakeSetupService stands in for the wizard. There is no object store in the
// test environment, so what runAutoSetup asks the wizard to do is what these
// tests observe — which is the whole defect: nobody was asking for a bucket.
type fakeSetupService struct {
	complete    bool
	finalizeErr error
	ensureErr   error

	finalized []wizard.Data
	ensured   []wizard.Data
}

func (f *fakeSetupService) IsSetupComplete(context.Context) (bool, error) {
	return f.complete, nil
}

func (f *fakeSetupService) FinalizeSetup(_ context.Context, data wizard.Data) error {
	f.finalized = append(f.finalized, data)
	return f.finalizeErr
}

func (f *fakeSetupService) EnsureObjectStorage(_ context.Context, data wizard.Data) error {
	f.ensured = append(f.ensured, data)
	return f.ensureErr
}

// autoSetupConfig is a docker-compose-shaped unattended install: the org and
// admin come from TEAMUP_SETUP_*, the services from the runtime config.
func autoSetupConfig() Config {
	return Config{
		AutoSetup:          true,
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
}

// The bucket is not part of what "setup complete" records, and an instance
// provisioned before anything created buckets — or whose bucket was deleted —
// has no other way back. Every boot repairs it.
func TestAutoSetupRepairsTheBucketOnAnAlreadyProvisionedInstance(t *testing.T) {
	svc := &fakeSetupService{complete: true}
	cfg := autoSetupConfig()

	if err := runAutoSetup(context.Background(), svc, cfg); err != nil {
		t.Fatalf("auto-setup on a provisioned instance: %v", err)
	}

	if len(svc.ensured) != 1 {
		t.Fatalf("expected one attempt to create the bucket, got %d", len(svc.ensured))
	}
	if svc.ensured[0].MinIOBucket != cfg.MinIOBucket {
		t.Errorf("bucket: got %q want %q", svc.ensured[0].MinIOBucket, cfg.MinIOBucket)
	}
	if len(svc.finalized) != 0 {
		t.Errorf("a provisioned instance must not be re-provisioned, got %d finalizes", len(svc.finalized))
	}
}

// ...but object storage being down must not stop an otherwise healthy instance
// from starting. /readyz reports the bucket separately, so it stays out of the
// load balancer until storage is back rather than crash-looping.
func TestAutoSetupKeepsBootingWhenTheObjectStoreIsDown(t *testing.T) {
	svc := &fakeSetupService{complete: true, ensureErr: errors.New("connection refused")}

	if err := runAutoSetup(context.Background(), svc, autoSetupConfig()); err != nil {
		t.Fatalf("a provisioned instance must still boot when storage is down, got: %v", err)
	}
	if len(svc.ensured) != 1 {
		t.Errorf("the bucket should still have been attempted, got %d attempts", len(svc.ensured))
	}
}

// Provisioning a fresh instance goes through FinalizeSetup, which creates the
// bucket as part of the same step; auto-setup does not do it twice.
func TestAutoSetupProvisionsAFreshInstance(t *testing.T) {
	svc := &fakeSetupService{}
	cfg := autoSetupConfig()

	if err := runAutoSetup(context.Background(), svc, cfg); err != nil {
		t.Fatalf("auto-setup: %v", err)
	}

	if len(svc.finalized) != 1 {
		t.Fatalf("expected one finalize, got %d", len(svc.finalized))
	}
	if svc.finalized[0].MinIOBucket != cfg.MinIOBucket {
		t.Errorf("bucket handed to finalize: got %q want %q", svc.finalized[0].MinIOBucket, cfg.MinIOBucket)
	}
	if len(svc.ensured) != 0 {
		t.Errorf("FinalizeSetup provisions the bucket; auto-setup should not repeat it, got %d", len(svc.ensured))
	}
}

// A fresh instance that cannot provision its storage is not a working
// instance: the error reaches the caller, which ends the boot.
func TestAutoSetupFailsWhenProvisioningFails(t *testing.T) {
	svc := &fakeSetupService{finalizeErr: errors.New(`create object storage bucket "teamup-files": connection refused`)}

	err := runAutoSetup(context.Background(), svc, autoSetupConfig())
	if err == nil {
		t.Fatal("provisioning that failed must be reported, not swallowed")
	}
	if !strings.Contains(err.Error(), "teamup-files") {
		t.Errorf("error should say what could not be created, got: %v", err)
	}
}
