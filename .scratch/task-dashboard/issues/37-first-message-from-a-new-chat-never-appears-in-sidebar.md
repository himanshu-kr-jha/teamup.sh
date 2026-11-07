# 37 - A new direct message or channel you are added to is missing from the sidebar until reload

Status: resolved
Type: bug
Severity: medium

## What is wrong

- The channel list is `['channels']` with the global 5 minute `staleTime` (`ChatPage.tsx:144-148`).
- When somebody opens a DM with you or adds you to a channel, the server delivers `chat.message` to you (membership is read from the database at send time, `internal/websocket/hub.go:444-475`), but nothing refreshes the list. `chat.membership.changed` is defined (`pkg/protocol/messages.go`) but never sent by the server and never handled by the client.
- `ChatNotifications.tsx:33-` bumps `unreadCount` with a `map` over the cached list (`:~65`), which does nothing for a channel that is not in it.

## How a user hits it

A colleague starts a DM with you. You hear the chime and get a desktop notification, but the conversation is not in your sidebar and has no badge. Clicking the notification opens `/chat/<id>` with no channel header.

## Simple fix

In the `chat.message` handler in `ChatNotifications.tsx`, if `incoming.channelId` is not in `queryClient.getQueryData(['channels'])?.data`, call `queryClient.invalidateQueries({ queryKey: ['channels'] })`.

## Answer

`ChatNotifications` invalidates `['channels']` on `chat.membership.changed`, through `watchChatChanges` in `features/chat/liveChanges.ts`.
This replaces the ticket's unknown-channel check, because the server now announces the change.
Tested in `liveChanges.test.ts`.

Backend half done, see below.
`OpenDirectMessage`, `AddMember` and `CreateChannel` (org-wide types only) send `chat.membership.changed` when a membership is actually created.
The frame is under `## Comments`.
Tests in `internal/chat/ws_inbound_test.go`: `TestStartingADirectMessageTellsTheOtherPerson:623`, `TestBeingAddedToAChannelTellsThem:650`, `TestCreatingAnOrgWideChannelTellsTheOrganization:678`.

## Comments

Backend contract for the frontend handler, as implemented.
On a newly created membership the server sends this to every member of the channel, the new ones included:

```json
{"type":"chat.membership.changed","payload":{
  "channelId":"<channelId>",
  "userIds":["<id of each person just added>"],
  "action":"added"
}}
```

The frontend only needs to invalidate `['channels']` when it arrives, and does not need to look at `userIds`.

- Starting a new DM: `userIds` holds both people, and both receive it, so your own other tabs and a caller whose DM was created by a missed call stay right too.
- `POST /channels/:id/members`: `userIds` is the one person added. Existing members receive it too, which also refreshes their member count. A repeat add sends nothing.
- Creating a `public` or `announcement` channel: `userIds` is everybody in it, and everybody in it receives it.

Deliberately not sent: creating a private channel (nobody else is in it), joining an org-wide channel yourself or rejoining one (you did it, and it would ping the whole organization for each first visit), and enrolling a brand new account in open channels (they have no socket yet and load the list on sign-in).
`action` is only ever `added` for now, because nothing announces removals.
