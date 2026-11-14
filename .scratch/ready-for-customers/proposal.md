# Ready for any organization: what is still not built

Status: future plan, for the owner to decide on.
Date: 14 November 2026.
Scope: a list of the work left; no code was changed.

## The goal in one paragraph

Any startup or organization should be able to take TeamUp, run one command on a server of its own, and have its people working in it the same day, without the TeamUp team stepping in.
After that it should keep running, update itself safely, and come back from a lost server.
Today TeamUp is ready for a pilot with a small organization that has somebody technical on hand: about 500 people at work and meetings of 50 to 60 on one 2-CPU server, set up by `scripts/new-server.sh`, with files and nightly database backups in the organization's own S3 bucket.
This is what stands between that and "any organization".

The bigger direction, a Drive, office documents and TeamUp's own mail, is in [the workspace suite proposal](../workspace-suite/proposal.md); this list is what comes before it.

## 1. Setting up a new organization

### 1.1 Email in setup (first)

TeamUp can send email (invitations, guest invitations to calls, the meeting notes), but setup never asks for an email service, so the Mumbai server sends none.
`new-server.sh` should ask for one (Amazon SES, Postmark, or the organization's own SMTP), send a test message to the owner, and refuse to finish until it arrives or the owner says to go on without.
Port 25 is closed on AWS, so this is a relay on port 587, the same choice as in the mail plan.

### 1.2 Setup that has been run more than once

The first full run of `new-server.sh` on a fresh server with an S3 bucket was on 14 November 2026, and it stopped four times: a script that was not executable (#106), a laptop still holding the old DNS answer (#107), a bucket address the last step refused (#108), and a key not allowed to set CORS.
The first three are fixed.
Run it again on a fresh test server, from the docs alone, before promising it to a customer.
It should also check everything the bucket key will need, not just reading and writing: deleting (or deleted files stay in the bucket), listing versions (or `scripts/db-backups.sh` cannot work) and setting CORS.
On 14 November the Mumbai server ended up with a key that can do none of the three, and nothing said so until later.

### 1.3 Google Calendar as a setup step

Today it is a separate script (`scripts/setup-google-calendar.sh`) that the owner has to know about; the Calendar page simply shows no "Connect Google Calendar" until it has run.
Setup should offer it, and the Calendar page should tell an admin what is missing rather than hide the button.

### 1.4 The owner's first hour

A short checklist on first sign-in: invite people, set the branding, connect email and calendar, check that a call works from outside the office network.

## 2. Signing in

### 2.1 Google and Microsoft sign-in (SSO)

Only email and password work today; OIDC is a stub (#15).
Most companies past about twenty people expect "Sign in with Google" or Microsoft, and it is also how people would be added and removed in one place.
TeamUp as the sign-in for everything else in the workspace suite builds on this.

### 2.2 Signing in on the phone app

Google and Microsoft both refuse to sign in inside an app's web view, so the app needs the system browser with a hand-back.

## 3. Running it after day one

### 3.1 Knowing when it is down

There is no uptime check or alert.
Wanted: an outside check of the site every minute, an alert by email when it fails, and warnings for a disk over 80 per cent, a certificate close to expiry, or a backup that did not run.

### 3.2 Updates without the TeamUp team's laptop

`make deploy` runs from a developer's machine with the repository.
A customer needs released versions (tagged, with built images) and a way to update from the server itself, or from the admin page, with the same backup first and rollback after.

### 3.3 A restore that has been practised

The nightly backup goes to the bucket with 30 days of versions behind it, but nobody has yet restored one of those copies onto a fresh server.
Do it once, write down the steps, and add `scripts/db-backups.sh restore` so it is one command.
A server keeping files on its own disk (MinIO) still backs up only to that disk; it should copy the backup off the server too.

### 3.4 More than one server

One server has no failover: if it is down, everybody is.
For larger customers: the database on a managed service (RDS), the call server on its own machine (meetings beyond 60 people need this anyway), and two app servers behind a load balancer.

## 4. Performance left open by the load test

From the 13 November 2026 load test, still open:

- A rush of people signing in at once (password hashing is deliberately slow).
- The task list returns up to 1,000 tasks at a time; it should page.
- Search on very common words is slow.
- Meetings above about 60 people lose video on one server; the call server needs a machine of its own.

## 5. The phone app

The Android app loads the live server; still to do, in order:

1. Push notifications through Firebase (web push does not reach an app's web view).
2. Sign-in through the system browser (2.2).
3. Downloads of files made in the browser (`blob:` addresses).
4. iOS (needs a Mac and the US$99 a year developer account).

## 6. What a paying customer will ask for

- **A security review**, ideally an outside penetration test, with the findings fixed.
- **A privacy policy, terms of service and a data processing agreement.**
- **Data export**: everything an organization has, in a form it can take elsewhere.
- **Retention policies**: delete messages and files after a set time, per organization or channel.
- **Compliance export**: messages for legal hold.
- **An audit log** an admin can read: who signed in, changed a role, deleted something.
- **A support process**: where customers report problems, and how fast they hear back.

## 7. Not built, and not needed for the first customers

From the README's "Not built" list:

- Guest accounts in chat (calls already take guests by link).
- End-to-end encryption.
- Federation with other TeamUp servers.
- Other languages (i18n).

## Suggested order

| Stage | What | Why then |
| --- | --- | --- |
| 1 | Email in setup (1.1), setup run again with the full key check (1.2), a practised restore (3.3) | Without these a new customer can get stuck on day one or lose data |
| 2 | Uptime and disk alerts (3.1), Google Calendar in setup (1.3), the first-hour checklist (1.4) | Enough to hand over to pilot customers |
| 3 | Google and Microsoft sign-in (2.1), released versions and self-update (3.2) | What most startups ask for next |
| 4 | Phone push and phone sign-in (5, 2.2), task list paging and search (4) | A complete experience on phones, and room to grow |
| 5 | Security review and the paperwork (6) | Before selling to companies that will ask |
| 6 | More than one server (3.4), then the workspace suite | Larger customers |

## Questions for the owner

1. Who is the first customer, and how technical are they? That decides how far stage 3.2 has to go before them.
2. Which email service should setup suggest by default: Amazon SES (cheapest on AWS) or Postmark (simplest)?
3. Will TeamUp run servers for customers (hosted), or only hand them the package (self-hosted)? Hosted moves 3.1, 3.2 and 3.4 onto the TeamUp team.
4. Is there a budget for an outside security review, and when?
