import { execFileSync } from 'node:child_process';
import { expect, type APIRequestContext, type Page } from '@playwright/test';
import { Api } from './api';
import { OWNER, PEOPLE, readState, type Person, type PersonKey } from './people';
import type { Session } from './fixtures';

/** The people the specs for meetings add beyond the seed, so a call can reach nine. */
export const EXTRA: Record<'zoe' | 'zed' | 'olga', Person & { key: string }> = {
  zoe: { key: 'zoe', name: 'Zoe Zero', email: 'zoe@e2e.test', password: 'E2e-pass-12345!' },
  zed: { key: 'zed', name: 'Zed Zone', email: 'zed@e2e.test', password: 'E2e-pass-12345!' },
  // Lives in a second organization; see addOtherOrg.
  olga: { key: 'olga', name: 'Olga Other', email: 'olga@other.test', password: 'E2e-pass-12345!' },
};

export async function apiAs(who: PersonKey | Person): Promise<Api> {
  const p = typeof who === 'string' ? PEOPLE[who] : who;
  return Api.signIn(p.email, p.password);
}

export const idOf = (who: PersonKey) => readState().ids[who];

/** Runs SQL against the database the server under test uses. For setup the UI cannot do, never for assertions about behaviour. */
export function sql(statement: string): string {
  const url = process.env.E2E_DATABASE_URL;
  if (!url) throw new Error('E2E_DATABASE_URL is not set; run through web/e2e/run.sh or run-meetings.sh');
  return execFileSync('psql', [url, '-At', '-v', 'ON_ERROR_STOP=1', '-c', statement], { encoding: 'utf8' }).trim();
}

const extraIds: Record<string, string> = {};

/** Adds an ordinary member, settled the way a person does on first sign-in. Idempotent. */
export async function ensureMember(person: Person & { key: string }): Promise<string> {
  if (extraIds[person.key]) return extraIds[person.key];
  const owner = await Api.signIn(OWNER.email, OWNER.password);
  try {
    const list: any = await owner.get('/users');
    const found = (list.users ?? list.data ?? list).find((u: any) => u.email === person.email);
    if (found) return (extraIds[person.key] = found.id);
    const created = await owner.post('/users', { name: person.name, email: person.email });
    const them = await Api.signIn(person.email, created.temporaryPassword);
    await them.post('/auth/password', { currentPassword: created.temporaryPassword, newPassword: person.password });
    await them.dispose();
    return (extraIds[person.key] = created.user.id);
  } finally {
    await owner.dispose();
  }
}

/** A second organization with one person, made with SQL because the product has one organization per install. */
export async function ensureOtherOrg(): Promise<string> {
  const existing = sql(`SELECT id FROM users WHERE email = '${EXTRA.olga.email}'`);
  if (existing) return existing;
  // Borrow Alice's password hash: everybody in the seed signs in with the same password.
  sql(`
    WITH o AS (INSERT INTO organizations (name, subdomain) VALUES ('Other Org', 'other') RETURNING id)
    INSERT INTO users (org_id, name, email, password_hash, role, is_admin, is_active)
    SELECT o.id, '${EXTRA.olga.name}', '${EXTRA.olga.email}', (SELECT password_hash FROM users WHERE email = '${PEOPLE.alice.email}'), 'admin', true, true FROM o`);
  return sql(`SELECT id FROM users WHERE email = '${EXTRA.olga.email}'`);
}

export const iso = (d: Date) => d.toISOString();
/** A time today (or offset days from today) in the browser's, and so this machine's, zone. */
export function at(dayOffset: number, hour: number, minute = 0): Date {
  const d = new Date();
  d.setDate(d.getDate() + dayOffset);
  d.setHours(hour, minute, 0, 0);
  return d;
}
/** The value a datetime-local input takes. */
export function localInput(d: Date): string {
  return new Date(d.getTime() - d.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}
export const dayParam = (d: Date) => localInput(d).slice(0, 10);

/** Someone who calls the log "no data": webhooks never reached the server. */
export const WEBHOOKS_DOWN =
  'WEBHOOKS NEVER ARRIVED: LiveKit did not call POST /api/livekit/webhook, so no attendance was recorded. ' +
  'This is a setup fault, not a product bug. Check the LiveKit container posts to the port the server listens on ' +
  '(web/e2e/README-services.md; web/e2e/run-meetings.sh checks it before starting).';

export interface LogShape {
  events: { type: string; userId: string; userName?: string; actorId?: string; at: string }[];
  sessions: { userId: string; userName?: string; joinedAt: string | null; leftAt: string | null }[];
  attendanceRecorded: boolean;
}

/**
 * Polls a call's log until the webhooks have produced what the check wants.
 * If they never produced anything at all, says so as a setup fault; if they
 * produced something else, it is the product's.
 */
export async function waitForLog(
  api: Api,
  callId: string,
  what: string,
  check: (log: LogShape) => boolean,
  timeoutMs = 30_000
): Promise<LogShape> {
  const deadline = Date.now() + timeoutMs;
  let log: LogShape = { events: [], sessions: [], attendanceRecorded: false };
  while (Date.now() < deadline) {
    log = await api.get(`/calls/${callId}/log`);
    if (check(log)) return log;
    await new Promise((r) => setTimeout(r, 500));
  }
  if (!log.attendanceRecorded && log.sessions.length === 0) throw new Error(`${WEBHOOKS_DOWN}\nWaiting for: ${what}`);
  throw new Error(`Webhooks arrive (${log.sessions.length} sessions, ${log.events.length} events) but this never happened: ${what}\n${JSON.stringify(log, null, 2)}`);
}

/** Counts the chime. Call before the page that joins loads. The chime is two oscillators. */
export async function spyOnAudio(session: Session) {
  await session.context.addInitScript(() => {
    const w = window as unknown as { __oscillators: number; __oscillatorAt: string[] };
    w.__oscillators = 0;
    w.__oscillatorAt = [];
    const original = AudioContext.prototype.createOscillator;
    AudioContext.prototype.createOscillator = function (this: AudioContext) {
      w.__oscillators++;
      w.__oscillatorAt.push(`${performance.now() | 0}ms ${new Error().stack?.split('\n').slice(2, 5).join(' | ')}`);
      return original.call(this);
    };
  });
}
/** Forget sounds so far: an invited person's phone may ring a moment while they follow the link, and that is not the chime. */
export const resetOscillators = (page: Page) =>
  page.evaluate(() => {
    const w = window as unknown as { __oscillators: number; __oscillatorAt: string[] };
    w.__oscillators = 0;
    w.__oscillatorAt = [];
  });
export const oscillators = (page: Page) => page.evaluate(() => (window as unknown as { __oscillators?: number }).__oscillators ?? 0);

/** Follows a call's link and waits to be in it. A meeting nobody has started asks first; see lobbyJoin. */
export async function joinCall(page: Page, callId: string) {
  await page.goto(`/calls/${callId}`);
  await expect(page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
}
/** The button that starts a scheduled meeting from its page. */
export const lobbyJoin = (page: Page) => page.getByRole('button', { name: /^Join( now)?$/ });

export const leaveCall = (page: Page) => page.getByRole('button', { name: 'Leave the call' }).click();

export async function createCall(api: Api, title: string, invitees: string[] = []) {
  return api.post('/calls', { title, callType: 'video', invitees });
}

export async function requestStatus(ctx: APIRequestContext, path: string) {
  return (await ctx.get(`/api${path}`)).status();
}
