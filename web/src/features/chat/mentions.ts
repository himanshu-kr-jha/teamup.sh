/**
 * Naming somebody in a message.
 *
 * A mention is written as @ and a handle: somebody's username, the part of
 * their email before the @ (@alice), or their name with the spaces taken out
 * (@AliceArcher), in any case. The server reads the same handles to decide who
 * is notified (internal/chat notifyMentions); this file shows them and helps
 * write them.
 */

export interface Mentionable {
  id: string;
  name: string;
  email?: string;
}

/** What the server reads as a handle, with trailing punctuation left out. */
const HANDLE = /@([A-Za-z0-9._-]+)/g;

export function usernameOf(person: Mentionable): string {
  return (person.email ?? '').split('@')[0] ?? '';
}

function squash(name: string): string {
  return name.replace(/\s+/g, '').toLowerCase();
}

/** Who a handle names, if anybody. */
export function resolveHandle(handle: string, people: Mentionable[]): Mentionable | undefined {
  const h = handle.replace(/[._-]+$/, '').toLowerCase();
  if (!h) return undefined;
  return (
    people.find((p) => usernameOf(p).toLowerCase() === h) ?? people.find((p) => squash(p.name) === h)
  );
}

export type MentionPart = { text: string } | { text: string; person: Mentionable };

/**
 * A message cut into plain text and mentions of real people. An @ that names
 * nobody, an email address, or a handle for somebody not in the list stays text.
 */
export function splitMentions(content: string, people: Mentionable[]): MentionPart[] {
  const parts: MentionPart[] = [];
  let last = 0;
  for (const match of content.matchAll(HANDLE)) {
    const at = match.index ?? 0;
    // Part of a word, such as an email address: not a mention.
    if (at > 0 && /[A-Za-z0-9._-]/.test(content[at - 1])) continue;
    const person = resolveHandle(match[1], people);
    if (!person) continue;
    const trimmed = match[1].replace(/[._-]+$/, '');
    const end = at + 1 + trimmed.length;
    if (at > last) parts.push({ text: content.slice(last, at) });
    parts.push({ text: content.slice(at, end), person });
    last = end;
  }
  if (last < content.length) parts.push({ text: content.slice(last) });
  return parts;
}

/**
 * The @-word being typed just before the caret, if any: what the suggestions
 * are for, and where a choice replaces it.
 */
export function mentionQuery(text: string, caret: number): { query: string; start: number } | null {
  const before = text.slice(0, caret);
  const match = /(^|\s)@([A-Za-z0-9._-]*)$/.exec(before);
  if (!match) return null;
  return { query: match[2], start: caret - match[2].length - 1 };
}

/** The people worth offering for a query, best matches first. */
export function suggestPeople(query: string, people: Mentionable[], limit = 6): Mentionable[] {
  const q = query.toLowerCase();
  const score = (p: Mentionable) => {
    const name = p.name.toLowerCase();
    const user = usernameOf(p).toLowerCase();
    if (!q) return 3;
    if (user.startsWith(q) || name.startsWith(q)) return 0;
    if (name.split(/\s+/).some((w) => w.startsWith(q))) return 1;
    if (name.includes(q) || user.includes(q)) return 2;
    return -1;
  };
  return people
    .map((p) => ({ p, s: score(p) }))
    .filter((x) => x.s >= 0)
    .sort((a, b) => a.s - b.s || a.p.name.localeCompare(b.p.name))
    .slice(0, limit)
    .map((x) => x.p);
}

/** The text with the @-word at `start` replaced by the person's handle and a space. */
export function insertMention(text: string, start: number, caret: number, person: Mentionable): { text: string; caret: number } {
  const handle = `@${usernameOf(person) || person.name.replace(/\s+/g, '')} `;
  const next = text.slice(0, start) + handle + text.slice(caret);
  return { text: next, caret: start + handle.length };
}
