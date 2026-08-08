# 04 — Fix the Settings profile save

**What to build:** Editing your name or avatar on the Settings page persists.

It currently cannot work, for two stacked reasons. The request targets a path that already carries the API prefix, while the HTTP client adds that prefix again — so it asks for a doubled path that does not exist. And the path it means to reach is not a route either: user updates are addressed by id, and there is no "me" alias. Both were confirmed against the running server. The failure is invisible because the error only reaches the console.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] Changing your display name persists and survives a reload
- [ ] The header and sidebar reflect the new name without a manual refresh
- [ ] A failed save tells the user, rather than appearing to succeed
- [ ] Either a "current user" route exists or the page addresses the signed-in user by id — pick one and keep it consistent
- [ ] A user still cannot edit somebody else's profile unless they administer the organization
