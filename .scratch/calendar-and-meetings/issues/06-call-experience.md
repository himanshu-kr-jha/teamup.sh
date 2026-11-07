# Call experience

Status: resolved
Blocked by: 02

Screen share proved for everybody (decisions 20, 21), join sound and toast, tile layout (decision 19), copy link, knock screen, admit and deny prompt, call log panel.
Reproduce the current screen share behaviour in a real browser first.

## Comments

### 2026-11-07 - Senior frontend developer: built and seen working

Files: `web/src/features/calls/{tileLayout,joinPing,knock,callLog,copyLink}.ts`, `CallControls`, `CallStage`, `CallLogDialog`, `KnockCenter`, `KnockScreen`, `CallViewPage`, `ActiveCallProvider`, `IncomingCallDialog`.

**What reproducing screen share found (real Chromium, real LiveKit, a non-organizer)**
Share was visible and working on a computer and at 320 and 375 wide, so permissions were never the problem. What was actually wrong:
- The title bar was white on white in light mode, because LiveKit's theme sets white text on everything inside it. The call's state and the minimise and expand buttons could not be seen.
- The compact view had no share button, and no microphone or camera either.
- The compact grid overflowed its window.
- `tracks.find` hid any second share.
- The control bar's buttons had no accessible names.
- Somebody already in a call could still be ringing; it then timed out and told the caller they did not answer.
- One person declining a group call dropped the caller, against what the server says that means.
- `LiveKitRoom` got a new `onError` on each render, which is in the effect that connects.

**Backend bug found by a screen**: `Service.EndCall` called `DeleteRoom` with the room's sid. LiveKit deletes by name, so it answered `not_found` and every teardown failed, leaving a caller connected to a call marked ended after a one-to-one decline. Fixed test-first against real LiveKit: `internal/calls/endcall_livekit_test.go`.

**Seen in a real browser against the real server and LiveKit**: share by a member on a computer, at 375, at 320 and from the compact window; two shares at once, clicking one onto the stage; join toast and sound for others and none for yourself or for who was already there; a burst of three arrivals as one toast and one chime (counted oscillators, not listened to); knock, deny, countdown, retry after the real minute, admit with no reload; prompt clearing for the other member; knock with nobody connected answered from `/chat`; call ended while waiting; ended call by direct link; call log with knocked, turned away, let in, joined, shared, in the order they happened; the log refused to somebody without access; `attendanceRecorded: false` wording; copy link; light, dark, fake camera video in tiles; somebody signed out following a link, signing in and arriving at the door, which used to land on chat; 21 and 24 real LiveKit connections paging at 16; dragging the compact window; a browser with `getDisplayMedia` removed shows no share button and says why.

**Not seen**
- A real camera is only the fake device, and nobody has heard the sounds: the check was that oscillators were created.
- Whoever is speaking staying on the page is tested in `promoteSpeakers` only. The fake participants publish no audio, so it was not exercised live.
- A real phone browser. `getDisplayMedia` missing was simulated by deleting it in desktop Chromium.
- Autoplay refusal: tests run with `--autoplay-policy=no-user-gesture-required`. The code drops a sound it cannot play at once rather than queueing it, and the toast still shows, but the refusal itself was not exercised.
- The answerer's second tab: `call.knock.resolved` excludes whoever answered, so another tab of theirs keeps the prompt until it is clicked, when it clears with "has already been answered".

**Awkward**
- Ticket 09 fixtures treat console errors as failures. The door produces an expected 403 on the token and 429 on a too-soon knock, and the browser logs both.
- `/calls/:id/requests` on a reloaded standing room logs an expected 403 and is caught. Going in by the card does not, as the page knows it is standing.
