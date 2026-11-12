import { chatText, parseBlocks, toHtml } from './format';

/**
 * Formatted text sent out of TeamUp.
 *
 * Copying puts two versions on the clipboard and the app it is pasted into
 * takes the one it understands: a document, an email or a page takes the
 * HTML, with real bold, lists and links; WhatsApp, Slack, Telegram and the
 * like take the plain text, whose *marks* they read as formatting.
 */
export async function copyFormatted(text: string): Promise<void> {
  const blocks = parseBlocks(text);
  const plain = chatText(blocks);
  const html = toHtml(blocks);
  if (typeof ClipboardItem !== 'undefined' && navigator.clipboard?.write) {
    try {
      await navigator.clipboard.write([
        new ClipboardItem({
          'text/plain': new Blob([plain], { type: 'text/plain' }),
          'text/html': new Blob([html], { type: 'text/html' }),
        }),
      ]);
      return;
    } catch {
      // Some browsers take only plain text; that still keeps the marks.
    }
  }
  await navigator.clipboard.writeText(plain);
}

/** Whether this device offers its own share sheet: phones and some desktops do. */
export function canShare(): boolean {
  return typeof navigator !== 'undefined' && typeof navigator.share === 'function';
}

/**
 * Hands the text to the device's share sheet, to send with any app on it.
 * False if it was not shared, including when the person closed the sheet.
 */
export async function shareText(text: string, title?: string): Promise<boolean> {
  if (!canShare()) return false;
  try {
    await navigator.share({ title, text: chatText(parseBlocks(text)) });
    return true;
  } catch {
    return false;
  }
}
