import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { tasksApi } from '@/shared/api/client';
import { isClientError } from '@/shared/api/errors';

const SCRUM_REFRESH_MS = 30_000;

/**
 * The dashboard is live work, so it is never served from cache without checking:
 * staleTime 0 is what lets returning to the window actually refetch.
 * `poll` is for the scrum view, which is on screen during a call.
 */
export function useDashboard(teamId: string | undefined, { poll = false }: { poll?: boolean } = {}) {
  return useQuery({
    queryKey: ['dashboard', teamId ?? 'all'],
    queryFn: () => tasksApi.dashboard(teamId).then((res) => res.data),
    placeholderData: keepPreviousData,
    staleTime: 0,
    refetchOnWindowFocus: true,
    // A team you do not answer for will not become one on a second try.
    retry: (failures, error) => failures < 1 && !isClientError(error),
    refetchInterval: poll ? SCRUM_REFRESH_MS : false,
  });
}
