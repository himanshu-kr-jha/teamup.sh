# 04 — RBAC roles and team membership

**What to build:** An admin can see who is in the organization, change what someone is allowed to do, and put people into teams — and the change takes effect.

The setup wizard already creates the four default roles and makes the first admin an owner, but there is no way to assign a role to anyone else or to build the team hierarchy, so the RBAC model exists in the schema and nowhere else.

**Blocked by:** 03 — Organization profile and configuration.

**Status:** ready-for-agent

- [ ] An admin can list the organization's members with their current roles
- [ ] An admin can assign a role to a member, and the member's effective permissions change accordingly
- [ ] Assigning a role the member already holds is harmless rather than an error
- [ ] An admin can add a member to a team and see the team's membership
- [ ] A member cannot assign roles or change team membership
- [ ] A user can be looked up by email within the organization, and the lookup does not cross organization boundaries
- [ ] An action gated on a permission is refused before the change and allowed after it
