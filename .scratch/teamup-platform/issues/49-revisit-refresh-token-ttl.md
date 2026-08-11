# 49 — Revisit the refresh-token TTL change (7d → 30d)

**What to build:** Confirm or revert the session-lifetime change that arrived as a side effect of ticket 35.

**Blocked by:** None

**Status:** open — flagged for review, landed as-is

- [ ] Decide the intended refresh-token lifetime for v1
- [ ] Make it configurable rather than compiled in, if operators should control it
- [ ] Align map.md, the code, and the deployment docs on one number

**Type:** task

## Evidence

Ticket 12 (`12-oidc-auth.md`) and map.md both specify **refresh token rotation (30d)**. The implementation used **7 days**. While fixing the signing key, ticket 35 changed `RefreshTokenTTL` from 7d to 30d to match the spec.

Nothing in ticket 35 asked for this. It is a documented-intent alignment, but it is also a **fourfold increase in how long a stolen refresh token stays useful**, made as a side effect of an unrelated fix.

## Why it needs a decision rather than acceptance

The spec number and the implemented number disagreed, and it is not obvious which was the considered one. 7 days may have been a deliberate tightening that never made it back into map.md, or it may have been an oversight. Picking the spec number is defensible, but it should be a choice rather than a default.

This interacts with [[48-revisit-refresh-token-grace-window]]: a longer refresh lifetime raises the value of the reuse-detection the grace window softens. The two should be decided together, not separately.

Worth considering whether this belongs in configuration at all — self-hosted operators in different regulatory environments will reasonably want different answers, and a compiled-in constant forces a rebuild.

See [[35-persist-jwt-signing-key]].
