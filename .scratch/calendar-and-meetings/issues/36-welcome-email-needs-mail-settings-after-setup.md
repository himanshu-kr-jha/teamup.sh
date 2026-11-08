# A new member is not emailed their sign-in details

Status: ready-for-human
Severity: Medium
Asked for by: the user, "when we add member and provide mail, it will send a welcome mail and its login details to that mail".

## What happened

The welcome email existed, but only went out when a mail server was configured, and that could only be done in first-time setup.
The live site was set up without one, so nobody could ever be emailed.

## Comments

2026-11-08, orchestrator: decisions 47 and 48.
Organization has an **Email** tab for an admin or owner: server, port, username, password (never sent back), send as, and **Send test email**.
Sending now supports port 465, a From with a name, and refuses to send a password unencrypted to a remote server.
Adding one member or a CSV emails each new member; the dialog says why when it did not, and still shows the password.
The welcome text mentions meetings, the calendar and the scrum, and has no em dash.
Verified: `internal/mail/smtp_test.go` against an in-process SMTP server (STARTTLS, 465, plain refusal, wrong password, server down, settings checks); `internal/org/mail_settings_test.go` for the endpoints, who may use them, the password never returned, and add and import; `web/e2e/meetings.y-mail.spec.ts` with a real Mailpit server, where the emailed password signs the new member in and sends them to change it.
Found and fixed while testing: changing only the password left the form unsaved, because the saved settings came back identical. Shown red without the fix.
Not verified: a real provider such as Gmail or SES, and delivery to a real inbox or spam folder.
