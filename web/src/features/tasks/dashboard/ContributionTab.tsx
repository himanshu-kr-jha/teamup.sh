import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { tasksApi } from '@/shared/api/client';
import type { TaskSummary } from '@/shared/types/dashboard';
import { cn, formatDate } from '@/shared/utils/cn';
import { DANGER_TEXT } from '../dueDate';
import { orderContributions, rangeProblem, rangeToApi, readRange, type DateRange } from './logic';
import { EmptyState, ErrorState, LoadingState, TaskRow, TeamFilter, ViewFrame } from './parts';
import { useDashboard } from './useDashboard';
import { useTabParams } from './useTabParams';
import { PersonAvatar } from '@/shared/components/UserAvatar';

const COLUMNS = ['completed', 'involved', 'addedTo', 'open'] as const;
const COLUMN_LABELS = { completed: 'Completed', involved: 'Involved', addedTo: 'Added to', open: 'Open' };
/** Four numbers and a name have to fit a phone, so the words shorten there. */
const COLUMN_PHONE_LABELS = { completed: 'Done', involved: 'Inv.', addedTo: 'Added', open: 'Open' };

/** Who completed what, and how much they were part of, over a range. */
export function ContributionTab() {
  const { team, person, from, to, update } = useTabParams();
  const { data: dashboard } = useDashboard(team);
  const range = readRange(from, to);
  const problem = rangeProblem(range);

  const { data, error, isPending, refetch } = useQuery({
    queryKey: ['contributions', team ?? 'all', range.from, range.to],
    queryFn: () => tasksApi.contributions({ ...rangeToApi(range), teamId: team }).then((res) => res.data),
    enabled: !problem,
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });

  return (
    <ViewFrame>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
        <div className="grid grid-cols-2 gap-2">
          <div className="min-w-0 space-y-1">
            <Label htmlFor="contributionFrom" className="text-xs">
              From
            </Label>
            <Input
              id="contributionFrom"
              type="date"
              className="h-9"
              value={range.from}
              max={range.to}
              onChange={(e) => update({ from: e.target.value })}
            />
          </div>
          <div className="min-w-0 space-y-1">
            <Label htmlFor="contributionTo" className="text-xs">
              To
            </Label>
            <Input
              id="contributionTo"
              type="date"
              className="h-9"
              value={range.to}
              min={range.from}
              onChange={(e) => update({ to: e.target.value })}
            />
          </div>
        </div>
        <TeamFilter teams={dashboard?.teams ?? []} />
      </div>

      {problem ? (
        <p role="alert" className={cn('text-sm', DANGER_TEXT)}>
          {problem}
        </p>
      ) : isPending ? (
        <LoadingState label="Adding up contributions" />
      ) : !data ? (
        <ErrorState error={error} fallback="Could not load contributions." onRetry={() => refetch()} />
      ) : data.people.length === 0 ? (
        <EmptyState title="Nobody here yet" hint="People on the teams you manage will show up here." />
      ) : (
        <>
          <div className="rounded-lg border border-border bg-card">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border text-xs text-muted-foreground">
                  <th scope="col" className="py-2 pl-3 pr-1 text-left font-medium sm:px-3">
                    Person
                  </th>
                  {COLUMNS.map((column) => (
                    <th key={column} scope="col" className="whitespace-nowrap px-1 py-2 text-right font-medium last:pr-3 sm:w-24 sm:px-3">
                      <span className="sm:hidden" aria-hidden>
                        {COLUMN_PHONE_LABELS[column]}
                      </span>
                      <span className="sr-only sm:not-sr-only">{COLUMN_LABELS[column]}</span>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {orderContributions(data.people).map((row) => (
                  <tr
                    key={row.userId}
                    className="cursor-pointer hover:bg-accent"
                    onClick={() => update({ person: row.userId })}
                  >
                    <td className="w-full max-w-0 truncate py-2 pl-3 pr-1 sm:px-3">
                      {/* The row is the big target; this is the one a keyboard reaches. */}
                      <button
                        type="button"
                        className="flex max-w-full items-center gap-2 text-left hover:underline"
                        onClick={(e) => {
                          e.stopPropagation();
                          update({ person: row.userId });
                        }}
                      >
                        <PersonAvatar userId={row.userId} name={row.name} className="h-6 w-6 shrink-0" fallbackClassName="text-[10px]" />
                        <span className="truncate">{row.name}</span>
                      </button>
                    </td>
                    {COLUMNS.map((column) => (
                      <td
                        key={column}
                        className={cn(
                          'px-1 py-2 text-right tabular-nums last:pr-3 sm:px-3',
                          (row[column] ?? 0) === 0 && 'text-muted-foreground'
                        )}
                      >
                        {row[column] ?? 0}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {error && (
            <p role="status" className={cn('text-xs', DANGER_TEXT)}>
              Could not refresh. These are the last numbers.
            </p>
          )}
        </>
      )}

      {person && !problem && (
        <ContributionDialog userId={person} range={range} onClose={() => update({ person: null })} />
      )}
    </ViewFrame>
  );
}

function ContributionDialog({
  userId,
  range,
  onClose,
}: {
  userId: string;
  range: DateRange;
  onClose: () => void;
}) {
  const { data, error, isPending, refetch } = useQuery({
    queryKey: ['contribution-detail', userId, range.from, range.to],
    queryFn: () => tasksApi.contributionDetail(userId, rangeToApi(range)).then((res) => res.data),
    staleTime: 30_000,
  });

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle className="break-anywhere pr-6">{data?.name ?? 'Contribution'}</DialogTitle>
          <DialogDescription>
            {formatDate(`${range.from}T00:00:00`)} to {formatDate(`${range.to}T00:00:00`)}
          </DialogDescription>
        </DialogHeader>
        {isPending ? (
          <LoadingState label="Loading their tasks" />
        ) : !data ? (
          <ErrorState error={error} fallback="Could not load their tasks." onRetry={() => refetch()} />
        ) : (
          <div className="min-w-0 space-y-4">
            <TaskGroup title="Completed" tasks={data.completed} />
            <TaskGroup title="Involved" tasks={data.involved} />
            <TaskGroup title="Added to" tasks={data.addedTo ?? []} />
            <TaskGroup title="Open" tasks={data.open} />
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function TaskGroup({ title, tasks }: { title: string; tasks: TaskSummary[] }) {
  return (
    <section className="min-w-0">
      <h3 className="mb-1 flex items-center gap-2 text-sm font-medium">
        {title} <span className="font-normal tabular-nums text-muted-foreground">{tasks.length}</span>
      </h3>
      {tasks.length === 0 ? (
        <p className="text-sm text-muted-foreground">None</p>
      ) : (
        <ul className="divide-y divide-border">
          {tasks.map((task) => (
            <TaskRow key={task.id} task={task} />
          ))}
        </ul>
      )}
    </section>
  );
}
