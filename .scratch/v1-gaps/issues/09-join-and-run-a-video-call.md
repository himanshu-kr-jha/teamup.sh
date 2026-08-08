# 09 — Join and run a video call

**What to build:** Joining a call actually connects you to it: you see and hear the other participants, and can control your own microphone, camera and screen.

This is the largest remaining gap. The call view is a placeholder that renders the words "LiveKit Call Integration" and nothing else — the LiveKit React SDK is installed and entirely unused. The server side is ready: joining issues a scoped access token for the call's real room, refuses ended calls and records attendance.

**Blocked by:** 08 — Start a call from the UI.

**Status:** ready-for-agent

- [ ] Joining a call connects to its room and shows the participants' video and audio
- [ ] Microphone, camera and screen share can each be toggled, and other participants see the change
- [ ] Participants joining and leaving update the view live
- [ ] Leaving returns to the call list and records that you left
- [ ] The last participant leaving, or the host ending the call, moves it to history
- [ ] Denied camera or microphone permission produces a usable explanation rather than a blank tile
- [ ] A failure to connect is reported, and does not leave the user staring at an empty room
- [ ] Audio-only calls work without requesting the camera
