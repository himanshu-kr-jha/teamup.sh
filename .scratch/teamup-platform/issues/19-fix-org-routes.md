# 19 — Fix org routes: Unused variables and pgx.Transaction

**What to build:** Fix the org routes by removing unused variables and fixing the transaction handling.

**Blocked by:** 15 — Fix org package (Transaction handling must be fixed first)

**Status:** resolved

- [x] Remove unused 'orgID' variables in routes.go (lines 57, 74, 103, 110)
- [x] Remove unused 'id' variables in routes.go (lines 90, 96, 126)
- [x] Ensure all route handlers use the fixed transaction API from org.Service

**Type:** task
