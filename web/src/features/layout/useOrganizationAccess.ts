import { useQuery } from '@tanstack/react-query';
import { useAuth } from '@/features/auth/AuthProvider';
import { tasksApi } from '@/shared/api/client';

/**
 * Whether somebody has any business on the Organization screen.
 *
 * For an ordinary member it was a page listing colleagues they could not
 * change, so it is not offered to them at all. Administrators run the place;
 * anybody who runs a team manages that team here, which is a real job and not
 * one the global role records — a team's manager is named on the team, so it
 * has to be asked for rather than read off the session.
 *
 * `resolved` says whether that question has been answered yet. Deciding to turn
 * somebody away before it has would bounce a manager off their own teams page.
 */
export function useOrganizationAccess(): { allowed: boolean; resolved: boolean } {
  const { user } = useAuth();
  const isAdmin = user?.roles?.some((r) => r === 'owner' || r === 'admin') ?? false;

  // Same key the tasks page uses, so this is usually already answered.
  const { data, isPending } = useQuery({
    queryKey: ['managed-teams'],
    queryFn: () => tasksApi.managedTeams(),
    enabled: !isAdmin,
  });

  if (isAdmin) return { allowed: true, resolved: true };
  return { allowed: (data?.data ?? []).length > 0, resolved: !isPending };
}
