# 13 — Remove the controls for features that are not in v1

**What to build:** The Settings page stops offering things the product does not do.

It currently presents Update Password, Enable 2FA, Manage Sessions, Download Data and Delete Account as ordinary buttons. All of them do nothing. Two of those — data export and account deletion — are listed in the platform spec as explicitly **not v1**, and two-factor authentication does not appear in the spec at all. A button that silently does nothing is worse than an absent one: it reads as a bug, and for account deletion it implies a guarantee the product cannot honour.

Delete them rather than build them. Where the capability does exist behind the scenes, wire it instead of removing it — session management is the likely candidate, since revocation is implemented.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] Enable 2FA, Download Data and Delete Account are gone from the interface
- [ ] Update Password is either wired to a working change-password flow or removed — not left inert
- [ ] Manage Sessions is either wired to the existing session listing and revocation or removed
- [ ] No remaining control in Settings is a no-op
- [ ] Anything removed is recorded as deferred, so it is a decision rather than a disappearance
