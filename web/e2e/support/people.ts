import { existsSync, readFileSync } from 'node:fs';

/** Everyone the seed creates, and the one password they all sign in with. */
export const PASSWORD = 'E2e-pass-12345!';

export const OWNER = {
  name: 'Olivia Owner',
  email: process.env.E2E_OWNER_EMAIL ?? 'owner@e2e.test',
  password: process.env.E2E_OWNER_PASSWORD ?? 'Owner-pass-12345',
};

export type PersonKey = 'owner' | 'mia' | 'max' | 'alice' | 'ann' | 'pat' | 'sam';

export interface Person {
  name: string;
  email: string;
  password: string;
}

export const PEOPLE: Record<PersonKey, Person> = {
  owner: OWNER,
  mia: { name: 'Mia Manager', email: 'mia@e2e.test', password: PASSWORD },
  max: { name: 'Max Manager', email: 'max@e2e.test', password: PASSWORD },
  alice: { name: 'Alice Archer', email: 'alice@e2e.test', password: PASSWORD },
  ann: { name: 'Ann Idle', email: 'ann@e2e.test', password: PASSWORD },
  pat: { name: 'Pat Platform', email: 'pat@e2e.test', password: PASSWORD },
  sam: { name: 'Sam Sales', email: 'sam@e2e.test', password: PASSWORD },
};

/** Titles of the tasks the seed creates, so tests can name them. */
export const TASKS = {
  aliceSpec: 'Alice write spec',
  aliceApi: 'Alice build API',
  aliceBlocked: 'Alice waiting on vendor',
  aliceReport: 'Alice finish report',
  patPipeline: 'Pat tune pipeline',
  patLogs: 'Pat clean logs',
  patCerts: 'Pat renew certificates',
  samCalls: 'Sam call prospects',
  samQuote: 'Sam draft quote',
};

export interface SeedState {
  ids: Record<PersonKey, string>;
  teams: { backend: string; platform: string; sales: string };
  channelId: string;
}

export const STATE_FILE = process.env.E2E_STATE_FILE ?? '/tmp/teamup-e2e-state.json';

export function readState(): SeedState {
  if (!existsSync(STATE_FILE)) throw new Error(`no seed state at ${STATE_FILE}; global setup did not run`);
  return JSON.parse(readFileSync(STATE_FILE, 'utf8'));
}
