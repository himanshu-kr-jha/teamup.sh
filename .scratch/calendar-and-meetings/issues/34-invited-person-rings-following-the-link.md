# An invited person's page rings briefly when they follow the link

Status: ready-for-human
Severity: Low
Found by: browser tester, who wrote `resetOscillators` around it in ticket 09.

## Steps

1. Olivia makes a call and invites Alice.
2. Alice, with TeamUp open and being rung, follows the link to it.

## What happens

The Incoming call dialog goes on ringing until the token arrives.
`IncomingCallDialog` only knew about the call once `activeCall` was set, and even then one ring got out before its effect removed it.

## Comments

2026-11-07, senior developer: `IncomingCallDialog` works out which call to ring for while rendering, leaving out the call you are in and the call whose page you are on.
Verified by "stops the ringing as soon as you are on your way in" in `web/e2e/meetings.y-fixes.spec.ts`, with the token slowed by 4 seconds, counting oscillators.
My first version of that test opened the link as a fresh page, and passed with the fix taken out: there the socket's frame arrives before the app is listening for it, so nothing rings.
The rewritten one follows the link inside the app while it rings.
