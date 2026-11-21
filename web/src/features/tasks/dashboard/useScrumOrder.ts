import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { scrumsApi } from '@/shared/api/client';
import { isClientError } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import type { ScrumOrder } from '@/shared/types/dashboard';

const SAVING = ['scrumOrder', 'save'] as const;

/**
 * The viewer's own order of the scrum table for one view, kept on the server so
 * it is the same after a reload and on another device.
 *
 * A move shows at once and is saved behind it. Saves run one after another, so
 * two quick moves cannot land in the wrong order, and the server's copy is read
 * back only once the last of them is in, so the table never jumps back to an
 * order on its way out. A save that fails puts back what the server has.
 */
export function useScrumOrder(teamId: string | undefined, { enabled = true }: { enabled?: boolean } = {}) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const key = ['scrumOrder', teamId ?? 'all'];

  const query = useQuery({
    queryKey: key,
    queryFn: () => scrumsApi.order(teamId).then((res) => res.data),
    enabled,
    staleTime: 30_000,
    retry: (failures, error) => failures < 1 && !isClientError(error),
  });

  const save = useMutation({
    mutationKey: SAVING,
    scope: { id: 'scrumOrder' },
    mutationFn: async (change: { people: string[] } | { personId: string; tasks: string[] }) => {
      if ('people' in change) await scrumsApi.setPeopleOrder({ teamId, people: change.people });
      else await scrumsApi.setTaskOrder(change.personId, change.tasks);
    },
    onError: () => toast.error('Your order could not be saved. Try again.', 'Not saved'),
    onSettled: () => {
      // Task orders are shared by every view, so every view's copy is stale.
      if (queryClient.isMutating({ mutationKey: SAVING }) <= 1) {
        return queryClient.invalidateQueries({ queryKey: ['scrumOrder'] });
      }
    },
  });

  const show = async (change: (order: ScrumOrder) => ScrumOrder) => {
    // A read in flight would land on top of the move, so it is called off first.
    await queryClient.cancelQueries({ queryKey: key });
    queryClient.setQueryData<ScrumOrder>(key, (old) => change(old ?? { people: [], tasks: {} }));
  };

  return {
    order: query.data,
    /** Asked for and not here yet. */
    isPending: enabled && query.isPending,
    error: query.error,
    movePeople: async (people: string[]) => {
      await show((order) => ({ ...order, people }));
      save.mutate({ people });
    },
    moveTasks: async (personId: string, tasks: string[]) => {
      await show((order) => ({
        ...order,
        tasks: { ...order.tasks, [personId]: tasks },
      }));
      save.mutate({ personId, tasks });
    },
  };
}
