# 01 — Shared database test harness

**What to build:** A single way for any package to write a test that talks to a real Postgres, with each test starting from a known-clean slate and unable to disturb a test in another package.

Today the harness is copy-pasted per package and every copy shares one database, so tests interfere: seeding an organization in one package breaks another package's assertion that only one organization exists. The suite is currently pinned to serial execution to hide that. Every ticket below this one writes database-backed tests, so fixing the harness first is what makes the rest cheap.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] One shared helper provides a connected pool to a disposable database, skipping cleanly when no test database is configured
- [ ] Each test gets isolation: data it writes is not visible to, and does not break, any other test
- [ ] The existing wizard and auth suites are migrated onto the helper and no longer define their own
- [ ] Tests pass with packages running in parallel; the serial-execution pin is removed from the pre-commit config and Makefile
- [ ] Running the suite twice in a row passes both times without manual database cleanup
- [ ] The repo documents in one place how to create and migrate the test database
