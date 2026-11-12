/**
 * What another app shared into TeamUp, waiting for somebody to say whom it is for.
 *
 * The service worker receives the share (public/push-sw.js) and keeps it in
 * this cache; the names here must match the ones there.
 */
const SHARE_INBOX = 'teamup-share-inbox';
const META = '/share-inbox/meta';

export interface Shared {
  /** The words to send, put together from the title, text and link that came. */
  text: string;
  files: File[];
}

interface Meta {
  title?: string;
  text?: string;
  url?: string;
  files?: Array<{ key: string; name: string; type: string }>;
}

/**
 * One message out of what an app shares: apps differ in what they put where,
 * and many put the link in the text as well, so nothing is said twice.
 */
export function composeShared({ title = '', text = '', url = '' }: { title?: string; text?: string; url?: string }): string {
  const t = title.trim();
  const x = text.trim();
  const u = url.trim();
  const parts: string[] = [];
  if (t && !x.includes(t) && t !== u) parts.push(t);
  if (x) parts.push(x);
  if (u && !x.includes(u)) parts.push(u);
  return parts.join('\n');
}

/** What is waiting, or null if nothing is. */
export async function readInbox(): Promise<Shared | null> {
  if (typeof caches === 'undefined') return null;
  try {
    const inbox = await caches.open(SHARE_INBOX);
    const meta = await inbox.match(META);
    if (!meta) return null;
    const m = (await meta.json()) as Meta;
    const files: File[] = [];
    for (const f of m.files ?? []) {
      const r = await inbox.match(f.key);
      if (r) files.push(new File([await r.blob()], f.name, { type: f.type }));
    }
    return { text: composeShared(m), files };
  } catch {
    return null;
  }
}

/** Forgets what was shared, once it is sent or put aside. */
export async function clearInbox(): Promise<void> {
  if (typeof caches === 'undefined') return;
  try {
    await caches.delete(SHARE_INBOX);
  } catch {
    // Nothing to do: the next share replaces it anyway.
  }
}
