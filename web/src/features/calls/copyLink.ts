/**
 * Puts a call's link on the clipboard. Over plain HTTP, on a local network say, the
 * clipboard API does not exist, so the old way is the fallback rather than a button
 * that does nothing.
 */
export async function copyCallLink(callId: string): Promise<boolean> {
  return copyText(`${window.location.origin}/calls/${callId}`);
}

/** Puts any text on the clipboard, the same way. */
export async function copyText(url: string): Promise<boolean> {
  try {
    if (navigator.clipboard) {
      await navigator.clipboard.writeText(url);
      return true;
    }
  } catch {
    // Refused; try the old way.
  }
  try {
    const box = document.createElement('textarea');
    box.value = url;
    box.setAttribute('readonly', '');
    box.style.position = 'fixed';
    box.style.opacity = '0';
    document.body.appendChild(box);
    box.select();
    const copied = document.execCommand('copy');
    box.remove();
    return copied;
  } catch {
    return false;
  }
}
