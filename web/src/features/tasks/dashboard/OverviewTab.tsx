import { Link } from 'react-router-dom';
import type { DashboardPerson, TaskCounts } from '@/shared/types/dashboard';
import { cn } from '@/shared/utils/cn';
import { DANGER_TEXT } from '../dueDate';
import { bucketSections, completedCaption, orderPeople } from './logic';
import { BucketList, EmptyState, ErrorState, LoadingState, StatTiles, TeamFilter, ViewFrame } from './parts';
import { useDashboard } from './useDashboard';
import { useTabParams } from './useTabParams';

export function OverviewTab() {
  const { team } = useTabParams();
  const { data, error, refetch } = useDashboard(team);

  if (!data) {
    return (
      <ViewFrame>
        {error ? (
          <ErrorState error={error} fallback="Could not load your overview." onRetry={() => refetch()} />
        ) : (
          <LoadingState label="Loading your overview" />
        )}
      </ViewFrame>
    );
  }

  const me = data.scope === 'self' ? data.people[0] : undefined;

  return (
    <ViewFrame>
      {data.teams.length > 0 && (
        <div className="flex justify-end">
          <TeamFilter teams={data.teams} />
        </div>
      )}
      <StatTiles counts={data.totals} />
      <p className="text-xs text-muted-foreground">{completedCaption(data.scope, data.lastScrumAt)}</p>

      {data.scope === 'self' ? (
        me && bucketSections(me.tasks).length > 0 ? (
          <BucketList sections={bucketSections(me.tasks)} />
        ) : (
          <EmptyState title="Nothing on your plate" hint="Tasks assigned to you, or that somebody adds you to, will show up here." />
        )
      ) : data.people.length > 0 ? (
        <PeopleTable people={data.people} />
      ) : (
        <EmptyState title="Nobody here yet" hint="People on the teams you manage will show up here." />
      )}
    </ViewFrame>
  );
}

const COLUMNS: { key: keyof TaskCounts; label: string; phone?: string; warn?: boolean }[] = [
  { key: 'pending', label: 'Pending' },
  { key: 'ongoing', label: 'Ongoing' },
  { key: 'blocked', label: 'Blocked', warn: true },
  { key: 'overdue', label: 'Overdue', warn: true },
  // The words that will not fit in a column on a phone.
  { key: 'addedTo', label: 'Added to', phone: 'Added' },
  { key: 'completed', label: 'Completed', phone: 'Done' },
];

function PeopleTable({ people }: { people: DashboardPerson[] }) {
  const { linkTo } = useTabParams();
  return (
    <div className="rounded-lg border border-border bg-card">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border text-[10px] text-muted-foreground sm:text-xs">
            <th scope="col" className="py-2 pl-3 pr-1 text-left font-medium sm:px-3">
              Person
            </th>
            {COLUMNS.map(({ key, label, phone }) => (
              <th key={key} scope="col" className="whitespace-nowrap px-0.5 py-2 text-right font-medium last:pr-3 sm:w-24 sm:px-3">
                {phone ? (
                  <>
                    <span className="sm:hidden" aria-hidden>
                      {phone}
                    </span>
                    <span className="sr-only sm:not-sr-only">{label}</span>
                  </>
                ) : (
                  label
                )}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {orderPeople(people).map((person) => (
            <tr key={person.userId}>
              <td className="w-full max-w-0 truncate py-2 pl-3 pr-1 sm:px-3">
                <Link to={linkTo('person', { person: person.userId })} className="hover:underline">
                  {person.name}
                </Link>
              </td>
              {COLUMNS.map(({ key, warn }) => (
                <td
                  key={key}
                  className={cn(
                    'px-0.5 py-2 text-right tabular-nums last:pr-3 sm:px-3',
                    (person.counts[key] ?? 0) === 0 && 'text-muted-foreground',
                    warn && (person.counts[key] ?? 0) > 0 && DANGER_TEXT
                  )}
                >
                  {person.counts[key] ?? 0}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
