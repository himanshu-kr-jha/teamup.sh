# 14 — Call recording in the deployment

**What to build:** Recording a call works on a real deployment, not just in principle.

The application side is implemented: starting a recording asks LiveKit's Egress service to composite the room and write the file into the organization's object storage prefix, ending a call finalises it, and the resulting file is covered by the same access checks as any other attachment. But no Egress service runs in the compose or Helm stack, so every attempt reports that recording is unavailable.

Retention stays deliberately open: the platform spec parks it as undecided, so recordings are kept until someone removes them. Agree a retention rule before enabling this in production.

**Blocked by:** 09 — Join and run a video call.

**Status:** ready-for-agent

- [ ] LiveKit Egress runs as part of the local compose stack and the Helm chart
- [ ] Egress is configured to reach the same object storage the application uses
- [ ] Starting a recording on a live call produces a playable file when the call ends
- [ ] The recording appears on the call's detail page and can be played or downloaded
- [ ] Access to a recording obeys the same organization and membership rules as any other stored file
- [ ] A deployment without Egress still reports recording as unavailable rather than failing obscurely
- [ ] The retention question is written down as an open decision, not left implicit
