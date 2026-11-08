# Forward a message, files and all

Status: ready-for-human
Severity: Low
Asked for by: the user, "any one can forward the message to other chats. forwarded message will have forwarded written, even files and images".

## Comments

2026-11-08, orchestrator: decision 53.
`internal/chat/forward.go`, `POST /api/messages/:id/forward`, migration 0025 (`messages.forwarded`), `files.Service.CopyObject`, and `web/src/features/chat/ForwardDialog.tsx`.
Verified: `internal/chat/forward_test.go` (marked, files copied under the target, original untouched, a reader-only check, all-or-nothing on targets, limits, deleted), and `web/e2e/meetings.y-more.spec.ts`, where somebody who was never in the original conversation receives it in two and downloads the file.
