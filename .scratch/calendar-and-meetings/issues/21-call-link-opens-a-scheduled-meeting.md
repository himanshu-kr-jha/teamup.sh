# Following a link opens a scheduled meeting, whenever it is, and locks it

Status: ready-for-human
Severity: High

Decision 35 says Join is offered only from 30 minutes before, "so a stray click days ahead must not lock it", and that this is the page's limit.
The limit is on two buttons and not on the page.

## Where

- `web/src/features/calls/CallViewPage.tsx:698`: the page calls `join(callId)` on mount with no check of the call.
- `web/src/features/calls/ActiveCallProvider.tsx:180`: that posts `/calls/:id/token`.
- `internal/calls/routes.go:443`: `openForJoin` opens it, and it can no longer be moved (409 from `internal/calendar/calendar.go:697`).
- Ways to reach that page early: the Copy link button on every call card (`CallsPage.tsx`), and a `call.knock` notification (`NotificationBell.tsx:90`), which sends the organizer straight in.

## Reproduction

1. Alice schedules a review for next Friday and invites Bob. She copies the link from Calls and pastes it into chat.
2. On Monday Bob clicks it. He is put into the call, and the row goes `scheduled` to `active`.
3. Alice tries to move the meeting: 409 "this meeting has already started".
4. Bob leaves. The row is `ended`, and ticket 20 applies on Friday.

A stranger knocking on Monday has the same effect: the organizer clicks the notification, is taken to `/calls/:id`, and opens the meeting just by looking.
Verified by reading the page and the provider; `internal/calendar/meeting_start_test.go` shows that a token opens the row at any time.

## Suggested minimal fix

Have `CallViewPage` read `GET /calls/:id` first.
If it is `scheduled` with a `scheduledStart` more than `JOIN_EARLY_MINUTES` ahead, show when it opens and a deliberate "Join now" button rather than asking for a token.
Keep the server as it is.
The knock notification should go to a page that shows the prompt without joining.

## Comments

2026-11-07, senior developer:
`CallViewPage` now reads the call first and decides with `lobbyFor` in `web/src/features/calls/lobby.ts`: a link to a meeting nobody has started stops at a lobby, and only a click asks for a token.
That goes for the 30 minutes before as well, which is wider than this ticket suggested, because the user's reading is that merely opening the page must not count as joining: decision 35.
The Join buttons on Calls and the calendar pass `state: { join: true }` and go straight in. The server is unchanged, and the meeting still locks once somebody joins, as the user decided.
The knock notification lands on the lobby, and `KnockCenter` reads the pending requests for the call being watched there, so the prompt shows without starting anything.
Somebody let in goes straight in, unless the meeting is more than 30 minutes ahead, when they stop at the lobby.
A cancelled meeting now says so rather than "This call has ended".
Verified: Vitest on `lobbyFor`, and four real-browser tests under "ticket 21", three of which go red with the decision taken out, including moving the meeting after the page was opened and reloaded.
