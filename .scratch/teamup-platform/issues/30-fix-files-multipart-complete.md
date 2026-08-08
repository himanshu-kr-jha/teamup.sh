# 30 — Fix files package: Complete multipart upload stub implementation

**What to build:** Complete the files package multipart upload stub implementation and remove unused imports.

**Blocked by:** 21 — Fix files package: Unused imports and MinIO API

**Status:** completed

- [x] Complete CompleteMultipartUpload implementation using minio-go v7 API
- [x] Remove unused "net/http" import from files.go
- [x] Fix routes.go unused imports ("context", "github.com/google/uuid")
- [x] Ensure CompleteMultipartUpload uses minio.CompleteMultipartUpload correctly

**Type:** task
