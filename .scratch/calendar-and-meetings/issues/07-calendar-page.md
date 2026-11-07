# Calendar page

Status: resolved
Blocked by: 04

Week and day view, person picker, schedule dialog with availability and conflict warnings, focus blocks and open slots, joining from an event.
Loading, empty and error states, and a phone layout.

## Comments

### 2026-11-07 - Senior frontend developer: built and seen working

Files: `web/src/features/calendar/{layout.ts,layout.test.ts,CalendarPage,CalendarGrid,EventDialog,BlockDialog,ScheduleDialog}`. No new dependency: a CSS grid plus `date-fns`. `layout.test.ts` runs forked in `America/New_York` because of the clock change on 2026-11-01.

**Choices**
- Open slots are drawn behind events, with a strip left showing, so a meeting inside one reads as the point of it.
- More overlap than fits (2 columns in a week, 3 in a day) goes into a `+N` chip that opens a list. A 138px column with three lanes was not readable.
- "You can see when X is taken, but not what for" shows only when the server sent no `source`, never by working out roles.
- Clicking an empty time blocks it on your own calendar and schedules a meeting on somebody else's, with them already added.
- State is in the address (`?date=&person=`), which is how a meeting notification lands on its day.

**Seen in a real browser against the real server**: week view and day view at 375, light and dark, no horizontal scroll; current time line; person picker; another person's calendar with no titles for somebody without authority (and `Secret project` absent from the page) and titles for their manager; block create, edit, delete; open slot behind events; five overlapping meetings; schedule dialog with availability bars and a conflict warning, going ahead; edit with no conflict against itself; cancel, and the invitee's calendar updating with no reload; join from an event; a past call at its real times; standing meeting on the calendar; 5-minute reminder arriving for organizer and invitee from the real sweep; a notification linking to the calendar on that day.

**Only with a mocked failure**: the error state (a 500 from `page.route`). The loading state was slowed with `page.route`. Both then recovered against the real server.

**Not seen**
- A real clock change: covered by tests only.
- A browser in a zone other than Asia/Calcutta.

**Awkward**
- Conflict hiding when editing a meeting is by matching its exact old start and end, since availability has no ids. Somebody with a different meeting at exactly those times is not warned before saving. The server's answer after saving is right, and the toast says so.
- The availability read returns the whole day, which is more than a conflict needs, in order to draw the bars.
