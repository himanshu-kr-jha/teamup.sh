/**
 * Moving between conversations from the keyboard, in the order the list shows
 * them: channels first, then direct messages.
 */

/** The conversation above (-1) or below (1) the open one, going round at the ends. */
export function stepConversation(order: { id: string }[], current: string | null | undefined, step: 1 | -1): string | null {
  if (order.length === 0) return null;
  const at = order.findIndex((c) => c.id === current);
  // Nothing open: down starts at the top, up at the bottom.
  if (at === -1) return order[step === 1 ? 0 : order.length - 1].id;
  return order[(at + step + order.length) % order.length].id;
}

/** The next conversation with something unread after the open one, or null when everything is read. */
export function nextUnread(order: { id: string; unreadCount?: number }[], current: string | null | undefined): string | null {
  const at = order.findIndex((c) => c.id === current);
  for (let i = 1; i <= order.length; i++) {
    const c = order[(at + i + order.length) % order.length];
    if (c.id !== current && (c.unreadCount ?? 0) > 0) return c.id;
  }
  return null;
}
