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
 * that is closing hands focus back to whatever opened it, after anything done
 * on the same frame, so this waits for it to go first. Returns the way to stop.
 */
export function focusWhenFree(box: () => HTMLInputElement | HTMLTextAreaElement | null): () => void {
  let frame = 0;
  let tries = 0;
  const attempt = () => {
    const el = box();
    if (el && !el.disabled && !overlayOpen(document)) {
      if (!typingElsewhere(document, el)) el.focus({ preventScroll: true });
      return;
    }
    // About a second and a half; a dialog still up after that is one being used.
    if (++tries < 90) frame = requestAnimationFrame(attempt);
  };
  frame = requestAnimationFrame(attempt);
  return () => cancelAnimationFrame(frame);
}
