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

### 4. Mail (integrate, last)

Running a mail server is the hardest part of a workspace and the one most likely to go wrong: messages land in spam unless the server's address has a good reputation and DNS has SPF, DKIM and DMARC, and AWS blocks sending on port 25 until asked.
Two ways:

- Plug in a self-hosted mail server such as **Stalwart** or **Mailcow**, with TeamUp accounts as its users through single sign-on.
- Or keep mail with a provider (Google, Zoho, Microsoft) and connect it, so TeamUp shows mail notifications and turns a mail into a task.

`internal/mail` today only sends TeamUp's own messages, such as invitations; it is not a mailbox.

### 5. Later

- Forms, with answers into a sheet.
- A wiki or knowledge base for each team.
- One search across chat, Drive and documents, respecting who may see what (chat search already does this for messages).
- Retention and export for compliance.

## Suggested order

1. Drive with quotas.
2. Office editing through ONLYOFFICE or Collabora, opened from Drive.
3. Single sign-on, with TeamUp as the identity provider for the editor.
4. Mail, as an integration.

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
