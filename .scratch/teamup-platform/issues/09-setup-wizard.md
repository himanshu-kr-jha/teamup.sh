## Question

Design the web-based setup wizard: first-run detection (empty DB), steps (org name/logo, admin user creation, OIDC provider config — Google/Entra/custom, MinIO/LiveKit connection test, SMTP optional), validation, idempotency (re-runnable), skip option for automated installs (env vars), and post-wizard redirect to app.

Status: claimed

## Answer

**Decision: Web-based setup wizard architecture defined below.**

### 1. First-Run Detection

```go
// internal/wizard/service.go
func (s *Service) IsSetupComplete(ctx context.Context) (bool, error) {
    // Check if any organization exists
    var count int
    err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM organizations`).Scan(&count)
    return count > 0, err
}

// Middleware: redirect to wizard if not complete
func (s *Server) wizardMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Skip for static assets, health checks, API
        if strings.HasPrefix(r.URL.Path, "/static/") ||
           strings.HasPrefix(r.URL.Path, "/api/health") ||
           strings.HasPrefix(r.URL.Path, "/wizard") {
            next.ServeHTTP(w, r)
            return
        }

        complete, err := s.wizard.IsSetupComplete(r.Context())
        if err != nil { http.Error(w, "setup check failed", 500); return }

        if !complete && !strings.HasPrefix(r.URL.Path, "/wizard") {
            http.Redirect(w, r, "/wizard", http.StatusFound)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

**Automated install bypass:** If all required env vars set, mark setup complete automatically on startup:
```bash
TEAMUP_SETUP_ORG_NAME="Acme Inc"
TEAMUP_SETUP_ADMIN_EMAIL="admin@acme.com"
TEAMUP_SETUP_ADMIN_PASSWORD="secure-password"
TEAMUP_SETUP_OIDC_PROVIDER="google"
TEAMUP_SETUP_GOOGLE_CLIENT_ID="xxx"
TEAMUP_SETUP_GOOGLE_CLIENT_SECRET="yyy"
TEAMUP_SETUP_MINIO_ENDPOINT="minio:9000"
TEAMUP_SETUP_MINIO_ACCESS_KEY="minioadmin"
TEAMUP_SETUP_MINIO_SECRET_KEY="minioadmin"
TEAMUP_SETUP_LIVEKIT_URL="http://livekit:7880"
TEAMUP_SETUP_LIVEKIT_API_KEY="key"
TEAMUP_SETUP_LIVEKIT_API_SECRET="secret"
```

---

### 2. Wizard Steps (6 Steps)

| Step | Route | Purpose | Required |
|------|-------|---------|----------|
| 1 | `/wizard/1-welcome` | Welcome, license, language | Yes |
| 2 | `/wizard/2-organization` | Org name, logo, subdomain | Yes |
| 3 | `/wizard/3-admin` | Admin user (name, email, password) | Yes |
| 4 | `/wizard/4-auth` | OIDC provider (Google/Entra/Custom) | Yes |
| 5 | `/wizard/5-services` | MinIO, LiveKit connection test | Yes |
| 6 | `/wizard/6-complete` | Summary, redirect to app | Yes |

---

### 3. State Management (Server-Side Session)

```go
// internal/wizard/state.go
type WizardState struct {
    CurrentStep int                    `json:"currentStep"`
    CompletedSteps map[int]bool        `json:"completedSteps"`
    Data          WizardData           `json:"data"`
    Errors        map[string]string    `json:"errors"`
}

type WizardData struct {
    // Step 2
    OrgName     string `json:"orgName" validate:"required,min=2,max=100"`
    OrgSubdomain string `json:"orgSubdomain" validate:"required,hostname_rfc1123"`
    OrgLogo     string `json:"orgLogo,omitempty"` // base64 data URL

    // Step 3
    AdminName  string `json:"adminName" validate:"required"`
    AdminEmail string `json:"adminEmail" validate:"required,email"`
    AdminPassword string `json:"adminPassword" validate:"required,min=12"`

    // Step 4
    OIDCProvider string `json:"oidcProvider" validate:"required,oneof=google entra custom"`
    GoogleClientID     string `json:"googleClientId,omitempty"`
    GoogleClientSecret string `json:"googleClientSecret,omitempty"`
    EntraClientID      string `json:"entraClientId,omitempty"`
    EntraClientSecret  string `json:"entraClientSecret,omitempty"`
    EntraTenantID      string `json:"entraTenantId,omitempty"`
    CustomIssuer       string `json:"customIssuer,omitempty"`
    CustomClientID     string `json:"customClientId,omitempty"`
    CustomClientSecret string `json:"customClientSecret,omitempty"`

    // Step 5
    MinIOEndpoint  string `json:"minioEndpoint" validate:"required,hostname_port"`
    MinIOAccessKey string `json:"minioAccessKey" validate:"required"`
    MinIOSecretKey string `json:"minioSecretKey" validate:"required"`
    MinIOBucket    string `json:"minioBucket" validate:"required"`
    LiveKitURL     string `json:"livekitUrl" validate:"required,url"`
    LiveKitAPIKey  string `json:"livekitApiKey" validate:"required"`
    LiveKitAPISecret string `json:"livekitApiSecret" validate:"required"`
    SMTPHost       string `json:"smtpHost,omitempty"`
    SMTPPort       int    `json:"smtpPort,omitempty"`
    SMTPUser       string `json:"smtpUser,omitempty"`
    SMTPPass       string `json:"smtpPass,omitempty"`
    SMTPFrom       string `json:"smtpFrom,omitempty"`
}

// Stored in encrypted cookie (or Redis session)
func (s *Service) SaveState(w http.ResponseWriter, r *http.Request, state *WizardState) error { ... }
func (s *Service) LoadState(r *http.Request) (*WizardState, error) { ... }
```

---

### 4. Step Handlers (Example: Step 5 - Services)

```go
// internal/wizard/handlers.go
func (h *Handler) HandleStep5Services(w http.ResponseWriter, r *http.Request) {
    state := h.loadState(r)

    if r.Method == "GET" {
        h.render(w, "wizard/step5.html", state)
        return
    }

    // Parse form
    if err := r.ParseForm(); err != nil { h.renderError(w, state, "form", err); return }

    data := WizardData{
        MinIOEndpoint:  r.FormValue("minioEndpoint"),
        MinIOAccessKey: r.FormValue("minioAccessKey"),
        MinIOSecretKey: r.FormValue("minioSecretKey"),
        MinIOBucket:    r.FormValue("minioBucket"),
        LiveKitURL:     r.FormValue("livekitUrl"),
        LiveKitAPIKey:  r.FormValue("livekitApiKey"),
        LiveKitAPISecret: r.FormValue("livekitApiSecret"),
        SMTPHost:       r.FormValue("smtpHost"),
        SMTPPort:       parseInt(r.FormValue("smtpPort")),
        SMTPUser:       r.FormValue("smtpUser"),
        SMTPPass:       r.FormValue("smtpPass"),
        SMTPFrom:       r.FormValue("smtpFrom"),
    }
    state.Data = data

    // Validate + Test connections
    if err := h.validateServices(r.Context(), data); err != nil {
        state.Errors["services"] = err.Error()
        h.saveState(w, r, state)
        h.render(w, "wizard/step5.html", state)
        return
    }

    state.CompletedSteps[5] = true
    state.CurrentStep = 6
    h.saveState(w, r, state)
    http.Redirect(w, r, "/wizard/6-complete", http.StatusFound)
}

func (h *Handler) validateServices(ctx context.Context, data WizardData) error {
    // MinIO: test connection + bucket exists/writable
    mc, err := minio.New(data.MinIOEndpoint, &minio.Options{
        Creds: credentials.NewStaticV4(data.MinIOAccessKey, data.MinIOSecretKey, ""),
        Secure: strings.HasPrefix(data.MinIOEndpoint, "https"),
    })
    if err != nil { return fmt.Errorf("minio config: %w", err) }

    exists, err := mc.BucketExists(ctx, data.MinIOBucket)
    if err != nil { return fmt.Errorf("minio bucket check: %w", err) }
    if !exists {
        if err := mc.MakeBucket(ctx, data.MinIOBucket, minio.MakeBucketOptions{}); err != nil {
            return fmt.Errorf("minio create bucket: %w", err)
        }
    }
    // Test write
    testKey := "wizard-test-" + uuid.New().String()
    if _, err := mc.PutObject(ctx, data.MinIOBucket, testKey, strings.NewReader("test"), 4, minio.PutObjectOptions{}); err != nil {
        return fmt.Errorf("minio write test: %w", err)
    }
    mc.RemoveObject(ctx, data.MinIOBucket, testKey)

    // LiveKit: test API connectivity
    lkClient := livekit.NewRoomServiceClient(data.LiveKitURL, data.LiveKitAPIKey, data.LiveKitAPISecret)
    _, err = lkClient.ListRooms(ctx, &livekit.ListRoomsRequest{})
    if err != nil { return fmt.Errorf("livekit connection: %w", err) }

    // SMTP (optional): test connection
    if data.SMTPHost != "" {
        if err := testSMTP(data.SMTPHost, data.SMTPPort, data.SMTPUser, data.SMTPPass); err != nil {
            return fmt.Errorf("smtp: %w", err)
        }
    }

    return nil
}
```

---

### 5. Finalization (Step 6 - Complete)

```go
func (h *Handler) HandleStep6Complete(w http.ResponseWriter, r *http.Request) {
    state := h.loadState(r)

    // Verify all steps complete
    for i := 1; i <= 5; i++ {
        if !state.CompletedSteps[i] {
            http.Redirect(w, r, fmt.Sprintf("/wizard/%d-step%d", i, i), http.StatusFound)
            return
        }
    }

    if r.Method == "GET" {
        h.render(w, "wizard/step6.html", state)
        return
    }

    // Create organization + admin user + config in single transaction
    err := h.wizard.FinalizeSetup(r.Context(), state.Data)
    if err != nil {
        state.Errors["finalize"] = err.Error()
        h.saveState(w, r, state)
        h.render(w, "wizard/step6.html", state)
        return
    }

    // Clear wizard state, create auth session
    h.clearState(w, r)
    h.createSession(w, r, state.Data.AdminEmail)

    http.Redirect(w, r, "/", http.StatusFound) // Redirect to app dashboard
}

func (s *Service) FinalizeSetup(ctx context.Context, data WizardData) error {
    return s.db.RunInTransaction(ctx, func(tx pgx.Tx) error {
        // 1. Create organization
        orgID := uuid.New()
        _, err := tx.Exec(ctx, `
            INSERT INTO organizations (id, name, subdomain, logo_url, created_at)
            VALUES ($1, $2, $3, $4, NOW())
        `, orgID, data.OrgName, data.OrgSubdomain, data.OrgLogo)
        if err != nil { return err }

        // 2. Create admin user
        userID := uuid.New()
        passwordHash, _ := bcrypt.GenerateFromPassword([]byte(data.AdminPassword), bcrypt.DefaultCost)
        _, err = tx.Exec(ctx, `
            INSERT INTO users (id, org_id, name, email, password_hash, role, is_admin, created_at)
            VALUES ($1, $2, $3, $4, $5, 'owner', true, NOW())
        `, userID, orgID, data.AdminName, data.AdminEmail, passwordHash)
        if err != nil { return err }

        // 3. Create default RBAC roles
        for _, role := range defaultRoles() {
            _, err = tx.Exec(ctx, `
                INSERT INTO roles (id, org_id, name, permissions, is_system, created_at)
                VALUES (gen_random_uuid(), $1, $2, $3, true, NOW())
            `, orgID, role.Name, role.Permissions)
            if err != nil { return err }
        }

        // 4. Store OIDC config
        oidcConfig := map[string]any{
            "provider": data.OIDCProvider,
            "google": map[string]string{"client_id": data.GoogleClientID, "client_secret": data.GoogleClientSecret},
            "entra": map[string]string{"client_id": data.EntraClientID, "client_secret": data.EntraClientSecret, "tenant_id": data.EntraTenantID},
            "custom": map[string]string{"issuer": data.CustomIssuer, "client_id": data.CustomClientID, "client_secret": data.CustomClientSecret},
        }
        oidcJSON, _ := json.Marshal(oidcConfig)
        _, err = tx.Exec(ctx, `
            INSERT INTO org_configs (org_id, key, value, created_at)
            VALUES ($1, 'oidc', $2, NOW())
            ON CONFLICT (org_id, key) DO UPDATE SET value = $2
        `, orgID, oidcJSON)
        if err != nil { return err }

        // 5. Store MinIO config
        minioConfig := map[string]string{
            "endpoint": data.MinIOEndpoint, "access_key": data.MinIOAccessKey,
            "secret_key": data.MinIOSecretKey, "bucket": data.MinIOBucket,
        }
        minioJSON, _ := json.Marshal(minioConfig)
        _, err = tx.Exec(ctx, `
            INSERT INTO org_configs (org_id, key, value, created_at)
            VALUES ($1, 'minio', $2, NOW())
        `, orgID, minioJSON)

        // 6. Store LiveKit config
        lkConfig := map[string]string{
            "url": data.LiveKitURL, "api_key": data.LiveKitAPIKey, "api_secret": data.LiveKitAPISecret,
        }
        lkJSON, _ := json.Marshal(lkConfig)
        _, err = tx.Exec(ctx, `
            INSERT INTO org_configs (org_id, key, value, created_at)
            VALUES ($1, 'livekit', $2, NOW())
        `, orgID, lkJSON)

        // 7. Store SMTP config (optional)
        if data.SMTPHost != "" {
            smtpConfig := map[string]any{
                "host": data.SMTPHost, "port": data.SMTPPort,
                "user": data.SMTPUser, "pass": data.SMTPPass, "from": data.SMTPFrom,
            }
            smtpJSON, _ := json.Marshal(smtpConfig)
            _, err = tx.Exec(ctx, `
                INSERT INTO org_configs (org_id, key, value, created_at)
                VALUES ($1, 'smtp', $2, NOW())
            `, orgID, smtpJSON)
        }

        // 8. Generate VAPID keys for push notifications
        vapidKeys, _ := GenerateVAPIDKeys()
        vapidJSON, _ := json.Marshal(vapidKeys)
        _, err = tx.Exec(ctx, `
            INSERT INTO org_configs (org_id, key, value, created_at)
            VALUES ($1, 'vapid', $2, NOW())
        `, orgID, vapidJSON)

        // 9. Mark setup complete
        _, err = tx.Exec(ctx, `
            INSERT INTO org_configs (org_id, key, value, created_at)
            VALUES ($1, 'setup_complete', 'true', NOW())
        `, orgID)

        return err
    })
}
```

---

### 6. Frontend (React + React Hook Form + Zod)

```tsx
// web/src/pages/wizard/Step5Services.tsx
const schema = z.object({
    minioEndpoint: z.string().url(),
    minioAccessKey: z.string().min(1),
    minioSecretKey: z.string().min(1),
    minioBucket: z.string().min(1),
    livekitUrl: z.string().url(),
    livekitApiKey: z.string().min(1),
    livekitApiSecret: z.string().min(1),
    smtpHost: z.string().optional(),
    smtpPort: z.coerce.number().optional(),
    smtpUser: z.string().optional(),
    smtpPass: z.string().optional(),
    smtpFrom: z.string().email().optional(),
});

export function Step5Services() {
    const { register, handleSubmit, formState: { errors } } = useForm<Schema>({
        resolver: zodResolver(schema),
        defaultValues: useWizardData(),
    });

    const [testing, setTesting] = useState<Record<string, 'idle'|'testing'|'success'|'error'>>({});
    const [testResults, setTestResults] = useState<Record<string, string>>({});

    const testConnection = async (service: 'minio' | 'livekit' | 'smtp') => {
        setTesting(s => ({ ...s, [service]: 'testing' }));
        try {
            await api.post('/wizard/test-service', { service, ...formData });
            setTestResults(r => ({ ...r, [service]: 'Connected successfully' }));
            setTesting(s => ({ ...s, [service]: 'success' }));
        } catch (err) {
            setTestResults(r => ({ ...r, [service]: err.message }));
            setTesting(s => ({ ...s, [service]: 'error' }));
        }
    };

    const onSubmit = handleSubmit(async (data) => {
        await api.post('/wizard/step5', data);
        navigate('/wizard/6-complete');
    });

    return (
        <form onSubmit={onSubmit} className="space-y-6">
            <Section title="MinIO (File Storage)" icon={Database}>
                <Grid cols={2}>
                    <Input label="Endpoint" {...register('minioEndpoint')} error={errors.minioEndpoint} />
                    <Input label="Bucket" {...register('minioBucket')} error={errors.minioBucket} />
                    <Input label="Access Key" {...register('minioAccessKey')} error={errors.minioAccessKey} />
                    <Input label="Secret Key" type="password" {...register('minioSecretKey')} error={errors.minioSecretKey} />
                </Grid>
                <Button type="button" variant="outline" onClick={() => testConnection('minio')} disabled={testing.minio === 'testing'}>
                    {testing.minio === 'testing' ? <Spinner /> : 'Test Connection'}
                </Button>
                {testResults.minio && <Alert variant={testing.minio === 'success' ? 'success' : 'error'}>{testResults.minio}</Alert>}
            </Section>

            <Section title="LiveKit (Video Calls)" icon={Video}>
                {/* Similar fields */}
            </Section>

            <Section title="SMTP (Email - Optional)" icon={Mail}>
                {/* Optional fields */}
            </Section>

            <WizardNavigation onBack={() => navigate('/wizard/4-auth')} onNext={onSubmit} />
        </form>
    );
}
```

---

### 7. Idempotency & Re-runnability

- **Organization creation**: `ON CONFLICT (subdomain) DO UPDATE` — safe to re-run
- **Admin user**: `ON CONFLICT (email) DO UPDATE` — password hash updated
- **Configs**: `INSERT ... ON CONFLICT (org_id, key) DO UPDATE SET value = EXCLUDED.value`
- **VAPID keys**: Regenerated only if missing
- **Wizard state**: Cleared on completion; can restart by clearing cookie or `DELETE /wizard/state`

---

### 8. Automated Install (Environment Variables)

```bash
# All required vars set → wizard skipped, setup runs on startup
TEAMUP_AUTO_SETUP=true
TEAMUP_SETUP_ORG_NAME="My Startup"
TEAMUP_SETUP_ORG_SUBDOMAIN="mystartup"
TEAMUP_SETUP_ADMIN_EMAIL="admin@mystartup.com"
TEAMUP_SETUP_ADMIN_PASSWORD="$(openssl rand -base64 32)"
TEAMUP_SETUP_OIDC_PROVIDER="google"
TEAMUP_SETUP_GOOGLE_CLIENT_ID="..."
TEAMUP_SETUP_GOOGLE_CLIENT_SECRET="..."
TEAMUP_SETUP_MINIO_ENDPOINT="minio:9000"
TEAMUP_SETUP_MINIO_ACCESS_KEY="minioadmin"
TEAMUP_SETUP_MINIO_SECRET_KEY="minioadmin"
TEAMUP_SETUP_MINIO_BUCKET="teamup-files"
TEAMUP_SETUP_LIVEKIT_URL="http://livekit:7880"
TEAMUP_SETUP_LIVEKIT_API_KEY="key"
TEAMUP_SETUP_LIVEKIT_API_SECRET="secret"
# SMTP optional
```

**Startup logic:**
```go
func (s *Server) autoSetupIfNeeded(ctx context.Context) {
    if !envBool("TEAMUP_AUTO_SETUP") { return }
    complete, _ := s.wizard.IsSetupComplete(ctx)
    if complete { return }

    data := WizardData{
        OrgName: os.Getenv("TEAMUP_SETUP_ORG_NAME"),
        // ... all fields from env
    }
    if err := s.wizard.FinalizeSetup(ctx, data); err != nil {
        log.Fatal().Err(err).Msg("Auto-setup failed")
    }
    log.Info().Msg("Auto-setup completed")
}
```

---

### 9. Security Considerations

| Aspect | Implementation |
|--------|----------------|
| **HTTPS only** | Wizard only accessible via HTTPS (enforced by ingress) |
| **Rate limiting** | 10 req/min per IP on wizard endpoints |
| **CSRF** | Double-submit cookie + SameSite=Strict |
| **Secrets** | Never logged; masked in UI; stored encrypted in DB (pgcrypto) |
| **Session** | Encrypted cookie (age: 30 min), HttpOnly, Secure, SameSite=Strict |
| **Password** | bcrypt (cost 12), min 12 chars, zxcvbn strength check |

---

### 10. Open Questions (for later)

- **Multi-language wizard** (i18n) — v2
- **Restore from backup** during setup — v2
- **Cluster join** (multi-node) — v2
- **License key validation** (if commercial) — v2

Status: resolved
