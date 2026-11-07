# The join toast covers the knock prompt and the call controls on a phone

Status: ready-for-human
Found by: ticket 09 (`web/e2e/meetings.spec.ts`, test "defect 30"). The test is red until this is fixed.

## Steps

1. Sign in as Olivia Owner at 375 by 812 (a phone) and join a call with one other person already invited.
   Wait two seconds.
2. Somebody else (Pat) joins the call, so the join toast "Pat Platform joined the call" shows.
3. Within the toast's few seconds on screen, somebody who is not in the call (Sam) opens the link and presses **Ask to join**.

## What happens

The toast is drawn at the top of the screen over the call's title bar and over the knock prompt.
At the centre of the **Admit** button, the topmost element is the toast, not the button (`document.elementFromPoint` returns the toast), so Olivia cannot press Admit or Deny until the toast goes.
The same toast covers part of the title bar: the copy link, call log, minimise and leave buttons sit under it, and the red leave button is partly hidden.
At 1280 wide the toast is bottom right and overlaps the right edge of the share-screen button by about 17px.

Seen in `admin__call-knock-prompt__375__light.png` and `admin__call-screen-share__375__dark.png`
(in `/tmp/claude-1000/-home-himanshu-kumar-jha-Documents-Ongoing-Projects-teamUp/47fd8d20-9a9e-48c5-8ba0-78ed94fb07ed/scratchpad/e2e-shots-meetings/`),
and at 1280 in `admin__call-knock-prompt__1280__light.png`.

## What the spec says

Request 3: somebody joining makes a sound and a notification.
Request 5 and decision 8: a knock is a prompt any member of the call can answer.
A notification about one arrival should not stop a member from answering the knock or leaving the call, which are the controls this page exists for.

## Notes

The toast lasts only a few seconds, so this is a transient fault, but it is exactly when people arrive and knock that it bites, and on a phone the prompt has no other place to be.
Toast and prompt both live at the top on a phone; moving one of them (toast above the control bar, or prompt below the title bar) fixes it.

## Comments

2026-11-07, senior developer:
Toasts now sit bottom right above `--toast-bottom`: the bottom navigation normally, and the measured height of the call's controls while a call fills the screen. The toast area catches no clicks itself.
The knock prompt sits below a full-screen call's title bar, since it covered Leave on a phone just as the toast did.
Verified: "defect 30" in `web/e2e/meetings.spec.ts` is green, and extended to the microphone and camera, plus a new one at 1280 for the share button.
