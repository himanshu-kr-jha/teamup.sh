# Mention people with @

Status: ready-for-human
Severity: Low
Asked for by: the user, "add way to mention members in the chat by @username or @name and that means notification should reach and in chat names, it should show @".

## Comments

2026-11-08, orchestrator: decision 58.
`internal/chat/chat.go` (usernames, trailing punctuation), `web/src/features/chat/mentions.ts`, `MentionUI.tsx`.
Verified: `mentions_days_test.go`, `mentions.test.ts`, and end to end: suggestions, @Name in the message, Alice's notification.
