// Web addresses in a message, so they can be shown as links.
//
// Only http and https addresses, and ones starting www., become links: never
// javascript: or data:, whatever somebody types. A bare name such as notes.md
// stays text, because file names look just like domains.

export type LinkPart = { text: string; href?: string };

const URL_LIKE = /\b(?:https?:\/\/|www\.)[^\s<>"]+/gi;

// Punctuation that ends a sentence rather than the address.
const TRAILING = /[.,;:!?'"*_~]+$/;

/** Drops what follows an address in a sentence: a full stop, or a bracket the address did not open. */
function trimEnd(url: string): string {
  let u = url;
  for (;;) {
    const before = u;
    u = u.replace(TRAILING, '');
    for (const [open, close] of [
      ['(', ')'],
      ['[', ']'],
      ['{', '}'],
    ]) {
      if (u.endsWith(close) && count(u, close) > count(u, open)) u = u.slice(0, -1);
    }
    if (u === before) return u;
  }
}

function count(s: string, ch: string): number {
  return s.split(ch).length - 1;
}

/** A message cut into plain text and links. */
export function splitLinks(content: string): LinkPart[] {
  const parts: LinkPart[] = [];
  let last = 0;
  for (const match of content.matchAll(URL_LIKE)) {
    const at = match.index ?? 0;
    const url = trimEnd(match[0]);
    // "https://" with nothing after it, or "www.something" with no domain after it, is not an address.
    const rest = url.replace(/^(?:https?:\/\/|www\.)/i, '');
    if (!rest || (/^www\./i.test(url) && !rest.includes('.'))) continue;
    const href = /^www\./i.test(url) ? `https://${url}` : url;
    try {
      const parsed = new URL(href);
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') continue;
    } catch {
      continue;
    }
    if (at > last) parts.push({ text: content.slice(last, at) });
    parts.push({ text: url, href });
    last = at + url.length;
  }
  if (last < content.length) parts.push({ text: content.slice(last) });
  return parts;
}

/**
 * The path within TeamUp when a link points at TeamUp itself, such as a call's
 * link, so it opens in place rather than in a new tab.
 */
export function inAppPath(href: string, origin: string = window.location.origin): string | null {
  try {
    const u = new URL(href);
    if (u.origin !== origin) return null;
    return u.pathname + u.search + u.hash;
  } catch {
    return null;
  }
}
