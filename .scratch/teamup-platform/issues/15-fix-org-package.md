# 15 — Fix org package: Transaction handling and unused variables

**What to build:** Fix the org package build errors by replacing RunInTransaction with pgx.Tx, adding pgx import, and removing unused variables in routes.

**Blocked by:** None — can start immediately

**Status:** resolved

- [x] Replace s.db.RunInTransaction with proper pgx.Tx transaction handling
- [x] Add "github.com/jackc/pgx/v5/pgxpool" and "github.com/jackc/pgx/v5" imports
- [x] Use tx.Begin(ctx) and tx.Commit()/Rollback() for transactions
- [x] Remove unused 'role' variable in org.go
- [x] Remove unused 'orgID' variables in routes.go (6 occurrences)
- [x] Remove unused 'id' variables in routes.go (3 occurrences)
- [x] Ensure bcrypt import is at top of file (not bottom)

**Type:** task
