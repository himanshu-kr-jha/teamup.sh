import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { orgApi } from '@/shared/api/client';
import { useAuthStore } from '@/shared/store/authStore';

interface DirectoryEntry {
  email?: string;
  jobTitle?: string;
  teams?: string[];
}

export interface Directory {
  /** What the organization knows about somebody, for finding them in any list. */
  about: (userId: string) => DirectoryEntry | undefined;
  /** The names of the people on a team, by the team's name. */
  membersOf: (teamName: string) => string[];
}

/**
 * Everybody's email, company role and teams, from the one member list the app
 * already keeps. Lists that only carry a name and an id (an audience, the
 * people somebody may assign to) still find people by all of it.
 */
export function usePeopleDirectory(): Directory {
  const signedIn = useAuthStore((s) => s.isAuthenticated);
  const { data } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers(), enabled: signedIn });
  return useMemo(() => {
    const byId = new Map<string, DirectoryEntry>();
    const byTeam = new Map<string, string[]>();
    for (const u of (data?.data ?? []) as Array<{ id: string; name?: string; email?: string; jobTitle?: string; teams?: string[] }>) {
      byId.set(u.id, { email: u.email, jobTitle: u.jobTitle, teams: u.teams });
      for (const team of u.teams ?? []) byTeam.set(team, [...(byTeam.get(team) ?? []), u.name ?? '']);
    }
    return { about: (id) => byId.get(id), membersOf: (team) => byTeam.get(team) ?? [] };
  }, [data]);
}
