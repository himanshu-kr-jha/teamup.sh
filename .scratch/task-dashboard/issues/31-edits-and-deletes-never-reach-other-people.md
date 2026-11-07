# 31 - A message you edit or delete stays unchanged on everybody else's screen

Status: resolved
Type: bug
Severity: high

## What is wrong

Both ends of the path are missing.

Server:
- The client edits and deletes over REST: `channelsApi.editMessage` and `channelsApi.deleteMessage` (`web/src/shared/api/client.ts`) hit `PATCH/DELETE /messages/:id`.
- Those handlers (`internal/chat/routes.go:563-599`) call `Service.EditMessage` / `Service.DeleteMessage` (`internal/chat/chat.go:2029-2066`), which call `updateMessage` / `deleteMessage` (`chat.go:537-566`). None of these broadcast anything.
- Only the websocket inbound paths broadcast (`chat.go:304-316` sends an edit as a partial `chat.message`, `chat.go:360` sends `chat.delete`). The web client never uses those.

Client:
- `ChatPage.tsx` subscribes to `chat.message`, `chat.reaction`, `chat.sent` and `chat.read.receipt` only. There is no `chat.edit` or `chat.delete` handler anywhere in `web/src` (grep confirms).
- The `chat.message` handler drops a frame without `userId` (`ChatPage.tsx:~530`), which is exactly what the websocket edit frame is, and its comment claims edits are "applied by patchMessage" - nothing does that for remote edits.

## How a user hits it

Somebody posts something wrong or sensitive and deletes it. Every other person with the channel open still sees the full text until they reload or the tab regains visibility (`ChatPage.tsx:~594` invalidates on visibilitychange). Edits likewise never show up live.

## Simple fix

In `Service.EditMessage` and `Service.DeleteMessage` broadcast `chat.edit` / `chat.delete` (`{channelId, messageId, content, editedAt}` / `{channelId, messageId}`) with `BroadcastToChannel`, excluding the actor. In `ChatPage` add `on('chat.edit')` and `on('chat.delete')` handlers that call the existing `patchMessage` (same patch the local edit and delete already apply, `ChatPage.tsx:~1320-1337`). Both type names already exist in `WSMessageType`.

## Answer

`ChatPage` applies the partial `chat.message` (empty `userId`) and `chat.delete` frames through `patchMessage`, using the same `applyEdit` and `applyDelete` as local changes.
`ChatNotifications` returns early on an empty `userId`, so an edit makes no sound, badge or notification.
A closed thread's cache is marked stale so it cannot show the old text.
Tested in `features/chat/liveChanges.test.ts`.

Backend half done, see below.
`EditMessage` and `DeleteMessage` broadcast, and the socket handlers now call them, so there is one path.
The frames are under `## Comments`.
Tests in `internal/chat/ws_inbound_test.go`: `TestARESTEditReachesTheChannelInTheFrameTheSocketPathSends:534`, `TestARefusedRESTEditReachesNobody:563`, `TestARESTDeleteReachesTheChannel:577`.

## Comments

Backend contract for the frontend handlers, as implemented.
`PATCH /messages/:id` and `DELETE /messages/:id` now broadcast to the channel's members, excluding whoever made the change (all of that user's sockets, so their other tabs do not hear it).
The WebSocket `chat.edit` and `chat.delete` frames go through the same service methods, so both paths send exactly this.

An edit is NOT a `chat.edit` frame, although the Simple fix above suggests one.
It reuses the frame the socket path already sent, a partial `chat.message`:

```json
{"type":"chat.message","payload":{"message":{
  "id":"<messageId>",
  "channelId":"<channelId>",
  "userId":"",
  "userName":"",
  "content":"<text as stored>",
  "createdAt":"0001-01-01T00:00:00Z",
  "editedAt":"2026-11-06T14:10:20.789+05:30"
}}}
```

Tell it from a new message by `userId` being empty.
Patch only `content` and `editedAt`, never `createdAt`, `userId` or `userName`.

A delete:

```json
{"type":"chat.delete","payload":{"messageId":"<messageId>"}}
```

It carries no `channelId`, and `patchMessage` does not need one.

Frontend trap: `ChatNotifications.tsx` listens to `chat.message` and only skips your own messages, so until it also returns early on an empty `userId`, every remote edit plays the chime, bumps the unread badge and raises a "New message" desktop notification.
That path was dormant while nothing sent edits to the web client, and this backend change wakes it, so the two handlers should ship together.
