/**
 * Whether opening a conversation should put the cursor in the message box.
 *
 * With a keyboard, the next thing anybody does in a conversation they opened is
 * type, so the box is ready for it. On a phone the same focus raises the
 * on-screen keyboard over half the conversation, every time one is opened only
 * to be read, so there the box waits to be tapped. (It was tried on phones,
 * and people found the keyboard jumping up unwelcome.)
 */
export function typesOnOpen(media: Pick<Window, 'matchMedia'> | undefined = typeof window === 'undefined' ? undefined : window): boolean {
  return !!media?.matchMedia?.('(pointer: fine)').matches;
}

/** A dialog or a menu is open, and will hand focus back to whatever opened it as it closes. */
export function overlayOpen(doc: Document): boolean {
  return !!doc.querySelector('[role="dialog"], [role="alertdialog"], [role="menu"], [role="listbox"]');
}

/** They are typing somewhere else already, so taking the cursor would lose their place. */
export function typingElsewhere(doc: Document, box: HTMLElement): boolean {
  const active = doc.activeElement as HTMLElement | null;
  if (!active || active === doc.body || active === box) return false;
  return active.matches('input:not([type="checkbox"]):not([type="radio"]):not([type="file"]), textarea, [contenteditable="true"]');
}

/**
 * Puts the cursor in `box` once nothing else is in the way. A dialog or a menu
 * that is closing hands focus back to whatever opened it, sometimes just after
 * the overlay has gone, so this waits for it to go and then holds the cursor
 * there a moment, taking it back from a button that grabs it. Never from a
 * box somebody is typing in. Time-based, as frames slow down under load.
 * Returns the way to stop.
 */
export function focusWhenFree(box: () => HTMLInputElement | HTMLTextAreaElement | null): () => void {
  const WAIT_MS = 3000;
  const HOLD_MS = 400;
  const started = performance.now();
  let placedAt = 0;
  let frame = 0;
  const attempt = () => {
    const now = performance.now();
    const el = box();
    if (placedAt === 0) {
      if (el && !el.disabled && !overlayOpen(document)) {
        if (typingElsewhere(document, el)) return;
        el.focus({ preventScroll: true });
        placedAt = now;
      } else if (now - started > WAIT_MS) {
        return;
      }
    } else {
      if (now - placedAt > HOLD_MS || !el) return;
      if (document.activeElement !== el && !typingElsewhere(document, el) && !overlayOpen(document)) {
        el.focus({ preventScroll: true });
      }
    }
    frame = requestAnimationFrame(attempt);
  };
  frame = requestAnimationFrame(attempt);
  return () => cancelAnimationFrame(frame);
}
