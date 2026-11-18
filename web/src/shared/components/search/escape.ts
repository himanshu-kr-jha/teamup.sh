/**
 * Escape in a search box clears what was typed before it closes anything.
 *
 * Radix closes a dialog or popover on Escape before the key reaches the box, so
 * the dialog asks first: a search box marked `data-escape-clears` that still
 * has text keeps the dialog open, and the box clears itself. The next Escape,
 * with the box empty, closes the dialog as usual.
 */
export function keepOpenWhileSearchHasText(event: KeyboardEvent) {
  const target = event.target;
  if (target instanceof HTMLInputElement && target.dataset.escapeClears !== undefined && target.value !== '') {
    event.preventDefault();
  }
}

/**
 * A dialog that opens on a search box focuses it on a computer, but not on a
 * phone, where the keyboard would jump up over the list the person came to see.
 * The dialog itself takes the focus instead, so Escape and Tab still work.
 */
export function skipSearchFocusOnPhone(event: Event) {
  if (typeof window === 'undefined' || window.matchMedia('(min-width: 768px)').matches) return;
  const content = event.currentTarget;
  if (!(content instanceof HTMLElement)) return;
  const first = content.querySelector('input:not([type="hidden"]), textarea, select, button, [href], [tabindex]:not([tabindex="-1"])');
  if (first instanceof HTMLInputElement && first.dataset.escapeClears !== undefined) {
    event.preventDefault();
    content.focus({ preventScroll: true });
  }
}
