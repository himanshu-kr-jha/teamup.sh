# PDFs open in the chat, and files download from the menu

Status: ready-for-human
Severity: Low
Asked for by: the user, "pdf in web pdf opener inside the chat, and download option similar for image, and add download option in drop down for files".

## Comments

2026-11-08, orchestrator: decision 51.
`web/src/features/files/PdfViewer.tsx`, `download.ts`, `AttachmentChip.tsx`, and the message menu in `ChatPage.tsx`.
Also fixed: a small picture's thumbnail was smaller than its own button, and file sizes and icons on a chip were unreadable inside your own message's coloured bubble; the chat's Send buttons had no accessible name.
Verified end to end: a real two-page PDF is drawn (ink on the canvas), zooms, downloads under its own name from the viewer and the chip; a picture downloads from its button and from the message menu.
Not verified: on a real phone, or with a very large PDF.
