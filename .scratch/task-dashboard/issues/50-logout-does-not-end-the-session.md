# 50 - Log out does not end the session: the next page load signs you straight back in

Status: resolved
Type: bug
Severity: high

Found by the real-browser E2E run (`web/e2e/dashboard.spec.ts`, test "30: logging out lands on /login..."), against a real server and Postgres.
Ticket 30 fixed the screen. The session itself is still alive on the server.

## What is wrong

Two server defects in `POST /api/auth/logout`:

1. `internal/auth/routes.go:41` registers `/logout` on the public group, outside `AuthMiddleware`.
   `Logout` then reads `GetUserFromContext(c)`, which is empty there, so `userCtx.SessionID` is `""` and `RevokeSession` is never called (`routes.go:251`).
   The refresh token stays valid in the database.
2. `ClearAuthCookies` (`internal/auth/auth.go:473-485`) expires both cookies with `Path: "/"`.
   The refresh cookie was set with `Path: "/api/auth/refresh"` (`auth.go:464`), so the browser treats the expiry as a different cookie and keeps `rt`.

So after logout the `at` cookie is gone, but the `rt` cookie is still held and still accepted.

## Exact reproduction

curl, no browser needed:

```
curl -c jar -H 'content-type: application/json' -d '{"email":"alice@e2e.test","password":"E2e-pass-12345!"}' http://localhost:18321/api/auth/login
curl -b jar -c jar -X POST http://localhost:18321/api/auth/logout        # 200, Set-Cookie at=; rt=; Path=/
curl -b jar -c jar -o /dev/null -w '%{http_code}' http://localhost:18321/api/auth/me       # 401
curl -b jar -c jar -X POST -o /dev/null -w '%{http_code}' http://localhost:18321/api/auth/refresh   # 200  <- should be 401
```

The jar still holds `rt` (path `/api/auth/refresh`) after the logout call.

In the browser (the failing Playwright test):

1. Sign in as alice@e2e.test through /login, open /tasks.
2. User menu, Log out. You land on /login. Reload: still /login.
3. Type /tasks in the address bar. The page does not redirect to /login. It loads Alice's whole dashboard, signed in as Alice.
   The app asks /auth/me (401), calls /auth/refresh with the surviving `rt` cookie (200), and carries on as her.

On a shared machine, logging out therefore does nothing: the next person who opens any TeamUp URL is Alice.

Screenshot: /tmp/claude-1000/-home-himanshu-kumar-jha-Documents-Ongoing-Projects-teamUp/47fd8d20-9a9e-48c5-8ba0-78ed94fb07ed/scratchpad/e2e-shots/defects/after-logout-still-signed-in.png

## Simple fix

- Put `/logout` behind the auth middleware, or have it read the refresh cookie and revoke that session when the access token is already expired.
  Logging out with an expired access token must still end the session.
- Clear `rt` with the path it was set with (`/api/auth/refresh`), as well as `at` with `/`.
- Add a Go test that logs in, logs out, then calls `/auth/refresh` with the cookies the server sent back and expects 401.

The E2E test is the acceptance check: `web/e2e/run.sh -g "30:"`.

## Answer

`POST /api/auth/logout` stays public, and now revokes the session behind the tokens on the request (`EndPresentedSession`), so it works with an expired access token.
Refresh after logout is 401, including when the old token is replayed by hand, and the access token dies with the session.

One thing the ticket's fix missed: with `rt` on `Path=/api/auth/refresh` the browser never sends it to `/api/auth/logout`, so the server could not have read it there.
`rt` now lives on `Path=/api/auth`, and is cleared on that path.
`internal/auth/logout_test.go` goes through a real cookie jar, because hand-written Cookie headers cannot see this.

Browsers already signed in hold `rt` on the old path, where it would shadow the new cookie, since the more specific path is sent first.
So every login and refresh also expires the old-path cookie, and logout clears both.
This can be removed once a refresh token's lifetime has passed since deploying.
Known gap for that window only: somebody with an old-path cookie and an already expired access token has the cookie cleared, but their token row stays valid until it expires, because the browser does not send it.

Logging in again after logout, and over a revoked session, is covered by tests.
The RP-initiated logout redirect in the handler is gone: it was unreachable, since the public route never had a user in context, so nothing changes for OIDC users, who still have no sign-out at the provider.
A redirect from an XHR could not have worked anyway. If that is wanted, it needs its own ticket.

`AuthProvider.logout` now pauses React Query first (`onlineManager.setOnline(false)`). Clearing the cache made every mounted query refetch on a dead session, which put four 401s in the console on the way out.
