# A note-taker in meetings that sends minutes to everybody

Status: ready-for-human
Severity: Medium
Asked for by: the user, "can we have our own meeting note taker, which can be present in any meeting and provide minutes of meeting to each person in that meet?"

## How it would work

A LiveKit agent joins the room as a visible participant called Notes, so everybody can see it is there.
It takes each person's audio track separately, which tells it who said what without guessing from voices.
Speech becomes text with Whisper, run on our own server (faster-whisper) or through a hosted speech-to-text service.
When the call ends, a language model turns the transcript into minutes: summary, decisions, action items with owners, open questions.
The minutes go to each attendee from the attendance TeamUp already records: in the conversation the meeting belongs to, by email, and on the meeting in the calendar; action items can become tasks.

## What it needs

A worker process beside LiveKit (Python agents SDK or Go), and a place to run speech-to-text: on our own CPU it is slow for long meetings, on a GPU or a hosted service it is fast and costs per minute.
A language model for the minutes: hosted (Claude) is the best quality; a local model keeps everything on the server.
Consent: it has to be visible, switchable per meeting, and announced, because recording people is regulated in many places.
Storage and retention rules for transcripts.

## Decide

Self-hosted only, or hosted speech-to-text and a hosted model for quality; on for every meeting, or only when somebody adds it.

## Comments

2026-11-08, orchestrator: built as decision 63, with the user's choices: Sarvam for speech to text, Claude, OpenAI or their own model such as Nemotron for the minutes, in every meeting, visible, switched off only by an administrator.
`internal/notetaker` (settings, Sarvam, Anthropic and OpenAI-compatible clients, the recorder, the session in the room, minutes, delivery), migration 0026, `web/src/features/admin/NotetakerTab.tsx`, `web/src/features/calls/MinutesPage.tsx`, `notetaker.tsx` in calls.
The recorder writes the Opus packets as they arrive into Ogg, so nothing is decoded and no audio library is needed.
Found by the browser test and fixed: LiveKit can say somebody is speaking before their audio is subscribed, and that was lost.
Verified: Go tests (clips only of speech, the 30-second limit, a cough not sent, the providers' requests, the whole flow from two people joining to both notified and emailed, and who may read the minutes), and `web/e2e/meetings.y-notes.spec.ts` on the real LiveKit with stand-ins for Sarvam and Claude, three runs in a row.
The browser tests now play speech-like audio as the microphone (`e2e/fixtures/speech.wav`), because Chromium's beep is too short to count as talking.
Not verified: real Sarvam and real Claude, a real meeting's voices, and on the VM, where the server has to reach LiveKit's media ports from inside its container.
