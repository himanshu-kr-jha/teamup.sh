# No way to recover a forgotten password

Status: resolved
Type: bug

The "Forgot password?" link on the login page goes to `/forgot-password`, which has no route, so it lands back on the login page.
No server endpoint resets a password, and an admin cannot set somebody else's.
Deleting a member and adding them back fails with 409, because the email is still held.

A company of 50 will hit this in its first month.
`docs/DEPLOYMENT.md` documents a tested workaround: an `UPDATE users` with a bcrypt hash, run on the VM.

The Google and Microsoft buttons on the same page are also dead on an install with no provider configured, answering 400.
Hiding them when no provider is configured would stop people trying them.

## Answer

An owner or admin resets a member's password from the Organization page: `POST /api/users/:id/reset-password`.
`ResetMemberPassword` in `internal/org/org.go` generates a password with the same generator as add-member, stores the hash, sets `must_change_password` and revokes the member's refresh tokens in one transaction, which ends their sessions at once.
The password is returned once and shown in the existing temporary password dialog, and the existing `ChangePasswordPage` does the rest.

Who may do it: owner or admin only (403), an admin cannot reset an owner (403), another organization's or a deleted member is 404.
Nobody can reset themselves (400), which the ticket did not ask for: `ChangePassword` proves the current password, and this would let a borrowed administrator session skip that.
Create-member writes nothing to the audit log, and nothing else in the product does either, so neither does this.

The login page now says "Forgot your password? Ask an administrator of your organization to reset it."
There is no email flow.

Adding a deleted member back no longer gives 409, and it did not need a redesign.
`UNIQUE (org_id, email)` became a partial unique index `WHERE deleted_at IS NULL`, in migration `0016_users_email_unique_while_live`, and the one `ON CONFLICT (org_id, email)` in `internal/wizard/wizard.go` names that predicate.
The person comes back as a new account, and their old row stays because other rows point at it.
Their old tasks and messages stay on the old row.
Rolling back renames the email on any deleted row that clashes, because the old constraint cannot hold both.
`TestEmailMigrationRollsBackPastARejoinedMember` covers that.

Tests: `internal/org/reset_password_test.go`, and "54. forgotten password" in `web/e2e/dashboard.spec.ts`.

Left alone:
- The Google and Microsoft buttons on the login page, mentioned above as a separate thought.
They still answer 400 with no provider configured.
- A lone owner who forgets their password still has no way back except SQL, because nobody outranks them.
`docs/DEPLOYMENT.md` says to keep a second owner.
- `ChangePasswordPage` checks for 8 characters and the server requires 12, so 8 to 11 characters gets the server's message after the request rather than before it.
- Deleting a member does not revoke their sessions, so a live session keeps working until its tokens run out.
Found while reading `DeactivateUser`, not tested here.
