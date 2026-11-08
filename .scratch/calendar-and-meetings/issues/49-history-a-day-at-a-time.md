# Chat history a day at a time

Status: ready-for-human
Severity: Low
Asked for by: the user, "load one day messages, for all the chats and when someone tries to go previous dates, load accordingly, so that it feels fast and seamless".

## Comments

2026-11-08, orchestrator: decision 59.
`internal/chat/history_days.go`, `GET /channels/
Verified: id/messages?unit=day&tz=`, and the chat page.:the Go test with Kolkata days and a gap, and end to end with sixty messages on each of two days a week apart.
