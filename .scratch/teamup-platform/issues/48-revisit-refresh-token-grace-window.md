# 48 — Revisit the 30-second refresh-token grace window

**What to build:** Decide whether the grace window introduced by ticket 35 is the right trade, or whether refresh-token replay should revoke the token family instead.

**Blocked by:** None

**Status:** open — flagged for review, landed as-is

- [ ] Decide: keep the grace window, or switch to reuse-detection with family revocation
- [ ] If keeping it, document the accepted risk in the deployment docs
- [ ] If changing it, solve the concurrent-refresh problem another way (single-flight in the client, or a server-side lock keyed on the session)

**Type:** task

## Evidence

Ticket 35 fixed a real defect: ten concurrent refreshes returned **1×200 and 9×401**, because rotation revoked the presented token before inserting its replacement with no lock between. Any multi-tab PWA — the primary client for this product — signed the user out.

The fix runs rotation in one transaction with `FOR UPDATE` and adds a **30-second grace window**: a caller presenting an already-rotated token within that window receives an access token on the replacement's session, but **no new refresh token**. Outside the window, or after an explicit sign-out, it is refused.

## The trade

OAuth 2.0 Security BCP treats refresh-token replay as the signal that a token has been **stolen**, and recommends revoking the entire token family on detection. A grace window deliberately softens that: for 30 seconds, a replayed token is treated as a legitimate race rather than as evidence of theft.

Arguments for keeping it as-is:

- The failure it fixes is certain and constant; the attack it weakens is speculative and narrow.
- No refresh token is issued to the replayer, so an attacker gets one short-lived access token, not a persistent foothold.
- 30s is far shorter than the access-token lifetime, so the extra exposure is bounded.

Arguments for changing it:

- Reuse detection is the only mechanism that notices a stolen refresh token at all. Softening it means theft is never detected, not merely detected late.
- The concurrency problem has other solutions — single-flight on the client, or a per-session server lock that makes losers wait for the winner rather than accepting a replay.

## Recommendation

Keep it for v1 and document it. Revisit before any deployment handling regulated data, where undetected refresh-token theft is likely to fail an audit regardless of the blast radius argument.

See [[35-persist-jwt-signing-key]].
