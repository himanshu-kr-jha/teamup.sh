# 06 — Members, roles and teams administration

**What to build:** An administrator can see who is in the organization, change what someone is allowed to do, and organise people into teams — from the UI.

The whole API is implemented and tested, including the guards that keep role changes to administrators and confine every change to one organization. There is no screen for any of it, so the RBAC model is currently unreachable except by hand.

**Blocked by:** 01 — Dialog and form primitives.

**Status:** ready-for-agent

- [ ] A members list shows each person's name, email and current roles
- [ ] An administrator can assign and remove a role, and the change is reflected in the list
- [ ] A non-administrator does not see these controls, and is refused if they call the API directly
- [ ] Teams can be created, renamed and deleted, and people added to or removed from them
- [ ] A team shows its members and its manager
- [ ] Deactivating a member removes them from the list
- [ ] Attempting something not permitted produces a clear message rather than a silent no-op
- [ ] Note for whoever picks this up: role changes are carried in the access token, so they take effect on the next token refresh rather than instantly. Either surface that or shorten the wait — do not leave it looking broken.
