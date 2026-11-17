// Finding somebody among the members: by what is written about them (name,
// email, company role, teams, roles), narrowed by a team or a role.

export interface Searchable {
  name: string;
  email?: string;
  jobTitle?: string;
  teams?: string[];
  roles?: string[];
}

export interface MemberFilter {
  /** Free text; every word must match something about them, in any order. */
  query: string;
  /** A team's name, or '' for any. */
  team: string;
  /** A role's name, or '' for any. */
  role: string;
}

/** Lower case, without accents, so "jose" finds "José". */
const fold = (s: string) => s.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLowerCase();

export function memberMatches(u: Searchable, f: MemberFilter): boolean {
  if (f.team && !(u.teams ?? []).includes(f.team)) return false;
  if (f.role && !(u.roles ?? []).includes(f.role)) return false;
  const words = fold(f.query).split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const about = fold([u.name, u.email, u.jobTitle, ...(u.teams ?? []), ...(u.roles ?? [])].filter(Boolean).join(' '));
  return words.every((w) => about.includes(w));
}

/** Whether somebody is narrowing the list at all. */
export const filtering = (f: MemberFilter) => !!(f.query.trim() || f.team || f.role);
