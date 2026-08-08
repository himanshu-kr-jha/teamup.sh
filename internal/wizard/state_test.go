package wizard

import (
	"context"
	"strings"
	"testing"
	"time"
)

func completeData() Data {
	return Data{
		OrgName:            "Acme Inc",
		OrgSubdomain:       "acme",
		AdminName:          "Ada Lovelace",
		AdminEmail:         "ada@acme.test",
		AdminPassword:      "correct-horse-battery",
		OIDCProvider:       "custom",
		CustomIssuer:       "https://id.acme.test",
		CustomClientID:     "cid",
		CustomClientSecret: "csecret",
		MinIOEndpoint:      "localhost:9000",
		MinIOAccessKey:     "minioadmin",
		MinIOSecretKey:     "minioadmin",
		MinIOBucket:        "teamup-files",
		LiveKitURL:         "http://localhost:7880",
		LiveKitAPIKey:      "devkey",
		LiveKitAPISecret:   "devsecret",
	}
}

// The bug that bricked setup: steps 1-4 validated their input and threw it away,
// then step 5 finalized an empty Data{}, inserting a blank organization.
func TestStateAccumulatesDataAcrossSteps(t *testing.T) {
	st := NewState()

	st.ApplyOrganization(OrganizationStep{OrgName: "Acme Inc", OrgSubdomain: "acme"})
	st.ApplyAdmin(AdminStep{AdminName: "Ada Lovelace", AdminEmail: "ada@acme.test", AdminPassword: "correct-horse-battery"})
	st.ApplyAuth(AuthStep{OIDCProvider: "custom", CustomIssuer: "https://id.acme.test", CustomClientID: "cid", CustomClientSecret: "csecret"})
	st.ApplyServices(ServicesStep{
		MinIOEndpoint: "localhost:9000", MinIOAccessKey: "minioadmin",
		MinIOSecretKey: "minioadmin", MinIOBucket: "teamup-files",
		LiveKitURL: "http://localhost:7880", LiveKitAPIKey: "devkey", LiveKitAPISecret: "devsecret",
	})

	if st.Data.OrgName != "Acme Inc" {
		t.Errorf("org name lost across steps: got %q", st.Data.OrgName)
	}
	if st.Data.AdminEmail != "ada@acme.test" {
		t.Errorf("admin email lost across steps: got %q", st.Data.AdminEmail)
	}
	if st.Data.MinIOBucket != "teamup-files" {
		t.Errorf("minio bucket lost across steps: got %q", st.Data.MinIOBucket)
	}
	if err := st.Data.Validate(); err != nil {
		t.Fatalf("data accumulated from all steps should be valid, got: %v", err)
	}
	for step := 1; step <= 4; step++ {
		if !st.CompletedSteps[step] {
			t.Errorf("step %d should be marked complete", step)
		}
	}
}

// A later step must not wipe data captured by an earlier one.
func TestApplyStepPreservesEarlierSteps(t *testing.T) {
	st := NewState()
	st.ApplyOrganization(OrganizationStep{OrgName: "Acme Inc", OrgSubdomain: "acme"})
	st.ApplyAdmin(AdminStep{AdminName: "Ada", AdminEmail: "ada@acme.test", AdminPassword: "correct-horse-battery"})

	if st.Data.OrgName != "Acme Inc" || st.Data.OrgSubdomain != "acme" {
		t.Fatalf("admin step clobbered organization data: %+v", st.Data)
	}
}

func TestValidateRejectsBlankOrganization(t *testing.T) {
	// Exactly the payload the old step5 handler sent to FinalizeSetup.
	if err := (Data{}).Validate(); err == nil {
		t.Fatal("empty Data must not validate — this is what created the blank org row")
	}
}

func TestValidateReportsEachMissingField(t *testing.T) {
	cases := map[string]func(*Data){
		"orgName":          func(d *Data) { d.OrgName = "" },
		"orgSubdomain":     func(d *Data) { d.OrgSubdomain = "" },
		"adminName":        func(d *Data) { d.AdminName = "" },
		"adminEmail":       func(d *Data) { d.AdminEmail = "" },
		"adminPassword":    func(d *Data) { d.AdminPassword = "" },
		"oidcProvider":     func(d *Data) { d.OIDCProvider = "" },
		"minioEndpoint":    func(d *Data) { d.MinIOEndpoint = "" },
		"minioBucket":      func(d *Data) { d.MinIOBucket = "" },
		"livekitUrl":       func(d *Data) { d.LiveKitURL = "" },
		"livekitApiSecret": func(d *Data) { d.LiveKitAPISecret = "" },
	}
	for field, blank := range cases {
		d := completeData()
		blank(&d)
		err := d.Validate()
		if err == nil {
			t.Errorf("%s: blank field must fail validation", field)
			continue
		}
		if !strings.Contains(err.Error(), field) {
			t.Errorf("%s: error should name the field, got %q", field, err.Error())
		}
	}
}

func TestValidateRejectsShortPassword(t *testing.T) {
	d := completeData()
	d.AdminPassword = "short"
	if err := d.Validate(); err == nil {
		t.Fatal("password under 12 chars must be rejected")
	}
}

func TestValidateRejectsUnknownOIDCProvider(t *testing.T) {
	d := completeData()
	d.OIDCProvider = "facebook"
	if err := d.Validate(); err == nil {
		t.Fatal("unknown OIDC provider must be rejected")
	}
}

func TestValidateAcceptsCompleteData(t *testing.T) {
	if err := completeData().Validate(); err != nil {
		t.Fatalf("complete data should validate, got: %v", err)
	}
}

// State must survive a round trip through the session store, or the wizard
// forgets everything between HTTP requests.
func TestSessionStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()

	st := NewState()
	st.ApplyOrganization(OrganizationStep{OrgName: "Acme Inc", OrgSubdomain: "acme"})

	id, err := store.Save(ctx, "", st, time.Minute)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if id == "" {
		t.Fatal("save must return a session id")
	}

	loaded, found, err := store.Load(ctx, id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !found {
		t.Fatal("a saved session must be reported as found")
	}
	if loaded.Data.OrgName != "Acme Inc" {
		t.Errorf("round trip lost data: %+v", loaded.Data)
	}
	if !loaded.CompletedSteps[1] {
		t.Error("round trip lost completed steps")
	}
}

func TestSessionStoreSaveReusesSessionID(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()

	id, err := store.Save(ctx, "", NewState(), time.Minute)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	same, err := store.Save(ctx, id, NewState(), time.Minute)
	if err != nil {
		t.Fatalf("resave: %v", err)
	}
	if same != id {
		t.Errorf("saving with an existing id must keep it: got %q want %q", same, id)
	}
}

func TestSessionStoreLoadUnknownIDStartsFresh(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()

	loaded, found, err := store.Load(ctx, "does-not-exist")
	if err != nil {
		t.Fatalf("unknown id should not error, got: %v", err)
	}
	if found {
		t.Error("an id we never issued must not be reported as found")
	}
	if loaded == nil {
		t.Fatal("unknown id should yield a fresh state, not nil")
	}
	if loaded.Data.OrgName != "" {
		t.Error("fresh state should be empty")
	}
}

func TestSessionStoreDeleteClearsState(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()

	st := NewState()
	st.ApplyOrganization(OrganizationStep{OrgName: "Acme Inc", OrgSubdomain: "acme"})
	id, _ := store.Save(ctx, "", st, time.Minute)

	if err := store.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	loaded, found, _ := store.Load(ctx, id)
	if found {
		t.Error("session should not be found after delete")
	}
	if loaded.Data.OrgName != "" {
		t.Error("state should be gone after delete")
	}
}

func TestValidateRejectsMalformedSubdomain(t *testing.T) {
	// The unattended TEAMUP_SETUP_* path only goes through Validate, so a bad
	// subdomain must be caught here rather than by the VARCHAR(63) column.
	for _, bad := range []string{"My Org!", "-leading", "trailing-", "UPPER", strings.Repeat("a", 64)} {
		d := completeData()
		d.OrgSubdomain = bad
		if err := d.Validate(); err == nil {
			t.Errorf("subdomain %q must be rejected", bad)
		}
	}
	for _, good := range []string{"acme", "acme-corp", "a", "a1-b2", strings.Repeat("a", 63)} {
		d := completeData()
		d.OrgSubdomain = good
		if err := d.Validate(); err != nil {
			t.Errorf("subdomain %q should be accepted, got: %v", good, err)
		}
	}
}
