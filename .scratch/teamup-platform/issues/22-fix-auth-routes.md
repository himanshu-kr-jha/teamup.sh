# 22 — Fix auth routes: Unused variables and base64 import

**What to build:** Fix the auth routes by removing unused variables and adding base64 import.

**Blocked by:** 14 — Fix chat package (WSConnection interface must be defined first)

**Status:** completed

- [x] Remove unused 'verifier' variable in routes.go line 115
- [x] Remove unused 'userID' variable in routes.go line 273
- [x] Remove unused 'rt' variable in routes.go line 340
- [x] Add "encoding/base64" import for parseLogoutToken function
- [x] Fix userID type conversion in UnlinkAccount (string → uuid.UUID)

**Type:** task
