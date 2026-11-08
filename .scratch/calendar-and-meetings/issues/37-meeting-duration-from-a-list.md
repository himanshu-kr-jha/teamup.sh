# Scheduling a meeting asks for a length from a list

Status: ready-for-human
Severity: Low
Asked for by: the user, "it should ask for start time and duration from drop down of 5,10,15,30 min", then "add" 45 minutes and 1 hour.

## Comments

2026-11-08, orchestrator: decision 45.
The **Ends** field is replaced by **Duration**: 5, 10, 15, 30, 45 min and 1 hour, default 30.
Editing a meeting of another length shows it, for example "1 h 30 min", and saving keeps it.
Verified: unit tests in `layout.test.ts`, and `web/e2e/meetings.y-calendar.spec.ts` which schedules 15 minutes through the dialog and edits a 90-minute meeting without shortening it. The two older specs that typed an end now pick a duration.
