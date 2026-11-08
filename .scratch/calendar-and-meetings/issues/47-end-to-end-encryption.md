# End-to-end encryption

Status: needs-info
Severity: Medium
Asked for by: the user, "see if we can make the chat end to end encrypted".

## What it would take

Every device holds its own keys, the server only stores ciphertext, and each conversation's key is shared with its members' devices, for example with MLS or the Signal sender-key scheme.
That needs key backup with a passphrase, or a new device cannot read history; adding somebody or removing somebody changes the key.

## What it costs

Server-side message search, notification text, the welcome and password-reset flow reading history, link previews and moderation by administrators all stop working on encrypted content.
A web app's code comes from the same server it protects against, so the guarantee is weaker than in an installed app unless the code is pinned or audited.

## Smaller steps

Calls: LiveKit supports encrypted media; with a key shared through the app it protects against the media server, not against the app server.
At rest: encrypting the database disk and object storage on the VM protects a stolen disk and changes nothing for users.
Waiting on the user to choose.
