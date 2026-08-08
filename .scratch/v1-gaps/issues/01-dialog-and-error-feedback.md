# 01 — Dialog and form primitives, with errors the user can see

**What to build:** A reusable modal-with-form building block, and one consistent way for a failed request to become something the user actually sees.

Two things force this first. There is no dialog component at all, even though the Radix dialog dependency is already installed, and four of the tickets below each need one to create something. And a failed request currently disappears: the Settings save writes the error to the browser console and leaves the button looking like it worked, so a broken action is indistinguishable from a successful one.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] A dialog primitive exists, closes on escape and on backdrop click, traps focus while open, and returns focus to whatever opened it
- [ ] A form inside a dialog can report per-field validation errors and a form-level failure without closing
- [ ] Submitting shows a pending state and cannot be double-submitted
- [ ] A failed API call surfaces a readable message to the user rather than only the console
- [ ] The message distinguishes "you did something wrong" from "something broke", following the status the API returns
- [ ] The existing Settings save is migrated onto it as the first caller, so the pattern is proven by a real screen
