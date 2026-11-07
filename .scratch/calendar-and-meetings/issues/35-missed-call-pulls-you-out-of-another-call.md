# An earlier call ringing out pulls you out of the call you are in

Status: ready-for-human
Severity: Medium
Found by: senior developer, when the full suite's `meetings.z-shots.spec.ts` failed with the organizer on a chat page in the middle of a call.

## Steps

1. Olivia calls Pat, who does not pick up, and Olivia moves on.
2. Olivia joins another call.
3. Pat's ring times out, or Pat declines the first call.

## What happens

The `call.missed` frame takes Olivia to the conversation with Pat, and the call she is in shrinks into a corner.
Putting somebody into that conversation is right when they are waiting on that call, and not when they are busy in another.

## Comments

2026-11-07, senior developer: the handler in `ActiveCallProvider.tsx` now does nothing while you are in a different call.
Verified by "does not pull you out of the one you are in" in `web/e2e/meetings.y-fixes.spec.ts`.
The conversation still gets its note.

2026-11-07, senior developer, ticket 10: that fix was incomplete, and the full suite caught it again in `meetings.z-shots.spec.ts`, with the organizer on a chat page and her call in the corner.
The frame could still arrive in the moment before she was in the new call, when no call was active, and it also pulled somebody who had moved on from any page, in no call at all.
It now moves only somebody still waiting on that call: the one this tab last set out to be in, or the one whose page it is on.
Both cases are new tests in `meetings.y-fixes.spec.ts`, red on the old build, the first with the join held open so the timing is not luck.

