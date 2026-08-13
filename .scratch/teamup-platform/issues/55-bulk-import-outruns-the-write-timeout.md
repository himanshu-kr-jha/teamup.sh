# 55 — A full-size bulk import outruns the server's write timeout and loses every password

**What to build:** Stop a 200-row import from being a single 29-second request whose response body is the only copy of 200 unrecoverable passwords.

**Blocked by:** None — can start immediately

**Status:** completed — maxImportRows lowered from 200 to 50 against the 30s WriteTimeout, with the 29.0s measurement recorded in the code (commit 60f9c8d).

- [ ] Add a test that fails when a max-size import exceeds a safe fraction of `WriteTimeout`
- [ ] Pick a direction: lower `maxImportRows` to what fits the budget, raise/scope the timeout for this route, run the import as a job the admin polls, or make the generated passwords retrievable until first use
- [ ] Whatever is chosen, an import that is cut off must not leave accounts nobody can sign in to
- [ ] Bound the work by time as well as by row count, so slower hardware fails predictably

**Type:** bug

## Evidence

`internal/org/org.go:934` caps an import at 200 rows. Every row costs one bcrypt at `bcrypt.DefaultCost` inside `AddMember` (`internal/org/org.go:877`). `cmd/teamup/main.go:678` sets:

```go
WriteTimeout: 30 * time.Second,
```

Measured, on real hardware, against a real database — `internal/hierarchyverify/escalation_test.go:TestBulkImportExceedsTheServerWriteTimeout`:

```
    200 rows created in 29.023s (145ms per row); 200 passwords returned in the body
    headroom against the 30s WriteTimeout: 1s (97% of the budget used)
```

**97% of the write budget on an idle machine with nothing else running.** Anything that makes a row slower than 150ms — a busier box, a smaller container, a shared CPU, contention from the other 55 people using the product — puts the import past the timeout.

Confirmed independently: `TestBulkImport/cap_of_200_rows` measured 28.58s for the same work.

### What happens when it does go over

`ImportMembers` is not transactional across rows, by design (`internal/org/org.go:959-966`): each `AddMember` commits its own transaction. So when `WriteTimeout` fires:

- every account created so far is **committed and permanent**
- the response is **never delivered**
- the passwords are `bcrypt`-hashed in `users.password_hash` and exist in plaintext nowhere else — `internal/org/org.go:840-844` says so: *"returns the generated password once — it is never stored in the clear and cannot be shown again"*
- the accounts have `must_change_password = TRUE` (`internal/org/org.go:886`), so they are unusable without the password that was just destroyed
- there is no re-send, no reset-by-email path (SMTP is optional and usually unconfigured, per the same comment), and re-running the import returns `duplicate` for every one of them (`TestBulkImport` shows this outcome)

The admin's recovery is to deactivate and re-create every account by hand.

### Reachability

`POST /api/users/import` — `internal/org/routes.go:57`, admin-gated (verified: `TestImportRequiresAdmin` gets 403 for both `member` and `manager`, 200 for `owner`). Onboarding a 55-person startup in one file is the exact use case the feature was built for, and the cap was chosen at 200 to accommodate it.

The comment at `internal/org/org.go:928-934` estimates "10-20 seconds" for 200 rows and calls that "about as long as a proxy will wait". The measurement is 29s, and the proxy that matters is TeamUp's own `http.Server`.
