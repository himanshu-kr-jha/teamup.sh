# Voice and video messages recorded on the spot

Status: ready-for-human
Severity: Low
Asked for by: the user, "members can also send voice messages and small video messages that can be recorded on that spot".

## Comments

2026-11-08, orchestrator: decision 61.
`web/src/features/chat/Recorder.tsx`, playing in `AttachmentChip.tsx`.
Verified: `Recorder.test.ts`, and end to end with Chromium's fake microphone and camera, through to playable media in the conversation. Not verified on Safari or a real phone.
