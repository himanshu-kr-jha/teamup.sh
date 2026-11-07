# 39 - If sending a chat message fails, the text and attachments you typed are gone

Status: resolved
Type: bug
Severity: medium

## What is wrong

`handleSend` (`web/src/features/chat/ChatPage.tsx:~685-702`) clears the composer (`setMessage('')`, `setStaged([])`) before the request is made.
`sendMessageMutation.onError` (`:482-485`) rolls back the optimistic cache entry and shows a toast, but never restores the composer. The message is not kept anywhere, and the staged attachments were already uploaded and are not re-offered.

## How a user hits it

Write a long update, lose the connection or hit a 403/429 (for example posting to an announcement channel as a non-admin) and press Enter. The toast says it could not be sent, and the draft is gone. Attachments have to be uploaded again.

## Simple fix

In `onError`, for non-reply sends, `setMessage((current) => current || variables.content)` and restore the staged files from the optimistic copy (or keep `staged` until `onSuccess`).

## Answer

A failed non-reply send puts the text and staged attachments back in the composer, ahead of anything typed since, and only in the conversation it was written in.
Tested in `features/chat/composerDraft.test.ts`.
