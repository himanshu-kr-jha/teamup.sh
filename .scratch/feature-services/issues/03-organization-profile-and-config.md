# 03 — Organization profile and configuration

**What to build:** The Settings screen shows the organization and the signed-in user as they actually exist in the database, and edits to them stick.

Right now every read of an organization, a user profile or a stored organization config returns nothing, so Settings can only render placeholders. This also unblocks a quieter problem: the setup wizard writes the OIDC, MinIO, LiveKit, SMTP and VAPID configuration into the organization's config store, but nothing can read it back, so those values are effectively write-only and the running app still depends entirely on environment variables.

**Blocked by:** 01 — Shared database test harness.

**Status:** ready-for-agent

- [ ] Settings shows the organization's real name, subdomain and logo
- [ ] Settings shows the signed-in user's real name, email and avatar
- [ ] Editing the organization name or logo persists and survives a reload
- [ ] Editing the current user's profile persists and survives a reload
- [ ] A stored organization config value can be read back by key, and writing a key twice updates rather than duplicating it
- [ ] Config values marked secret are never returned to the browser
- [ ] Looking up an organization by subdomain works, and a lookup for an unknown subdomain reports not-found rather than an empty organization
- [ ] Reads are scoped to the caller's organization
