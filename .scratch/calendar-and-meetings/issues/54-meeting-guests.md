# Guests from outside the organization in a meeting

Status: done

## What

The user asked: "if we schedule a meeting, we should be able to share someone to join from outside the org, as guest. They can join from the URL that is provided, and if password so that it's secure, and ask name before joining."

## Built

- `internal/calls/guests.go`, `guest_routes.go`, migration `0027_meeting_guests` (links, guests, wrong-passcode attempts).
- Members: `GET` and `PUT /api/calls/:id/guest-link`, `POST /api/calls/:id/guests/:guestId/remove`.
- Guests, with no account: `GET /api/guest/:token`, `POST /api/guest/:token/join`.
- Web: `GuestAccess.tsx` in the calendar's meeting details and behind **Guests** in a call; `GuestJoinPage.tsx` at `/join/:token`; guest tiles say "(guest)" and the organizer has **Remove**.

## Decisions

- Only the organizer or an administrator turns the link on or off; everybody who may join the meeting sees the link and passcode, to pass on.
- The passcode is kept as typed, like the link, because members are shown it.
- A guest comes in only while the meeting is on; before that the page waits and lets them in when it starts.
- Guests have identities `guest-<id>`; they are not on the attendance trail, and a removed guest is put out again by the webhook if they return on the token they had.
- Wrong passcodes: 5 per address and 50 per link in 10 minutes. The address is nginx's `X-Real-IP`, believed only from a private or loopback peer.
- The note-taker hears guests and lists them as "Name (guest)" in the minutes; only members are sent the minutes.

## Verified

Go: `internal/calls/guests_test.go` (who may manage and see the link, across two organizations; waiting, wrong passcode, the token's grants; removal and return; turning off; limits by address and for the link; the address header), `TestMeetingGuestsMigrationIsReversible`, and the note-taker naming a guest.
Browser: `web/e2e/meetings.y-guests.spec.ts`, from the organizer making the link in the calendar to the guest waiting, joining, being shown as a guest, and being removed; looked at on a phone and a computer.

## Not verified

A guest on a real phone on another network, through the live server's TURN.
