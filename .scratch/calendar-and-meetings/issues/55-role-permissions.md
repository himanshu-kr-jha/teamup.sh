# Role permissions that are real, chosen from a checklist

Status: done

## What

The user asked: "while adding new kind of roles, the permission should be provided in check list and dropdowns so that it's selectable."
Looking first showed the permissions were stored and never checked: access came from the role being named owner or admin (28 checks on the server, 7 in the web app), so a checklist alone would have set boxes that did nothing.
The user chose to make them real.

## Built

- `internal/auth/permissions.go`: the list of ten permissions in three areas (Organization, Work, Meetings), the built-in roles' defaults, `UserContext.Can`, and `PermissionsOf`, read fresh from the database on every request with no cache, so a role given or taken away applies on the next request on every replica.
- Every server check now asks for a permission; `GET /api/permissions` serves the list; `/auth/me` and sign-in return the person's permissions.
- Nobody grants more than they hold: making, changing, deleting, giving or taking away a role, and adding or importing a member with a role, are refused when the role can do something the person cannot; only real permissions can be saved; the organization keeps at least one owner.
- Migration 0029 gives the built-in roles the new defaults (owner everything, admin all ten, manager record calls, member none) and translates the old names in roles organizations made.
- Web: `features/admin/RolesTab.tsx`, a checklist grouped by area with descriptions, All and None per area, a "Start from" dropdown of other roles, and boxes greyed out for what the person lacks; role dropdowns in Add member and Assign role show what each role does and offer only those within reach; the Organization page shows each part by permission.

## Decisions

- Permissions are read from the database on each request, not carried in the token, so taking a role away is immediate.
- The built-in roles stay as they are; organizations make their own for anything else.
- Owner-only things stay owner-only: resetting another owner's password.

## Verified

Go: `internal/org/permissions_test.go` (a made-up role does exactly its boxes, nobody grants more than they hold, adding or importing a member cannot escalate, taking a role away, the list endpoint), migration 0029 up and down over built-in and made-up roles, every package's tests.
Browser: `web/e2e/meetings.y-roles.spec.ts`, making a role from the checklist, giving it to Alice, what she can and cannot see and do, and a role keeper unable to tick or hand out what he lacks; looked at on a phone and a computer.
