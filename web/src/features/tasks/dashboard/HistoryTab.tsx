import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { ChevronLeft, Loader2 } from 'lucide-react';
import { Badge } from '@/shared/components/ui/badge';
import { Button } from '@/shared/components/ui/button';
import { scrumsApi } from '@/shared/api/client';
import { formatDate, formatTime } from '@/shared/utils/cn';
import { groupSnapshot, nextBefore, scrumLabel } from './logic';
import { EmptyState, ErrorState, LoadingState, ScrumPersonCard, TeamFilter, ViewFrame } from './parts';
import { useDashboard } from './useDashboard';
import { useTabParams } from './useTabParams';

const PAGE_SIZE = 20;

const when = (iso: string) => `${formatDate(iso)} at ${formatTime(iso)}`;
const tasksWord = (n: number) => `${n} ${n === 1 ? 'task' : 'tasks'}`;
const peopleWord = (n: number) => `${n} ${n === 1 ? 'person' : 'people'}`;

export function HistoryTab() {
  const { scrum } = useTabParams();
  return scrum ? <SavedScrum id={scrum} /> : <ScrumList />;
}

function ScrumList() {
  const { team, linkTo } = useTabParams();
  const { data: dashboard } = useDashboard(team);
  const { data, error, isPending, refetch, hasNextPage, fetchNextPage, isFetchingNextPage, isFetchNextPageError } =
    useInfiniteQuery({
      queryKey: ['scrums', team ?? 'all'],
      queryFn: ({ pageParam }) =>
        scrumsApi.list({ teamId: team, limit: PAGE_SIZE, before: pageParam }).then((res) => res.data),
      initialPageParam: undefined as string | undefined,
      getNextPageParam: (page) => nextBefore(page, PAGE_SIZE),
      staleTime: 30_000,
    });
  const scrums = data?.pages.flat() ?? [];

  return (
    <ViewFrame>
      {!!dashboard?.teams.length && (
        <div className="flex justify-end">
          <TeamFilter teams={dashboard.teams} />
        </div>
      )}

      {isPending ? (
        <LoadingState label="Loading saved scrums" />
      ) : !data ? (
        <ErrorState error={error} fallback="Could not load saved scrums." onRetry={() => refetch()} />
      ) : scrums.length === 0 ? (
        <EmptyState title="No scrums saved yet" hint="Complete one from the Scrum tab and it will stay here." />
      ) : (
        <>
          <ul className="divide-y divide-border rounded-lg border border-border bg-card">
            {scrums.map((s) => (
              <li key={s.id}>
                <Link
                  to={linkTo('history', { scrum: s.id })}
                  className="flex flex-col gap-x-4 gap-y-0.5 px-3 py-2.5 hover:bg-accent sm:flex-row sm:items-center md:px-4"
                >
                  <span className="min-w-0 flex-1">
                    <span className="block text-sm font-medium tabular-nums">{when(s.createdAt)}</span>
                    <span className="block break-anywhere text-xs text-muted-foreground">
                      {scrumLabel(s)}, held by {s.heldByName}
                    </span>
                  </span>
                  <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
                    {peopleWord(s.participantCount)}, {tasksWord(s.entryCount)}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
          {hasNextPage && (
            <div className="text-center">
              {isFetchNextPageError && (
                <p role="alert" className="mb-2 text-sm text-muted-foreground">
                  Could not load more.
                </p>
              )}
              <Button variant="outline" size="sm" onClick={() => fetchNextPage()} disabled={isFetchingNextPage}>
                {isFetchingNextPage && <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden />}
                Load more
              </Button>
            </div>
          )}
        </>
      )}
    </ViewFrame>
  );
}

/** A completed scrum, exactly as it stood. Nothing here can be changed. */
function SavedScrum({ id }: { id: string }) {
  const { linkTo } = useTabParams();
  const { data, error, isPending, refetch } = useQuery({
    queryKey: ['scrums', 'one', id],
    queryFn: () => scrumsApi.get(id).then((res) => res.data),
    // Immutable on the server, so one read is good for as long as it is cached.
    staleTime: Infinity,
  });

  return (
    <ViewFrame>
      <Button asChild variant="ghost" size="sm" className="-ml-2">
        <Link to={linkTo('history')}>
          <ChevronLeft className="mr-1 h-4 w-4" aria-hidden />
          All scrums
        </Link>
      </Button>

      {isPending ? (
        <LoadingState label="Loading that scrum" />
      ) : !data ? (
        <ErrorState error={error} fallback="Could not load that scrum." onRetry={() => refetch()} />
      ) : (
        <>
          <div className="space-y-1">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-lg font-semibold tabular-nums">{when(data.createdAt)}</h2>
              <Badge variant="secondary">Saved, read-only</Badge>
            </div>
            <p className="break-anywhere text-sm text-muted-foreground">
              {scrumLabel(data)}, held by {data.heldByName}. Done since {when(data.doneSince)}.
            </p>
          </div>
          {data.notes && (
            <div className="rounded-lg border border-border bg-muted/40 p-3">
              <h3 className="mb-1 text-xs font-medium text-muted-foreground">Notes</h3>
              <p className="whitespace-pre-wrap break-anywhere text-sm">{data.notes}</p>
            </div>
          )}
          {data.participants.length === 0 ? (
            <EmptyState title="Nobody was on this scrum" />
          ) : (
            <div className="space-y-2">
              {groupSnapshot(data).map((row) => (
                <ScrumPersonCard key={row.userId} row={row} />
              ))}
            </div>
          )}
        </>
      )}
    </ViewFrame>
  );
}
