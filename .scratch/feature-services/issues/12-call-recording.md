# 12 — Call recording

**What to build:** A call can be recorded, and the recording is retrievable afterwards from the call's detail page.

Recording currently fails loudly with "recording not implemented" rather than pretending to work. LiveKit performs the recording through its Egress service and writes the output to S3-compatible storage directly, so the work is wiring Egress to the MinIO bucket, tracking the recording's lifecycle against the call, and surfacing the result — not streaming media through this application.

Retention policy is deliberately left open: the platform spec parks it as undecided, so this ticket stores recordings and surfaces them without expiring them. Agree a retention rule before enabling this in production.

**Blocked by:** 09 — Call history and detail.

**Status:** ready-for-agent

- [ ] Starting a recording on an active call succeeds and the call shows as being recorded to everyone in it
- [ ] Stopping the recording, or ending the call, finalises the recording
- [ ] The finished recording is stored in object storage under the organization's prefix
- [ ] The call's detail page lists its recordings with duration and size, and each can be played or downloaded
- [ ] Access to a recording is subject to the same organization and membership checks as any other stored file
- [ ] A recording that fails to start or finalise leaves the call in a sane state and surfaces a real error, not a silent success
- [ ] Only a user permitted to manage calls can start or stop a recording
