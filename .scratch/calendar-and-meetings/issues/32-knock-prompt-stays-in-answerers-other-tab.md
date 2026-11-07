# The knock prompt stays in the other tab of whoever answered

Status: ready-for-human
Severity: Low
Found by: browser tester, reported to the orchestrator and not filed at the time.

## Steps

1. Olivia is in a call and has TeamUp open in a second tab.
2. Sam knocks. The prompt shows in both of her tabs.
3. She presses Deny in the first tab.

## What happens

The prompt stays in the second tab until she answers it there, and is told "has already been answered".
`AnswerKnock` sent `call.knock.resolved` to everybody who could have been prompted except whoever answered.

## Comments

2026-11-07, senior developer: `AnswerKnock` in `internal/calls/knock.go` now sends it to whoever answered as well, and the client already ignores a prompt it does not have.
Verified by `TestWhoeverAnswersAKnockIsToldToo` (Go, red first) and "goes away when the same person answers in the first" in `web/e2e/meetings.y-fixes.spec.ts` (real browser, two tabs of one person).
