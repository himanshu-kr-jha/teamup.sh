# 10 — File attachments end to end

**What to build:** Attaching a file to a message or a task keeps it: it shows up afterwards and can be downloaded again.

Uploading half-works today. Presigned upload and download URLs are issued and properly access-controlled, but **nothing ever records the attachment**, so once the browser forgets the object key the file is unreachable. History does not return attachments either, so they vanish on reload.

**Blocked by:** 01 — Dialog and form primitives.

**Status:** ready-for-agent

- [ ] Attaching a file to a message stores a record of it, and the attachment is still shown after a reload
- [ ] The same holds for a task attachment
- [ ] Attachments come back with message and task history, with filename, size and type
- [ ] An image attachment previews inline; anything else offers a download
- [ ] Downloading works for another member of the same channel or task
- [ ] Someone outside the organization cannot download it, even with the object key
- [ ] Upload progress is visible, and a failed upload is reported rather than leaving a silent gap
- [ ] An upload that fails part-way does not leave an attachment record pointing at a missing object
