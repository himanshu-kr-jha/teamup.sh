# Standing meeting and Added to screens

Status: resolved
Blocked by: 03, 05

Standing meetings on Calls, admin management, attendance by day with management scope.
**Added to** on Overview, By person, Scrum and Contribution.

## Comments

### 2026-11-07 - Senior frontend developer: built and seen working

Files: `web/src/features/calls/standing/{standing.ts,standing.test.ts,StandingMeetings,StandingMeetingDialog,AttendancePage}`, route `/calls/standing/:id/attendance`, `tasks/dashboard/{logic,parts,OverviewTab,ContributionTab,ScrumTab}`.

**Seen in a real browser against the real server**: admin create, edit and archive through the dialogs; a member sees the rooms and no admin buttons; Join from the card as a member, with webhooks marking both present; attendance as admin, with team filter, as a manager and as a member at 375 and in dark; `unknown` with its explanation (join pressed with no connection, so webhooks never came); the log on that day saying no data rather than nobody; a range over 92 days refused; empty dates. Added to on Overview (employee and admin), By person, Scrum, Contribution with the person's detail, and saved scrums before and after: the old one has no section and no error. 375 and dark with no horizontal scroll.

**Scope**: nothing is hidden by role except the admin create, edit and archive buttons, and the server refuses those too. Attendance draws whatever the server returns.

**Only with a mocked failure**: the error states of standing meetings, calls and attendance.

**Not seen**
- A day other than today with real data. Late (74 minutes, in red) and absent (the five who did not come) were looked at on today's row, from real webhooks.
- The team filter's options come from `GET /tasks/dashboard`, the same as everywhere else, which makes a page about attendance read the whole dashboard.

**Awkward**: a reload at `/calls/<occurrence id>` joins by token and works, as `MayJoin` allows a standing meeting.
