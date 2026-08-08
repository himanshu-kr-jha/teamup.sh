# 02 — Close the file access-control hole

**What to build:** File upload and download URLs that only work for files the caller is actually entitled to.

The two access checks behind the file endpoints currently accept everything unconditionally. Any authenticated user can therefore ask for a presigned upload URL under any organization's prefix, or a presigned download URL for any object key they can name — including another organization's chat attachments, task attachments and call recordings. Presigned URLs bypass the application once issued, so this is a live data-exposure bug rather than a missing feature.

This is listed as security work, not a feature slice: it makes an existing, reachable endpoint safe.

**Blocked by:** 01 — Shared database test harness.

**Status:** ready-for-agent

- [ ] An upload URL is only issued when the caller's organization matches the organization the object will be stored under
- [ ] An upload URL is only issued when the caller may write to the referenced parent (the channel, task or call the attachment belongs to)
- [ ] A download URL is only issued when the object key belongs to the caller's organization and the caller may read its parent
- [ ] A request naming another organization's object key is refused, and the refusal does not reveal whether that object exists
- [ ] Attempts are logged with enough context to investigate, without logging the presigned URL itself
- [ ] Tests cover: same-org allowed, cross-org refused, unknown object refused, and a caller without access to the parent refused
