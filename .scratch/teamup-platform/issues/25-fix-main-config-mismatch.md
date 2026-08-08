# 25 — Fix main.go: Config struct mismatch

**What to build:** Resolve the Config struct type mismatches in main.go so the unified Config can be used to initialize all services with their respective Config types.

**Blocked by:** None — can start immediately

**Status:** completed

- [x] Create a unified Config struct in main.go that embeds all service-specific Config fields
- [x] Add conversion methods or helper functions to create service-specific Config from unified Config
- [x] Update service initialization calls to use the correct Config types

**Type:** task
