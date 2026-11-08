# Calendars, meeting control, scrum attendance and tasks you were added to

Status: ready-for-agent
Target: one startup, 50 people, self-hosted, already live on one VM.

## What was asked

1. Everyone has their own calendar.
   A call somebody is added to or creates appears on it, from this time to this time.
   Somebody can block time for their own work and free up slots for meetings.
2. Anyone in a call can share their screen.
3. Somebody joining a call makes a sound and a notification.
4. Better video tiling.
5. If a link to a private meeting reaches somebody who was not invited, members are pinged and can let them in.
   Everything is traced: who joined, and when.
6. An admin can make one standing meeting for the morning scrum and one for the evening scrum, used by everyone.
   Each day's attendance is saved: who came, and when.
7. A task somebody else added you to shows up among your work and counts as contribution.
8. All of this is visible to managers and admins.

## Vocabulary

- **Meeting**: a call scheduled ahead of time, with a start, an end and invited people.
- **Focus block**: time somebody has blocked for their own work.
- **Open slot**: time somebody has marked as free for meetings.
- **Standing meeting**: a permanent room an admin creates, such as the morning scrum, with a time of day and weekdays.
- **Occurrence**: one day of a standing meeting, which is one row in `calls`.
- **Knock**: a request to join a private call from somebody who is not a member of it.
- **Member of a call**: invited to it, admitted to it, or a member of its channel.
- **Added to a task**: put on `task_watchers` by somebody else (decision 38). That table is what the task page already shows as the people on a task.

## Decisions

These are readings of an ambiguous request, and they are cheap to change.

1. **Tasks do not go on the calendar.**
   The request says "if anyone assigns a task, let's not go to task", read as: the calendar is for time, and works like assigning does, in that being added puts it on your calendar without you doing anything.
2. **A scheduled meeting is fixed on an invitee's calendar.**
   Only the organizer, or an org admin, can move or cancel it.
   There is no accept or decline.
3. **Conflicts warn and do not refuse.**
   Scheduling over somebody's focus block or meeting shows who conflicts, and the organizer can go ahead.
4. **Calendar visibility.**
   Anybody in the organization sees another person's times and whether each is busy, focus or open, with no titles.
   Whoever has authority over that person, by the existing `hasAuthorityOver`, sees titles and call details as well.
5. **Past calls show what really happened.**
   An unscheduled call appears on the calendar of everybody invited, with the time they were really in it where there is one.
6. **Attendance comes from LiveKit webhooks, not from issuing a token.**
   A token is only permission, so somebody who never connected was not there.
   One person can have several sessions in one call, because people drop and rejoin, and every session is kept.
7. **Knocking is for logged-in people in the organization.**
   Guests with no account are a non-goal.
   Org admins and managers knock like anybody else, because being able to read logs is not the same as walking into a private call.
8. **Any member of the call can admit or deny.**
   Only members who are connected at that moment are pinged in the call.
   If nobody is connected, the organizer and invited people get a notification, and the request waits.
   A pending request ends when the call ends.
9. **An admitted person becomes a member**, so reconnecting does not knock again.
   A denied person can knock again, at most once a minute.
10. **The audit trail is append-only.**
    `call_events` records knocked, admitted, denied, joined, left, screen share started and stopped, with who did it and when.
    There is no edit or delete endpoint.
    Times for joined and left are the times LiveKit reports.
11. **Who may read a call's log:** its members, an org admin or owner, and a manager with authority over at least one of its members, who gets the full log for that call.
    Everybody else gets 404.
12. **Standing meetings are organization-wide, and everyone in the organization may join without knocking.**
    Only an org admin or owner may create, edit or archive one.
    Archiving hides the room and keeps its history.
13. **Standing meetings have a time zone**, picked by the admin and defaulted from their browser.
    An occurrence's date is worked out in that zone, so a scrum belongs to the right day wherever the server is.
14. **Attendance per occurrence:** every person's first join, last leave, total minutes present and minutes late against the scheduled start.
    Everybody active who was in the organization on that date and did not join is listed as absent.
    Absent is derived on read, while joined and left are stored.
15. **Who may read attendance:** an org admin or owner sees everybody, a manager sees the people they answer for, and everybody else sees only themselves.
    A team filter works as it does on the dashboard.
16. **Reminders:** an invited person gets a notification when invited, when a meeting moves or is cancelled, and 5 minutes before it starts.
    The organizer also gets the 5-minute reminder, once, and no notification for their own changes.
    A reminder is claimed with an atomic update, so several replicas send it once.
17. **Added to a task counts, and is shown separately.**
    The dashboard gains an **Added to** bucket and count for open tasks somebody else put them on and they are not assigned to, for the employee and for managers and admins.
    Contribution gains **Added to**: tasks they were on and not assigned to, in the same range as involved.
    `completed` keeps meaning assigned to them, so one task is not credited as completed twice.
    A saved scrum snapshot records it too.
18. **Being added to a task notifies the person**, if it does not already.
19. **Tile layout is a tested pure function.**
    It picks the number of columns that makes tiles largest at 16:9 for the space available, with the one exception in decision 37.
    Above 16 people the grid pages, and whoever is speaking stays on the current page.
20. **Several people can share at once.**
    One takes the stage, the others sit in the strip, and clicking one puts it on stage.
21. **A phone browser that cannot capture its screen shows no share button.**
    That is a limit of those browsers, and it is stated rather than worked around.
22. **A scheduled meeting starts when somebody joins it, and it rings nobody.**
    A meeting is a `calls` row with status `scheduled` until somebody who may join asks for a token, which opens it.
    The page asks for a token only when somebody means to join: see decision 35.
    People were told when invited and reminded before the start, so there is no ring, no device push and no redelivery on reconnect, and a meeting nobody came to is not a missed call.
    An invitee cannot end or decline one that has not started.
    A meeting that has opened can no longer be moved or cancelled: it answers 409.
23. **A meeting or occurrence whose room emptied comes back.**
    A scheduled meeting reopens until its scheduled end, an occurrence for the rest of its day in its zone, or to the end of its slot if that runs past midnight, both on the same row.
    An ordinary call that has ended stays ended.
    So a meeting whose row is `ended` before its scheduled end is still on: it is reminded, it holds its slot on calendars and availability, it offers Join, and it reads as scheduled and not as finished.
    Real times replace the slot only once its scheduled end has passed.
24. **LiveKit saying a room finished ends the call**, for ordinary calls too, with the same announcement and missed call note as hanging up.
    Until now a call whose last person closed their laptop stayed active for ever.
    A session still open at that moment is closed at the time LiveKit reports, and a room somebody has connected to since is not ended.
    LiveKit retries a webhook only a few times, so every replica also asks every 5 minutes which rooms exist, and a call left alone for 10 minutes whose room is gone ends the same way.
    If LiveKit cannot be asked, nothing changes.
    Migration 0022 closes unscheduled calls still `active` a day after they started, as of the last thing known about them.
    On a calendar, somebody who never joined a call nobody closed is busy only for the ring window, 45 seconds.
25. **Picking up and attending are separate facts.**
    `call_participants.answered_at` and `calls.answered` record asking for a token, and drive ringing and missed calls, so those keep working when webhooks do not arrive.
    `call_sessions` records connections and drives everything about attendance.
26. **When no connection was ever reported, the answer is no data.**
    With no webhook sessions, nobody connecting and webhooks being down look the same, so `GET /calls/:id/log` says `attendanceRecorded: false`.
    A scrum day where people pressed join and no connection arrived lists everybody as `unknown`, not `absent`.
    A day nobody pressed join on is reliably absent, because no webhook could have been missed.
    A partial outage cannot be detected: sessions that were missed are simply missing.
27. **A standing meeting has no members.**
    Everybody may be in the room, but that makes nobody a member, so it does not open the log, which would get around decision 15.
    Only an org admin or owner, or a manager over somebody who came, reads an occurrence's log.
    The same goes for the calendar: an occurrence on your calendar names no invited people and no organizer.
    Nobody is offered a log they would be refused: attendance days, calendar items, the call list and `GET /calls/:id` carry `canReadLog`, which is the log's own rule applied to whoever is reading.
28. **`GET /calls/:id` follows the log's rule.**
    It used to give title, invitees and who joined to anybody in the organization with a link.
    Now somebody who may not read the log gets `id`, `status`, `callType`, `canKnock` and `canReadLog: false`, plus `title` if they may join anyway, as in a standing meeting.
29. **A standing meeting has a length,** `durationMinutes`, default 30, up to 480, because the calendar has to show until when.
    A slot may cross midnight and is one room on both sides of it: Join means yesterday's occurrence while its slot is running, tomorrow's within 30 minutes of its start, and otherwise today's.
    It is on everybody's calendar on the days it runs since it was created, as `busy` with source `standing`, and counts as a `meeting` conflict.
    An occurrence copies its scheduled start, so changing the time later does not make anybody late in retrospect.
30. **Attendance edges.**
    A person who came on a day the meeting does not run is listed, and nobody is absent from that day.
    Absence follows the schedule that was in force on that date, kept in `standing_meeting_schedules`, so editing the weekdays or the time rewrites no history.
    A schedule counts from the day it was set, and a slot earlier than the moment it was set has nobody absent, so a meeting created at 14:00 marks nobody absent from its 09:00 slot that day.
    Before a held day's scheduled start, only people who have come are listed: somebody early does not make everybody else absent.
    A meeting's schedule before migration 0024 is not known, and is taken to have always been what it is.
    A manager also sees themself, unless they asked for one team.
    Somebody who came and has since been deactivated stays on an org admin's unfiltered view.
    An open session counts up to now at most, and never past the call's end or the end of that day, or of its slot if that is later.
    A range covers at most 92 days.
31. **Reminders go to invited people and the organizer,** as decision 16 reads, and are at most once: a replica dying between the claim and the send loses that reminder rather than repeating it on every restart.
    A meeting already opened early is still reminded.
32. **Knocking with nobody connected also sends the frame**, marked `connected: false`, to the organizer and invited people along with the notification.
    That keeps a knock answerable from an open tab if webhooks are down and everybody only looks absent, and the prompt clears for all of them once somebody answers.
    Somebody who may already join gets 409 from knock.
33. **Limits.**
    A block must be shorter than 30 days, a meeting at most 24 hours with at most 200 invited, a calendar read at most 92 days, an availability read at most 200 people.
34. **Changing who is invited tells only the people it changes**: added people get `meeting.invited`, removed people get `meeting.cancelled`.
    A title change notifies nobody.
    When an admin moves somebody's meeting, the organizer gets `meeting.moved` too.

35. **Join is offered from 30 minutes before a scheduled meeting, and only a click on Join joins.**
    Joining opens it and an opened meeting can no longer be moved (decision 22), so a stray click days ahead must not lock it, and merely opening its page is not joining.
    So following a link to a scheduled meeting that has not opened, or a knock notification about one, stops at a lobby, whenever that is, including within the 30 minutes.
    It says when the meeting is, with **Join now** before the 30 minutes and **Join** within them, and a member can answer anybody at the door from there without starting anything.
    The Join buttons on Calls and the calendar go straight in, because pressing one is the decision.
    Somebody let in goes straight in, since asking to join was the decision, unless it is more than 30 minutes ahead: then being admitted only makes them invited, and they stop at the lobby.
    A meeting with people in it is simply joined, and somebody who may not join goes to the door, which opens nothing.
    The server still accepts an earlier token; this is the page's limit only.
36. **Knocking is a click, not automatic.**
    Somebody following a private link sees the door with **Ask to join**, so opening a link by mistake pings nobody.
37. **Two people in a space wider than it is tall sit side by side.**
    This is the orchestrator's decision, and the one exception to decision 19.
    On a typical laptop window (about 1016 by 680) stacking would make tiles about 15% larger, but a conversation reads across, and two faces stacked on a laptop looks like a mistake.
    In a space as tall as it is wide or taller, such as a phone held upright, two people stack, because that is what comes out largest.
    Three people and more follow decision 19 everywhere.
    It is one function, `bestGrid`.
38. **Only being added by somebody else counts as Added to.**
    Adding yourself to a task is following it: it does not appear in the dashboard's Added to bucket or count, in contribution, or in a saved scrum, and it raises no notification.
    `task_watchers.added_by` records who did it.
    Somebody else adding you after you followed it does count, and tells you, once; following it after being added takes nothing away.
    Contribution's **involved** still counts following a task, since it is what somebody has touched and not what they were given.
39. **What existing watchers count as (migration 0023).**
    A row from before `added_by` counts as added by somebody else only if a `task.added` notification for that person and task exists, because that has always been raised for being added by somebody else and never for adding yourself.
    Otherwise it counts as having added yourself: wrongly taking away credit for an old add is the smaller error than crediting tasks somebody only chose to follow.
    `added_by` stays empty for somebody else whose identity is not known.
    Scrums already saved are records of their day and are not rewritten.
40. **Somebody deactivated keeps nothing open.**
    They are no longer a member of any call and may not join any, their sockets close on every replica, and they are removed from any room where a session of theirs is open.
    A token already issued is not revoked and its lifetime is unchanged, as the user decided, so they can still connect with one: the `participant_joined` webhook records that and removes them.
    If webhooks are down, neither removal can know which rooms they are in.
41. **Recording follows who may be in the call.**
    Recording or stopping needs the rank, as before, and also being allowed to join the call, unless an org admin or owner; anybody else gets the same 404 as `GET /calls/:id/log`.
    The response is what that person may read of the call.
    Each recording has its own file, so recording a call that reopened does not overwrite the first.
42. **Toasts sit above the controls.**
    They sit above the bottom navigation, and above the call's controls while a call fills the screen, and the toast area itself catches no clicks.
    The knock prompt sits below a full-screen call's title bar, so leaving stays in reach.
43. **A browser that blocks a call's sound is told about.**
    The call shows **Turn on sound** whenever LiveKit says audio cannot play, in the full view and in the corner window.
44. **A new version never reloads a page by itself.**
    `autoUpdate` reloaded every tab when a new service worker activated, which dropped calls, as a browser test showed.
    The worker now waits, the page offers **Reload**, and not while that tab is in a call; accepting moves only that tab.
    A tab left open checks once an hour.
    The cost is that somebody who ignores the offer keeps running the old web app against the new server.
45. **A meeting is a start and a length from a list.**
    The user asked for 5, 10, 15 and 30 minutes, then added 45 minutes and 1 hour, so the list is those six, default 30.
    A slot picked on the calendar keeps its length when it is on the list, otherwise 30.
    Editing a meeting of another length, such as 90 minutes made through the API, offers its current length as well, so saving does not shorten it.
    The API still accepts any length up to 24 hours; the list is the page's.
46. **A meeting that runs past its end shows as extended, for whoever was in it during the overrun.**
    Once its scheduled end has passed, somebody who was there sees their real time: from when they joined to now while they are still in it, growing, or to when they left.
    The calendar item then carries `scheduledEndsAt`, the planned end, and `running` while it is still going, and the page draws the part past the planned end striped and labelled.
    Somebody who left by the planned end, or never came, keeps their time as it was, because they were not busy during the overrun.
    A meeting still going past its end is busy in availability and conflicts for whoever is in it.
    A call nobody closed cannot grow for long: the 5-minute sweep ends a call whose room has gone (decision 24 and ticket 23).
47. **An administrator sets up the mail server under Organization, Email, at any time.**
    It used to be settable only during first-time setup.
    Only an org admin or owner may read, save or test it.
    The password is never sent back to the page, and a blank password on save keeps the saved one; clearing the username clears it.
    Port 465 is TLS from the start, other ports upgrade with STARTTLS, and a password is never sent to a server across the network without encryption.
    **Send test email** sends to the administrator asking, and a server refusing is reported as the answer (`sent: false` with the server's words), not as a failure of the request.
48. **New members are emailed their sign-in details when they are added, singly or by CSV.**
    The send happens before the request answers, so the dialog never claims an email went that did not; that can take as long as the mail server does.
    If it was not sent, the dialog says why, either no mail server or the server's own words, and the password is still shown to pass on.
    A CSV import stops trying after the first failure, so a broken server costs one wait and not fifty.
    Resetting a password still shows the new one on screen only and does not email it: not asked for.
49. **A public status page at `/status` says whether anything is down now and what was down in the last 24 hours.**
    It needs no sign-in, because the time you most want it is when signing in does not work, and it is linked from the sign-in page and the user menu.
    It reports the services TeamUp depends on under names a member uses: TeamUp, Messages and data, Live updates, File sharing, Calls.
    Every replica records the readiness checks twice a minute into one Redis bucket per minute, kept 26 hours, and a failure any replica saw wins.
    A minute nobody recorded is shown as no record, not as working.
    No error text, host or port is in the report, because it is public.
    The database down is an outage of everything; anything else down is "some things are not working", and a banner says what inside the app.
    Without Redis the page still shows the current state and says the history cannot be read.
50. **An organization has its own colour and logo, and none means TeamUp's own look.**
    An admin sets them under Organization, Appearance: eleven presets or any hex colour, and a PNG, JPEG, WebP or GIF up to 512 KB; SVG is refused because it can carry script.
    They are readable without a session, so the sign-in page wears them, and the logo is served with a versioned address, cached for good, sandboxed.
    The colour replaces `--primary`, `--ring` and `--primary-foreground` in light and dark, with whichever text colour reads better on it, and the browser remembers it so the next load paints in it before anything is fetched.
    The organization's name replaces "TeamUp" beside the mark only once there is a logo.
51. **A PDF in chat opens to be read in the chat, and every file can be saved from where it is.**
    PDFs are drawn with pdf.js, loaded only when one is opened, page by page as they scroll into view, with zoom; a phone's browser cannot show a PDF in a frame.
    A PDF chip and a picture each have their own Download button, the viewers have one, and the message menu has Download for each file on the message.
52. **The Chat tab shows how many conversations have something unread, not how many messages.**
    It is the same list as the chat page's, kept current from wherever you are, in the sidebar and along the bottom on a phone.
    A message in a conversation the list has never seen, such as a new direct message, reads the list again so it is counted.
53. **Any message somebody wrote can be forwarded, with its files, to up to ten conversations, and arrives marked Forwarded.**
    The person forwarding must be able to read the original and post in every target; every target is checked before anything is sent, and not being able to read it is a 404.
    It arrives as a new message from whoever forwarded it, with `forwarded: true`; the original author is not named, as in WhatsApp.
    A file is copied inside the object store into each target conversation, because a file's path decides who may open it; the original stays readable only where it was.
    Deleted messages and events, such as a missed call, cannot be forwarded.
54. **The calendar has month, week and day views, chosen with a switch and kept in the address.**
    Without a choice it is week on a computer and day on a phone, as before.
    The month shows whole weeks, Monday first, up to three things a day and "+N more", without open time; a phone shows a dot for each thing.
    Clicking a day, or "+N more", opens it in the day view.
55. **Everybody can set their own profile picture under Settings, Profile.**
    The browser cuts it to the middle square at 256 pixels as JPEG, which also drops the photo's location and camera details.
    It is stored in the database (`user_avatars`) and served at `/api/users/:id/avatar?v=<hash>` to anybody in the organization, cached for good under that address; a new picture is a new address.
    Only the person or an administrator changes it; PNG, JPEG or WebP up to 512 KB, never SVG.
    It shows in chat, the header, the sidebar and the organization's member lists, with initials otherwise.
56. **The chat's call button is Call: a voice call with everybody's microphone on and cameras off.**
    It creates an `audio` call; the join token says so, and the page joins with the microphone on, for the caller and for whoever answers.
    Anybody can turn their camera on in the call. Meetings and calls started from Calls still join with both off.
57. **End-to-end encryption is not built yet; see ticket 47 for what it would take.**

## Non-goals

- Guests with no account.
- Accept or decline, recurring personal events, Google or Outlook sync, drag-to-resize, CSV export.
- Recording changes, or transcripts of scrums.

## API (all under the authenticated group, except the webhook)

- `POST /api/livekit/webhook` - verified with the LiveKit signature, not a session.
- `POST /calls/:id/knock`, `GET /calls/:id/requests`, `POST /calls/:id/requests/:rid/admit`, `POST /calls/:id/requests/:rid/deny`.
- `GET /calls/:id/log` - the audit trail and per-person sessions.
- `GET /calendar?userId=&from=&to=` - one person's calendar, at the level the caller may see.
- `GET /calendar/availability?userIds=&from=&to=` - free, busy, focus and open for scheduling.
- `POST /calendar/blocks`, `PATCH /calendar/blocks/:id`, `DELETE /calendar/blocks/:id` - own focus blocks and open slots.
- `POST /meetings`, `PATCH /meetings/:id`, `DELETE /meetings/:id` - scheduling, returning any conflicts.
- `GET`, `POST /standing-meetings`, `PATCH`, `DELETE /standing-meetings/:id`, `POST /standing-meetings/:id/join`.
- `GET /standing-meetings/:id/attendance?date=&from=&to=&teamId=`.

## Frontend

- `/calendar`, in the sidebar and bottom navigation: a week view on a computer and a day view on a phone, with a person picker for anybody, and titles only where allowed.
- Calls: copy link, knock screen, admit and deny prompt, join sound and toast, new grid, several shares, a call log panel.
- Standing meetings: pinned at the top of Calls with a Join button, managed by admins, with an **Attendance** view by day.
- Tasks: **Added to** on Overview, By person, Scrum and Contribution.

## Tickets

See `issues/`.
Status lives in each ticket, and the running log is `progress.md`.
