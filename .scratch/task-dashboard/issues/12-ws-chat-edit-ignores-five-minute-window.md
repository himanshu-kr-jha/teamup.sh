# 12 - Editing over the WebSocket ignores the five minute edit window

Status: resolved
Type: bug

## What is wrong

The REST edit (`PATCH /messages/:id`) refuses edits after `EditWindow` (5 minutes) and refuses deleted messages. The WebSocket `chat.edit` frame does not: it checks only that the caller owns the message, then rewrites it. Anyone can silently rewrite what they said last week, in front of everybody, by sending the frame instead of using the button. The 5-minute rule exists so that a conversation people have already read stays what was said.

Also the live broadcast sends `payload.Content` raw, while the stored text went through `sentinelStripper`, so other open tabs show different text than a reload does.

## Where

- `internal/chat/chat.go:283-325` `HandleEditMessage` (no time window).
- `internal/chat/chat.go:2029-2054` `EditMessage` has the window (`EditWindow`, `chat.go:2027`).
- `internal/chat/chat.go:~555` `getMessageOwner` only returns owner, not `created_at`.

## How to reproduce

In `internal/chat/ws_inbound_test.go` style: send a message, `UPDATE messages SET created_at = NOW() - interval '1 hour'`, dial as the author, send `chat.edit` with new content. Today the row content changes; it should not (REST `EditMessage` returns forbidden for the same message).

## Simple fix

Make `HandleEditMessage` call `s.EditMessage(ctx, messageID, userID, payload.Content)` (it already does ownership, deleted and window checks) and broadcast the stored content.

## Answer

`HandleEditMessage` now calls `EditMessage`, so the window, ownership and deleted checks are the REST ones, and the broadcast carries the text as stored (`updateMessage` returns it).
Tests in `internal/chat/ws_inbound_test.go`: `TestAChatEditFrameIsRefusedWhereTheRESTEditIs:334`, `TestAChatEditFrameFromSomebodyElseIsRefused:379`, `TestAChatEditFrameBroadcastsTheStoredText:407`.
The deleted and somebody-else cases already passed before the fix and stay as guards.
The sender still gets no error frame on refusal, as with the other chat frames; it is logged.
