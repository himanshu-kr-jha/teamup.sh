# Voice and video messages in the app's look, and a loading screen

Status: done

## What

The user asked to "update the UI of self video record and audio in chat to match the theme of app, and add a loading screen so that it comes up whenever there is delay, in theme of app."

## Built

- `web/src/features/files/VoicePlayer.tsx`: a play button in the organization's colour, the recording's real waveform (decoded in the browser, made up from its name when it cannot be read), click or drag or arrow keys to move, the time, 1×, 1.5× and 2×, download; one voice message plays at a time.
- `web/src/features/files/VideoNotePlayer.tsx`: a rounded square with a play button in the organization's colour, a progress line, the length, sound, full screen and download; a placeholder until the first frame instead of black.
- `web/src/features/files/media.ts`: the waveform, the time, and the length of a browser recording, which says Infinity until worked out.
- The voice recorder shows a live level of what the microphone hears; the video recorder has a camera's shutter button, a progress line to the limit, and "Starting the camera" until the first frame.
- `LoadingScreen` (the router's fallback) and the same screen in `index.html` for before the code arrives, so the first screen does not jump; `TopProgress`, a thin bar along the top when a page moves, a page's first data is slow, or something is being saved, after 350 ms and not for background checks.

## Verified

Browser: `meetings.y-chat.spec.ts` now plays a voice message (length, playing, position moves, speed) and a video message; `meetings.y-loading.spec.ts` slows the server to see the loading screen in the organization's colour and the bar, and that a quick page shows no bar.
Looked at in light and dark, including `index.html` with no code and no stylesheet.
Unit: `media.test.ts`.
