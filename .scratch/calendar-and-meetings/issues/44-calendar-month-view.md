# Month, week and day views on the calendar

Status: ready-for-human
Severity: Low
Asked for by: the user, "in calendar, have monthly view, weekly and daily view".

## Comments

2026-11-08, orchestrator: decision 54.
`web/src/features/calendar/MonthGrid.tsx`, `layout.ts` (`viewFrom`, month days, `itemsOnDay`), and the switch in `CalendarPage.tsx`.
Found by the end-to-end test and fixed before it shipped: the list of a day's items covered the day, so clicking the space between them did nothing.
Verified: unit tests in `layout.test.ts`, and the end-to-end test (switch, address, next and previous month, details, opening a day, reload, no sideways scroll on a phone).
