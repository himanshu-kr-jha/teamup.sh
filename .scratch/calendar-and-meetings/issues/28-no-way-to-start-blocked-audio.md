# Joining from a fresh page can be silent, with nothing saying so

Status: ready-for-human
Severity: Low to Medium

`RoomAudioRenderer` is mounted in `web/src/features/calls/ActiveCallProvider.tsx:309`, and `CallViewPage` joins on mount with no click.
Nothing in `web/src` uses `StartAudio`, `room.startAudio()`, `canPlaybackAudio` or `AudioPlaybackStatusChanged`.

In livekit-client 2.21.0, a refused `play()` emits `AudioPlaybackFailed` and the room emits `AudioPlaybackStatusChanged(false)` (`src/room/track/Track.ts:187`, `src/room/Room.ts:2176`).
Nothing retries on a later click. Only `room.startAudio()` does.

## Reproduction

Read from the code and the library source. Not run in a browser, because the browser tester owns that setup and headless autoplay rules differ.

1. In a browser with no media engagement for the site, such as Safari, Firefox or a new Chrome profile, open a pasted `/calls/<id>` link in a new tab, or reload.
2. The page joins by itself with no gesture, and audio elements are refused.
3. The call shows people and video, and they cannot be heard. No button or message says why, and clicking around does not fix it.

The new Copy link feature makes this entry path the normal one for a link.

## Suggested minimal fix

Inside `LiveKitRoom`, use `useAudioPlayback(room)` from `@livekit/components-react`, or render `<StartAudio label="Click to hear the call" />`, in both the full and the corner frame.

## Also

`useChime` is used by `useJoinAnnouncements` (`ActiveCallProvider.tsx:108`), and its `AudioContext` is never closed when `CallFrame` unmounts.
Each call in one tab leaves a running context behind, until the tab closes.
Call `stop` from the `useJoinAnnouncements` effect's cleanup.

## Comments

2026-11-07, senior developer:
`BlockedAudioPrompt` in `ActiveCallProvider.tsx` uses `useAudioPlayback` and shows **Turn on sound** in both frames whenever LiveKit says audio cannot play.
`useJoinAnnouncements` closes its `AudioContext` on cleanup.
Verified: the real-browser test under "ticket 28" makes `HTMLMediaElement.play` refuse until a click, as a browser does, and goes red with the prompt taken out.
Not verified on real Safari or Firefox autoplay rules, since the suite runs Chromium with a stand-in for the refusal.
