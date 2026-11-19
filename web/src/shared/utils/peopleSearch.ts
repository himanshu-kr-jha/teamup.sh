// Finding somebody, or a team, in a list: the one rule every people and team
// list in the app uses, so a name found in one place is found the same way in
// all of them.
//
// Every word typed has to match something about them, in any order, ignoring
// case and accents. Matches are ranked: a name that starts with the word first,
// then a word in the name that starts with it, then a word of anything else
// (email, company role, team) that does, then anything that merely contains it.

/** Lower case, without accents, so "jose" finds "José". */
export const fold = (s: string) => s.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLowerCase();

const wordsOf = (s: string) => s.split(/[^\p{L}\p{N}]+/u).filter(Boolean);

/** What a search may match about one thing in a list. */
export interface Facts {
  /** What it is called: a name, a handle. A match here ranks highest. */
  primary: Array<string | null | undefined>;
  /** Everything else known about it: an email, a company role, teams, members. */
  secondary?: Array<string | null | undefined>;
}

export interface Indexed<T> {
  item: T;
  /** Each primary text, folded whole. */
  names: string[];
  nameWords: string[];
  otherWords: string[];
  /** Everything, folded and joined, for a match in the middle of a word. */
  all: string;
}

/** Folds everything once, so typing only compares strings already made. */
export function indexBy<T>(items: T[], facts: (item: T) => Facts): Indexed<T>[] {
  return items.map((item) => {
    const f = facts(item);
    const names = f.primary.filter((s): s is string => !!s).map(fold);
    const others = (f.secondary ?? []).filter((s): s is string => !!s).map(fold);
    return {
      item,
      names,
      nameWords: names.flatMap(wordsOf),
      otherWords: others.flatMap(wordsOf),
      all: [...names, ...others].join(' | '),
    };
  });
}

/** The words of a query, folded; none means everything matches. */
export const queryWords = (query: string) => fold(query).split(/\s+/).filter(Boolean);

/** How well one typed word matches: lower is better, null is no match. */
function scoreWord(word: string, e: Indexed<unknown>): number | null {
  if (e.names.some((n) => n.startsWith(word))) return 0;
  if (e.nameWords.some((w) => w.startsWith(word))) return 1;
  if (e.otherWords.some((w) => w.startsWith(word))) return 2;
  if (e.all.includes(word)) return 3;
  return null;
}

/** How well a whole query matches, or null when any word matches nothing. */
export function scoreOf(e: Indexed<unknown>, words: string[]): number | null {
  let total = 0;
  for (const w of words) {
    const s = scoreWord(w, e);
    if (s === null) return null;
    total += s;
  }
  return total;
}

/**
 * The items that match, best first; among equals, in the order given. An empty
 * query keeps everything in the order given.
 */
export function search<T>(index: Indexed<T>[], query: string): T[] {
  const words = queryWords(query);
  if (words.length === 0) return index.map((e) => e.item);
  const scored: Array<{ item: T; score: number; at: number }> = [];
  index.forEach((e, at) => {
    const score = scoreOf(e, words);
    if (score !== null) scored.push({ item: e.item, score, at });
  });
  return scored.sort((a, b) => a.score - b.score || a.at - b.at).map((s) => s.item);
}

/** Ranks in one go, for a list searched once. */
export function rankBy<T>(items: T[], query: string, facts: (item: T) => Facts): T[] {
  if (queryWords(query).length === 0) return items;
  return search(indexBy(items, facts), query);
}

/** Whether one thing matches a query at all. */
export function matches(f: Facts, query: string): boolean {
  const words = queryWords(query);
  if (words.length === 0) return true;
  return scoreOf(indexBy([null], () => f)[0], words) !== null;
}

/** What is known about a person that a search can find them by. */
export interface PersonLike {
  name: string;
  email?: string | null;
  jobTitle?: string | null;
  teams?: string[] | null;
  roles?: string[] | null;
  /** Other names they go by, such as a mention handle. */
  aliases?: string[];
}

export const personFacts = (p: PersonLike): Facts => ({
  primary: [p.name, ...(p.aliases ?? [])],
  secondary: [p.email, p.jobTitle, ...(p.teams ?? []), ...(p.roles ?? [])],
});

/** A team is found by its name, and by the names of the people on it. */
export interface TeamLike {
  name: string;
  members?: string[] | null;
}

export const teamFacts = (t: TeamLike): Facts => ({ primary: [t.name], secondary: t.members ?? [] });

/** By name, A to Z, ignoring case and accents, as every people list is shown. */
export const byName = <T extends { name?: string | null }>(a: T, b: T) =>
  (a.name ?? '').localeCompare(b.name ?? '', undefined, { sensitivity: 'base' });
