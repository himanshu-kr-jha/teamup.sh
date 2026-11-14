# A self-hosted workspace suite: a proposal

Status: future plan, for the owner to decide on.
Date: 14 November 2026.
Scope: direction only; no code was changed.

## The goal in one paragraph

TeamUp should be able to stand in for Google Workspace for an organization that wants to run everything itself.
Each person gets their own storage for files, the organization gets shared storage for its teams, and documents, spreadsheets and presentations can be opened and edited together in the browser.
The organization hosts all of it on its own server and keeps its files in its own bucket, and pays for the server and the storage rather than a fee per person.

## What TeamUp already covers

| Google Workspace | TeamUp today |
| --- | --- |
| Chat | Channels, direct messages, threads, reactions, files in conversations |
| Meet | Video calls, screen sharing, recording, standing meetings, knocking, AI meeting notes |
| Calendar | Calendar, scheduling over busy time, focus time, Google Calendar sync |
| Admin console | Organization, roles and permissions, teams, branding, service status |
| Tasks | Tasks, boards, the scrum dashboard, projects, GitHub |
| Mobile apps | The Android app (iOS still to do) |

## What is missing, and how to get each

### 1. Drive: files that belong to people and teams (build)

Today a file exists only as part of a chat message or a task comment.
Drive gives files a home of their own:

- "My files" for each person, with folders.
- Shared folders for a team or the whole organization.
- Sharing a file or folder with people or teams, as viewer or editor, and a link that only signed-in members can open.
- Previews in the browser for pictures, PDFs, audio and video, which the chat already does.
- A file from Drive can be sent into a conversation or attached to a task, and the reverse.
- Trash, with recovery for a number of days.

Storage quotas:

- A quota for each person, for example 5 GB, and one for the organization, both set by an administrator.
- Usage shown to each person in Settings and to the administrator per person and per team.
- The quota is counted from `size_bytes`, which `internal/files` already records from what storage reports rather than what the browser claims.
- Today there is only a largest-file limit (`MaxUploadBytes`); nothing counts a person's total yet.

Why build rather than integrate: Drive is mostly permissions, and TeamUp already has the people, teams, roles and the permission checks.
A separate product such as Nextcloud would bring a second set of accounts and a second idea of who may see what.

Storage costs little: with files in an outside bucket (PR #103) a Lightsail bucket holds 100 GB for 3 dollars a month, and grows without touching the server.

### 2. Docs, Sheets and Slides (integrate)

Do not write an editor.
Plug a self-hosted office suite into Drive instead:

- **ONLYOFFICE Docs** or **Collabora Online** open Word, Excel and PowerPoint files in the browser with live co-editing, and save back to the file.
- Both speak WOPI, a standard where the host (TeamUp) hands the editor a file and a short-lived token, and checks who may read or write it.
- Files stay ordinary `.docx`, `.xlsx` and `.pptx` in the organization's bucket, so nothing is locked in.
- The editor runs as one more container, and needs about 2 to 4 GB of memory of its own, so the server grows from the 4 GB plan to the 8 GB one (about 44 dollars a month on Lightsail instead of 24).
- Check each suite's licence for the number of simultaneous editors before choosing.

### 3. One sign-in for everything (build, and it is overdue)

The office editor, and mail if it comes, should use TeamUp accounts so people sign in once.
TeamUp would act as the identity provider for them, and also accept sign-in from Google or Microsoft for organizations that already have it.
OIDC sign-in is a stub today (README, Known gaps, #15), and the mobile app needs it too, so this is worth doing for its own sake.

### 4. TeamUp Mail: our own mail server (a feature, last)

Decision (owner, 14 November 2026): TeamUp gets its own mail server as a feature, so an organization running TeamUp hosts its people's mail itself.
Connecting an outside provider (Google, Zoho, Microsoft) stays possible for organizations that already have one, but it is not the plan.

How to build it:

- TeamUp owns everything a person and an administrator see: mailboxes made with the account, the mail screens in the web and phone apps, aliases and shared addresses such as `support@`, quotas, the DNS checks, and the admin settings.
- The mail engine underneath (SMTP to send and receive, IMAP and JMAP for mail apps, spam filtering, DKIM signing, queues and retries) is a hard, security-sensitive area where mistakes lose mail or get it marked as spam.
  Start from a proven open-source engine run as one more container that TeamUp drives over its API, with **Stalwart** the first candidate: one program, written in Rust, with SMTP, IMAP, JMAP, spam filtering and an admin API, and storage that can go in Postgres and the same bucket as Drive.
  Writing parts of the engine ourselves is a later choice, made only where the bundled one gets in the way.
- Mail in TeamUp: turn a mail into a task, share a mail into a conversation, and file attachments into Drive.

Running a mail server is the part of a workspace most likely to go wrong: messages land in spam unless the server's address has a good reputation and DNS has SPF, DKIM and DMARC, and AWS blocks sending on port 25 until asked.
The setup has to say so plainly, check the records, and offer a relay (below) from the start.

`internal/mail` today only sends TeamUp's own messages, such as invitations; it is not a mailbox.

What it gives an organization that runs TeamUp, once built:

- Mail on the organization's own domain: adding a person makes their account and their mailbox, such as `ravi@acme.com`, in one step.
- One sign-in for mail, in TeamUp or in any mail app on a phone.
- The mailbox counts against the person's storage quota, beside their Drive.
- The setup script lists the DNS records (MX, SPF, DKIM, DMARC) and checks them, as it already checks the domain for the site.

What the organization has to bring: its own domain, and a server allowed to send mail.
Where port 25 is blocked, as on AWS and Lightsail until asked, or the address has no reputation yet, mail goes out through a relay such as Amazon SES or Postmark.
A new server's mail can land in spam for its first weeks, and the setup should say so.

Mail should be something an administrator turns on, not a default, since an organization with mail elsewhere may not want to move it.

### 5. Later

- Forms, with answers into a sheet.
- A wiki or knowledge base for each team.
- One search across chat, Drive and documents, respecting who may see what (chat search already does this for messages).
- Retention and export for compliance.

## Suggested order

1. Drive with quotas.
2. Office editing through ONLYOFFICE or Collabora, opened from Drive.
3. Single sign-on, with TeamUp as the identity provider for the editor.
4. TeamUp Mail, our own mail server.

Each stage is useful on its own, so the order can stop at any point.

## What a self-hosting organization would pay

For 50 people, at Lightsail prices in Mumbai:

| Setup | Server | Files | About a month |
| --- | --- | --- | --- |
| TeamUp with Drive | 4 GB plan, 24 dollars | 100 GB bucket, 3 dollars | 27 dollars |
| Plus office editing | 8 GB plan, 44 dollars | 100 GB bucket, 3 dollars | 47 dollars |

Google Workspace for the same 50 people costs about 7 to 14 dollars each, 350 to 700 dollars a month.

## Questions to settle before stage 1

- Default quota for a person and for the organization, and what happens at the limit: refuse new uploads, or warn first.
- Does a file sent in chat count against the sender's quota, and does it appear in their Drive?
- What happens to a person's files when they leave: moved to their manager, to an administrator, or kept for a number of days and then removed.
- ONLYOFFICE or Collabora, after trying both with real documents.
