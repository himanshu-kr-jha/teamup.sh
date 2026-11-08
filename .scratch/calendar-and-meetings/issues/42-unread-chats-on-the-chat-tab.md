# The Chat tab shows how many chats have something unread

Status: ready-for-human
Severity: Low
Asked for by: the user, "if anyone messages it should show the badge containing the number of chats sent the message".

## Comments

2026-11-08, orchestrator: decision 52.
`web/src/features/chat/useUnreadChats.ts`, the sidebar and the bottom navigation.
Bug found by the end-to-end test and fixed: opening a conversation set its badge to zero, but a list read back from the server before it had stored the read marker brought the old count back, and nothing zeroed it again until the next message. The open conversation now stays at zero whenever the list says otherwise.
Verified end to end: three messages in one room and one in another count as 2, live on another page, on a phone along the bottom, and reading one leaves 1.
