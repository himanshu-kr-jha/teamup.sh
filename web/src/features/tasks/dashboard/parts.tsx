import { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { AlertTriangle, Loader2 } from 'lucide-react';
import { Badge } from '@/shared/components/ui/badge';
import { Button } from '@/shared/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { apiErrorMessage, isClientError } from '@/shared/api/errors';
import type { TaskCounts, TeamRef } from '@/shared/types/dashboard';
import { cn } from '@/shared/utils/cn';
import { DANGER_TEXT, taskDateLabel } from '../dueDate';
import type { BucketSection, ScrumRow } from './logic';
import { useTabParams } from './useTabParams';

/** The scrolling page every dashboard view sits in. */
export function ViewFrame({ children }: { children: ReactNode }) {
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-5xl space-y-4 p-3 md:p-6">{children}</div>
    </div>
  );
}

export function LoadingState({ label }: { label: string }) {
  return (
    <div role="status" className="flex items-center justify-center gap-2 py-16 text-sm text-muted-foreground">
      <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
      {label}
    </div>
  );
}

export function EmptyState({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="rounded-lg border border-dashed border-border px-4 py-12 text-center">
      <p className="text-sm font-medium">{title}</p>
      {hint && <p className="mt-1 text-sm text-muted-foreground">{hint}</p>}
    </div>
  );
}

export function ErrorState({
  error,
  fallback,
  onRetry,
}: {
  error: unknown;
  fallback: string;
  onRetry?: () => void;
}) {
  const { team, update } = useTabParams();
  // A team in the address that this person does not answer for is not a failure
  // to retry, it is a filter to take off.
  const badFilter = !!team && isClientError(error);
  return (
    <div role="alert" className="rounded-lg border border-border px-4 py-12 text-center">
      <AlertTriangle className="mx-auto mb-2 h-5 w-5 text-muted-foreground" aria-hidden />
      <p className="text-sm">{badFilter ? 'That team is not one you can view.' : apiErrorMessage(error, fallback)}</p>
      {badFilter ? (
        <Button variant="outline" size="sm" className="mt-3" onClick={() => update({ team: null })}>
          Show everyone
        </Button>
      ) : (
        onRetry && (
          <Button variant="outline" size="sm" className="mt-3" onClick={onRetry}>
            Try again
          </Button>
        )
      )}
    </div>
  );
}

const ALL_TEAMS = 'all';

/** Narrows the view to a team. Nothing to narrow, nothing drawn. */
export function TeamFilter({ teams }: { teams: TeamRef[] }) {
  const { team, update } = useTabParams();
  if (teams.length === 0) return null;
  return (
    <Select value={team ?? ALL_TEAMS} onValueChange={(v) => update({ team: v === ALL_TEAMS ? null : v, person: null })}>
      <SelectTrigger className="h-9 w-full sm:w-48" aria-label="Filter by team">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={ALL_TEAMS}>All teams</SelectItem>
        {teams.map((t) => (
          <SelectItem key={t.id} value={t.id}>
            {t.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

const TILES: { key: keyof TaskCounts; label: string; warn?: boolean }[] = [
  { key: 'pending', label: 'Pending' },
  { key: 'ongoing', label: 'Ongoing' },
  { key: 'blocked', label: 'Blocked', warn: true },
  { key: 'overdue', label: 'Overdue', warn: true },
  { key: 'addedTo', label: 'Added to' },
  { key: 'completed', label: 'Completed' },
];

export function StatTiles({ counts }: { counts: TaskCounts }) {
  return (
    <dl className="grid grid-cols-3 gap-2 md:grid-cols-6">
      {TILES.map(({ key, label, warn }) => (
        <div key={key} className="flex flex-col-reverse rounded-lg border border-border bg-card px-3 py-2">
          <dt className="text-xs text-muted-foreground">{label}</dt>
          <dd className={cn('text-2xl font-semibold tabular-nums', warn && (counts[key] ?? 0) > 0 && DANGER_TEXT)}>
            {counts[key] ?? 0}
          </dd>
        </div>
      ))}
    </dl>
  );
}

interface RowTask {
  id: string;
  title: string;
  status: string;
  priority: string;
  overdue: boolean;
  dueDate: string | null;
  completedAt: string | null;
  isNew?: boolean;
}

/** One task on one line: title first, and only the facts worth a glance after it. */
export function TaskRow({ task }: { task: RowTask }) {
  const date = taskDateLabel(task);
  const pressing = task.priority === 'urgent' || task.priority === 'high';
  return (
    <li className="flex min-w-0 flex-col gap-x-3 gap-y-1 py-1.5 text-sm sm:flex-row sm:items-center">
      <span className="flex min-w-0 flex-1 items-center gap-2">
        <Link to={`/tasks/${task.id}`} className="min-w-0 break-anywhere hover:underline sm:truncate">
          {task.title}
        </Link>
        {task.isNew && (
          <Badge variant="secondary" className="shrink-0 px-1.5 py-0">
            New
          </Badge>
        )}
      </span>
      {(pressing || date || task.status === 'in_review') && (
        <span className="flex shrink-0 items-center gap-2 text-xs text-muted-foreground">
          {task.status === 'in_review' && <span>In review</span>}
          {pressing && (
            <Badge variant="outline" className="px-1.5 py-0 font-medium capitalize">
              {task.priority}
            </Badge>
          )}
          {date && <span className={cn('tabular-nums', date.overdue && DANGER_TEXT)}>{date.text}</span>}
        </span>
      )}
    </li>
  );
}

function Count({ value }: { value: number }) {
  return <span className="font-normal tabular-nums text-muted-foreground">{value}</span>;
}

/** Tasks under the spec's headings, each as a card. */
export function BucketList({ sections }: { sections: BucketSection[] }) {
  return (
    <div className="space-y-4">
      {sections.map((section) => (
        <section key={section.key}>
          <h2 className="mb-1 flex items-center gap-2 text-sm font-medium">
            {section.label} <Count value={section.tasks.length} />
          </h2>
          <ul className="divide-y divide-border rounded-lg border border-border bg-card px-3">
            {section.tasks.map((task) => (
              <TaskRow key={task.id} task={task} />
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

/** One person on a scrum call, live or saved. */
export function ScrumPersonCard({ row }: { row: ScrumRow }) {
  return (
    <section className="rounded-lg border border-border bg-card p-3 md:px-4">
      <header className="flex flex-wrap items-baseline gap-x-2">
        <h2 className="min-w-0 break-anywhere text-sm font-semibold">{row.name}</h2>
        {row.teams.length > 0 && (
          <p className="min-w-0 break-anywhere text-xs text-muted-foreground">
            {row.teams.map((t) => t.name).join(', ')}
          </p>
        )}
        {row.counts.overdue > 0 && (
          <p className={cn('ml-auto text-xs tabular-nums', DANGER_TEXT)}>{row.counts.overdue} overdue</p>
        )}
      </header>

      {row.sections.length === 0 ? (
        <p className="mt-1 text-sm text-muted-foreground">No tasks</p>
      ) : (
        <div className="mt-2 space-y-2 md:space-y-1">
          {row.sections.map((section) => (
            <div key={section.key} className="md:grid md:grid-cols-[6rem_minmax(0,1fr)] md:gap-x-3">
              <h3
                className={cn(
                  'flex items-center gap-1.5 self-start text-xs font-medium text-muted-foreground md:py-2',
                  section.key === 'blocked' && DANGER_TEXT
                )}
              >
                {section.label} <Count value={section.tasks.length} />
              </h3>
              <ul className="divide-y divide-border">
                {section.tasks.map((task) => (
                  <TaskRow key={task.id} task={task} />
                ))}
              </ul>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
