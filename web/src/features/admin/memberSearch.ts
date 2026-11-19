// Finding somebody among the members: by what is written about them (name,
// email, company role, teams, roles), narrowed by a team or a role. The
// matching itself is the app's one rule, in shared/utils/peopleSearch.

import { matches, personFacts } from '@/shared/utils/peopleSearch';

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

export function memberMatches(u: Searchable, f: MemberFilter): boolean {
  if (f.team && !(u.teams ?? []).includes(f.team)) return false;
  if (f.role && !(u.roles ?? []).includes(f.role)) return false;
  return matches(personFacts(u), f.query);
}

/** Whether somebody is narrowing the list at all. */
export const filtering = (f: MemberFilter) => !!(f.query.trim() || f.team || f.role);
