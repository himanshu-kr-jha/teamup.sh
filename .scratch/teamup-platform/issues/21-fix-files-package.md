# 21 — Fix files package: Unused imports and MinIO API

**What to build:** Fix the files package by removing unused uuid import and fixing MinIO multipart upload API.

**Blocked by:** None — can start immediately

**Status:** completed

- [x] Remove unused "github.com/google/uuid" import in files.go
- [x] Fix MinIO multipart upload: use minio.NewMultipartUpload (not NewMultipartUpload on client)
- [x] Fix PresignMultipartUpload method call (check v7 API)
- [x] Remove unused 'err' variable in files.go line 127

**Type:** task
