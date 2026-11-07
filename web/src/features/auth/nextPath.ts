/**
 * Where to go after signing in. Somebody sent a link to a call who is not signed in
 * should arrive at that call and not at the chat page, or the link has done nothing.
 */
const HOME = '/chat';

export function loginPathFor(pathname: string, search: string): string {
  if (pathname === '/' || pathname === HOME || pathname === '/login') return '/login';
  return `/login?next=${encodeURIComponent(pathname + search)}`;
}

/** Only ever a path on this site, since the address can be typed by anybody. */
export function safeNext(next: string | null): string {
  if (!next || !next.startsWith('/') || next.startsWith('//')) return HOME;
  if (/[\\\s]/.test(next)) return HOME;
  if (next === '/login' || next.startsWith('/login?')) return HOME;
  return next;
}
