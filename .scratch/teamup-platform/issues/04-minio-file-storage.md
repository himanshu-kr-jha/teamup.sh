## Question

Specify MinIO integration: bucket structure (org-scoped? user-scoped?), presigned URL generation for uploads/downloads, multipart upload for large files, retention policies, virus scanning hook (optional), and client-side upload flow (direct to MinIO via presigned URL vs. proxied through backend).

Status: claimed

## Answer

**Decision: MinIO integration architecture defined below.**

### 1. Bucket Structure

**Single bucket per organization** (org-scoped) — simplest for permissions, quotas, and cleanup.

```
teamup-files/
├── org-{orgId}/
│   ├── chat/
│   │   ├── {channelId}/
│   │   │   ├── {messageId}/
│   │   │   │   ├── {attachmentId}-{originalFilename}
│   │   │   │   └── thumbnails/
│   ├── tasks/
│   │   ├── {taskId}/
│   │   │   ├── {attachmentId}-{originalFilename}
│   │   │   └── thumbnails/
│   ├── recordings/
│   │   ├── {roomName}/
│   │   │   ├── {recordingId}.mp4
│   │   │   └── thumbnails/
│   ├── avatars/
│   │   └── {userId}-{timestamp}.{ext}
│   └── exports/
│       └── {exportId}.{zip|csv|pdf}
```

**Why not user-scoped?** Cross-user sharing (chat attachments, task files) requires org-level access anyway. Org bucket = single policy, single quota, single lifecycle.

**Bucket naming:** `teamup-files-{orgId}` (DNS-safe, unique per org). Created on org creation via setup wizard.

---

### 2. Presigned URL Generation (Go Backend)

#### Upload Presigned URL (POST policy)
```go
// internal/files/service.go
package files

import (
    "context"
    "time"
    "github.com/minio/minio-go/v7"
    "github.com/minio/minio-go/v7/pkg/credentials"
)

type Service struct {
    client     *minio.Client
    bucketName string
    cfg        Config
}

type UploadRequest struct {
    OrgID       string
    Category    string // "chat" | "tasks" | "recordings" | "avatars" | "exports"
    RefID       string // channelId, taskId, roomName, userId, exportId
    Filename    string
    ContentType string
    Size        int64
    UserID      string
}

type UploadResponse struct {
    UploadURL   string            `json:"uploadUrl"`   // Presigned PUT URL
    ObjectKey   string            `json:"objectKey"`   // Full object key
    ExpiresAt   time.Time         `json:"expiresAt"`
    Headers     map[string]string `json:"headers"`     // Required headers (Content-Type, etc.)
    Multipart   *MultipartInfo    `json:"multipart,omitempty"`
}

func (s *Service) GenerateUploadURL(ctx context.Context, req UploadRequest) (*UploadResponse, error) {
    // Validate: user has access to org, category, ref
    if err := s.validateAccess(ctx, req); err != nil {
        return nil, err
    }

    objectKey := s.buildObjectKey(req)
    contentType := req.ContentType
    if contentType == "" {
        contentType = mime.TypeByExtension(filepath.Ext(req.Filename))
    }

    // Single-part upload (< 50MB): presigned PUT
    if req.Size < 50*1024*1024 {
        url, err := s.client.PresignedPutObject(ctx, s.bucketName, objectKey, 15*time.Minute)
        return &UploadResponse{
            UploadURL: url.String(),
            ObjectKey: objectKey,
            ExpiresAt: time.Now().Add(15 * time.Minute),
            Headers:   map[string]string{"Content-Type": contentType},
        }, err
    }

    // Multipart upload (>= 50MB)
    return s.initiateMultipartUpload(ctx, req, objectKey, contentType)
}

func (s *Service) buildObjectKey(req UploadRequest) string {
    // Sanitize filename
    safeName := sanitizeFilename(req.Filename)
    timestamp := time.Now().UTC().Format("20060102T150405Z")
    return fmt.Sprintf("org-%s/%s/%s/%s-%s", req.OrgID, req.Category, req.RefID, timestamp, safeName)
}
```

#### Download Presigned URL (GET)
```go
func (s *Service) GenerateDownloadURL(ctx context.Context, orgID, objectKey string, filename string, inline bool) (string, error) {
    // Validate access
    if err := s.validateObjectAccess(ctx, orgID, objectKey); err != nil {
        return "", err
    }

    reqParams := make(url.Values)
    if inline {
        reqParams.Set("response-content-disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
    } else {
        reqParams.Set("response-content-disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
    }

    url, err := s.client.PresignedGetObject(ctx, s.bucketName, objectKey, 1*time.Hour, reqParams)
    return url.String(), err
}
```

#### Thumbnail Generation (async, via webhook)
```go
// internal/files/thumbnail.go
func (s *Service) GenerateThumbnail(ctx context.Context, objectKey string) (string, error) {
    // Download object
    obj, err := s.client.GetObject(ctx, s.bucketName, objectKey, minio.GetObjectOptions{})
    if err != nil { return "", err }
    defer obj.Close()

    // Decode image (supports JPEG, PNG, WebP, HEIC via libheif)
    img, format, err := image.Decode(obj)
    if err != nil { return "", err }

    // Resize to 256x256 max (maintain aspect)
    thumb := resize.Thumbnail(256, 256, img, resize.Lanczos3)

    // Encode as WebP (best compression)
    thumbKey := strings.TrimSuffix(objectKey, filepath.Ext(objectKey)) + "_thumb.webp"
    buf := new(bytes.Buffer)
    if err := webp.Encode(buf, thumb, webp.Lossless); err != nil { return "", err }

    // Upload thumbnail
    _, err = s.client.PutObject(ctx, s.bucketName, thumbKey, buf, int64(buf.Len()), minio.PutObjectOptions{
        ContentType: "image/webp",
    })
    return thumbKey, err
}
```

---

### 3. Multipart Upload (Large Files)

**Threshold:** ≥ 50MB (configurable)

```go
func (s *Service) initiateMultipartUpload(ctx context.Context, req UploadRequest, objectKey, contentType string) (*UploadResponse, error) {
    // Create multipart upload
    uploadID, err := s.client.NewMultipartUpload(ctx, s.bucketName, objectKey, minio.PutObjectOptions{
        ContentType: contentType,
    })
    if err != nil { return nil, err }

    // Generate presigned URLs for each part (client decides part count)
    partCount := int((req.Size + 50*1024*1024 - 1) / (50 * 1024 * 1024)) // 50MB parts
    if partCount > 10000 { partCount = 10000 } // S3 limit

    partURLs := make([]string, partCount)
    for i := 1; i <= partCount; i++ {
        url, err := s.client.PresignMultipartUpload(ctx, s.bucketName, objectKey, uploadID, i, 2*time.Hour)
        partURLs[i-1] = url.String()
    }

    return &UploadResponse{
        UploadURL: "", // Not used for multipart
        ObjectKey: objectKey,
        ExpiresAt: time.Now().Add(2 * time.Hour),
        Headers:   map[string]string{"Content-Type": contentType},
        Multipart: &MultipartInfo{
            UploadID:  uploadID,
            PartURLs:  partURLs,
            PartSize:  50 * 1024 * 1024,
        },
    }, nil
}

func (s *Service) CompleteMultipartUpload(ctx context.Context, orgID, objectKey, uploadID string, parts []CompletedPart) error {
    // Validate access
    if err := s.validateObjectAccess(ctx, orgID, objectKey); err != nil {
        return err
    }

    // Convert to minio.CompletePart
    minioParts := make([]minio.CompletePart, len(parts))
    for i, p := range parts {
        minioParts[i] = minio.CompletePart{PartNumber: p.PartNumber, ETag: p.ETag}
    }

    _, err := s.client.CompleteMultipartUpload(ctx, s.bucketName, objectKey, uploadID, minioParts)
    return err
}
```

**Client-side flow (React):**
```typescript
// hooks/useFileUpload.ts
async function uploadLargeFile(file: File, uploadResp: UploadResponse) {
    const { partURLs, partSize, uploadID } = uploadResp.multipart!;
    const parts: CompletedPart[] = [];

    for (let i = 0; i < partURLs.length; i++) {
        const start = i * partSize;
        const end = Math.min(start + partSize, file.size);
        const chunk = file.slice(start, end);

        const response = await fetch(partURLs[i], {
            method: 'PUT',
            body: chunk,
            headers: { 'Content-Type': uploadResp.headers['Content-Type'] },
        });
        if (!response.ok) throw new Error(`Part ${i+1} failed`);

        parts.push({ PartNumber: i + 1, ETag: response.headers.get('ETag')! });
    }

    // Complete
    await api.post('/files/multipart/complete', { objectKey: uploadResp.objectKey, uploadID, parts });
}
```

---

### 4. Retention Policies

| Category | Retention | Action |
|----------|-----------|--------|
| `chat` attachments | 1 year (configurable) | Soft delete → archive to cold storage |
| `tasks` attachments | 3 years (compliance) | Retain until task deleted + 90 days |
| `recordings` | 30 days (configurable) | Auto-delete unless pinned |
| `avatars` | Until replaced | Old version deleted on update |
| `exports` | 7 days | Auto-delete after download |

**Implementation:** MinIO ILM (Information Lifecycle Management) rules + Go backend cleanup job.

```yaml
# MinIO ILM config (mc ilm rule add)
# Chat: transition to cold after 90 days, expire after 365
# Recordings: expire after 30 days
# Exports: expire after 7 days
```

**Go cleanup job (cron, daily):**
```go
func (s *Service) CleanupExpiredObjects(ctx context.Context) error {
    // List objects with prefix, check metadata/tags for expiry
    // Delete expired
    // Update DB (mark attachment as archived/deleted)
}
```

---

### 5. Virus Scanning Hook (Optional, v2)

**Not in v1.** Architecture for future:

```go
// internal/files/scanner.go
type VirusScanner interface {
    Scan(ctx context.Context, reader io.Reader) (bool, error) // clean, error
}

// ClamAV implementation
type ClamAVScanner struct {
    daemon string // "clamd:3310"
}

func (s *ClamAVScanner) Scan(ctx context.Context, r io.Reader) (bool, error) {
    conn, err := net.Dial("tcp", s.daemon)
    // ... send file, parse response
}

// Integration: after upload completes (webhook or post-complete hook)
// If infected: quarantine object, alert admin, delete reference
```

**v1:** Skip scanning. Document as known limitation. Add `virus_scanned: false` metadata.

---

### 6. Client-Side Upload Flow: Direct to MinIO (Presigned URL)

**Decision: Direct upload to MinIO via presigned URL.** Backend only generates URLs, validates access, records metadata.

**Why not proxied?**
- Backend becomes bottleneck for large files
- Double bandwidth (client → backend → MinIO)
- No streaming upload progress
- Complex timeout handling

**Flow:**
```
1. Client → Backend: POST /api/files/upload-url { category, refId, filename, size, contentType }
2. Backend: Validates access, generates presigned URL, returns { uploadUrl, objectKey, expiresAt, headers, multipart? }
3. Client → MinIO: PUT {uploadUrl} (or multipart to partURLs)
4. Client → Backend: POST /api/files/complete { objectKey, uploadId?, parts? }
5. Backend: Records file metadata in DB (attachment row), triggers thumbnail job if image
6. Client: Uses objectKey in chat.send / task.attachment
```

**Security:**
- Presigned URLs expire (15 min single, 2 hr multipart)
- URLs bound to specific object key (cannot overwrite other files)
- Backend validates org membership before generating URL
- CORS on MinIO: allow only app origin

**MinIO CORS Config:**
```bash
mc cors set teamup-files '{
  "CORSRule": [{
    "AllowedOrigins": ["https://app.teamup.local"],
    "AllowedMethods": ["GET", "PUT", "POST", "HEAD"],
    "AllowedHeaders": ["*"],
    "ExposeHeaders": ["ETag"],
    "MaxAgeSeconds": 3600
  }]
}'
```

---

### 7. Database Schema (Attachments)

```sql
-- migrations/003_attachments.sql
CREATE TABLE attachments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL REFERENCES organizations(id),
    uploaded_by     UUID NOT NULL REFERENCES users(id),
    category        VARCHAR(20) NOT NULL, -- 'chat', 'tasks', 'recordings', 'avatars', 'exports'
    ref_id          VARCHAR(255) NOT NULL, -- channel_id, task_id, room_name, user_id, export_id
    object_key      VARCHAR(1024) NOT NULL UNIQUE,
    filename        VARCHAR(512) NOT NULL,
    content_type    VARCHAR(128) NOT NULL,
    size_bytes      BIGINT NOT NULL,
    thumbnail_key   VARCHAR(1024),
    checksum_sha256 CHAR(64),
    virus_scanned   BOOLEAN DEFAULT FALSE,
    virus_clean     BOOLEAN,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX idx_attachments_org_category_ref ON attachments(org_id, category, ref_id);
CREATE INDEX idx_attachments_object_key ON attachments(object_key);
```

---

### 8. Docker Compose / Helm Config

```yaml
# docker-compose.yml (excerpt)
minio:
  image: minio/minio:latest
  command: server /data --console-address ":9001"
  ports:
    - "9000:9000"   # API
    - "9001:9001"   # Console
  environment:
    MINIO_ROOT_USER: minioadmin
    MINIO_ROOT_PASSWORD: minioadmin
  volumes:
    - minio-data:/data
  healthcheck:
    test: ["CMD", "mc", "ready", "local"]
    interval: 30s
    timeout: 10s
  networks:
    - teamup-net

# Helm values
minio:
  enabled: true
  mode: standalone  # or distributed for HA
  replicas: 1
  resources:
    limits:
      memory: 2Gi
  persistence:
    size: 50Gi
```

---

### 9. Open Questions (for later)

- CDN integration (CloudFront, Cloudflare) for public assets (avatars, exports)
- Cross-org file sharing (guest access) — v2
- Immutable writes (WORM) for compliance — v2
- Server-side encryption (SSE-S3, SSE-KMS) — MinIO supports, enable in prod

Status: resolved
