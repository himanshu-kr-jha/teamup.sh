# 35 - Messages are marked read (and the tick turns) while the chat tab is in the background

Status: resolved
Type: bug
Severity: medium

## What is wrong

`ChatPage.tsx` marks the open channel read without checking the tab is visible:

- `ChatPage.tsx:~563` `chat.message` handler calls `markChannelRead` whenever `incoming.channelId === channelId`.
- `ChatPage.tsx:~638-644` the effect keyed on the last message id does the same.
- `markChannelRead` (`:659-685`) zeroes the unread badge immediately and POSTs the read marker, which now also broadcasts `chat.read.receipt` (`internal/chat/chat.go` `MarkRead` / `HandleRead` / `announceRead`).

`ChatNotifications.tsx:50` correctly treats a hidden tab as not watching, so the same message raises a desktop notification and a sound while ChatPage simultaneously marks it read.

## How a user hits it

A colleague leaves TeamUp open on /chat in a background tab. You message them. Their notification fires, but your tick turns to read (with the new live receipts, instantly), and when they come back the channel has no unread badge, so the message is easy to miss.

## Simple fix

Guard both call sites with `document.visibilityState === 'visible'`, and add a `visibilitychange` listener that calls `markChannelRead` for the latest message when the tab becomes visible.

## Answer

Both read paths now require a visible tab, and `whenVisible` in `features/chat/composerDraft.ts` marks the latest message read when the tab comes back.
Tested in `composerDraft.test.ts`, with a fake document.
