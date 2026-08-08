# 15 — Add team members

**What to build:** An administrator can add somebody to the organization, and that person can sign in.

Right now nobody can. There is no endpoint that creates a user, no invitation table, and the "Sign up" link on the login page points at a route that does not exist. The OIDC callback would not help either — it returns a mock user in a randomly generated organization. The only account that exists is the one the setup wizard created, so the product is a single-user demo until this lands. That makes it the most valuable thing outstanding.

Deliberately no email: SMTP is optional in v1 and usually unconfigured, so an emailed invitation link would fail silently on most installs. The administrator creates the account with a temporary password and passes it on however they already communicate. Invitation links can come later, once email is a dependency worth having.

The new member must be made to change that temporary password, and the enforcement has to hold against the API and not only the UI.

**Blocked by:** None — can start immediately.

**Status:** done

- [x] An administrator can add a member from the Organization screen, giving a name, email and role
- [x] A temporary password is generated and shown once, with a warning that it will not be shown again
- [x] The new member appears immediately in the members list with the role they were given
- [x] The new member can sign in with that temporary password
- [x] On first sign-in they must set their own password before they can use anything else, and that holds if they call the API directly rather than using the screen
- [x] Once changed, the requirement does not come back
- [x] A non-administrator cannot add members, through the UI or the API
- [x] A duplicate email is reported clearly rather than failing as a server error
- [x] The dead "Sign up" link no longer offers something that does not exist
