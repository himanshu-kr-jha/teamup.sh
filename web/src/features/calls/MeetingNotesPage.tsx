import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useInfiniteQuery } from '@tanstack/react-query';
import { format, isToday, isYesterday } from 'date-fns';
import { AlertTriangle, ListChecks, Loader2, NotebookPen, Search, Users } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Badge } from '@/shared/components/ui/badge';
import { notetakerApi, type MeetingNotesItem } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useAuth } from '@/features/auth/AuthProvider';
import { cn } from '@/shared/utils/cn';

const STATUS: Record<MeetingNotesItem['status'], { label: string; tone: string } | undefined> = {
  ready: undefined,
  taking: { label: 'In progress', tone: 'bg-sky-100 text-sky-800 dark:bg-sky-950 dark:text-sky-300' },
  writing: { label: 'Being written', tone: 'bg-sky-100 text-sky-800 dark:bg-sky-950 dark:text-sky-300' },
  failed: { label: 'Not written', tone: 'bg-destructive/10 text-destructive' },
  empty: { label: 'Nothing heard', tone: 'bg-muted text-muted-foreground' },
};

function dayLabel(d: Date): string {
  if (isToday(d)) return 'Today';
  if (isYesterday(d)) return 'Yesterday';
  return format(d, 'EEEE d MMMM yyyy');
}

/** Meetings, newest first, under a heading for each day. */
function byDay(items: MeetingNotesItem[]): Array<{ day: string; items: MeetingNotesItem[] }> {
  const groups: Array<{ day: string; items: MeetingNotesItem[] }> = [];
  for (const it of items) {
    const day = dayLabel(new Date(it.date));
    const last = groups[groups.length - 1];
    if (last && last.day === day) last.items.push(it);
    else groups.push({ day, items: [it] });
  }
  return groups;
}

/**
 * Every meeting the note-taker was in that you were in too, with its minutes:
 * the way back to them once the notification is gone. Administrators can also
 * see everybody's.
 */
export function MeetingNotesPage() {
  const { user } = useAuth();
  const isAdmin = user?.roles?.some((r) => r === 'owner' || r === 'admin') ?? false;
  const [scope, setScope] = useState<'mine' | 'all'>('mine');
  const [typed, setTyped] = useState('');
  const [q, setQ] = useState('');
  useEffect(() => {
    const t = setTimeout(() => setQ(typed.trim()), 300);
    return () => clearTimeout(t);
  }, [typed]);

  const { data, error, isPending, fetchNextPage, hasNextPage, isFetchingNextPage, refetch } = useInfiniteQuery({
    queryKey: ['meeting-notes', scope, q],
    queryFn: async ({ pageParam }) => (await notetakerApi.list({ scope, q: q || undefined, before: pageParam })).data,
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => (last.hasMore ? last.items[last.items.length - 1]?.date : undefined),
    // Keeps up with minutes still being written.
    refetchInterval: (query) =>
      query.state.data?.pages.some((p) => p.items.some((i) => i.status === 'taking' || i.status === 'writing')) ? 15_000 : false,
  });
  const items = data?.pages.flatMap((p) => p.items) ?? [];

  return (
    <div className="flex h-full flex-col bg-background">
      <div className="flex h-16 shrink-0 items-center gap-3 border-b border-border px-4 md:px-6">
        <h1 className="truncate text-2xl font-bold">Meeting notes</h1>
      </div>

      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-3xl space-y-5 px-4 py-5 md:py-6">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
            <div className="relative min-w-0 flex-1">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
              <Input
                type="search"
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                placeholder="Search titles and summaries"
                aria-label="Search meeting notes"
                className="pl-9"
              />
            </div>
            {isAdmin && (
              <div role="group" aria-label="Whose meetings" className="inline-flex shrink-0 rounded-md border border-border p-0.5">
                {(['mine', 'all'] as const).map((s) => (
                  <button
                    key={s}
                    type="button"
                    aria-pressed={scope === s}
                    onClick={() => setScope(s)}
                    className={cn(
                      'rounded px-3 py-1.5 text-sm font-medium transition-colors',
                      scope === s ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground'
                    )}
                  >
                    {s === 'mine' ? 'Mine' : 'Everyone’s'}
                  </button>
                ))}
              </div>
            )}
          </div>

          {isPending ? (
            <Loader2 className="mx-auto h-6 w-6 animate-spin text-muted-foreground" aria-label="Loading" />
          ) : error ? (
            <div className="rounded-lg border border-border p-6 text-center">
              <p className="text-sm text-muted-foreground">{apiErrorMessage(error, 'Could not load the meeting notes.')}</p>
              <Button variant="outline" size="sm" className="mt-3" onClick={() => refetch()}>
                Try again
              </Button>
            </div>
          ) : items.length === 0 ? (
            <div className="rounded-lg border border-dashed border-border p-8 text-center" data-testid="notes-empty">
              <NotebookPen className="mx-auto mb-3 h-8 w-8 text-muted-foreground" aria-hidden="true" />
              {q ? (
                <p className="font-medium">No meeting notes match “{q}”.</p>
              ) : (
                <>
                  <p className="font-medium">No meeting notes yet</p>
                  <p className="mx-auto mt-1 max-w-sm text-sm text-muted-foreground">
                    When the note-taker is on, the minutes of every call and meeting you are in appear here shortly after it ends.
                  </p>
                </>
              )}
            </div>
          ) : (
            <>
              {byDay(items).map((g) => (
                <section key={g.day} aria-label={g.day} className="space-y-2">
                  <h2 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{g.day}</h2>
                  <ul className="divide-y divide-border overflow-hidden rounded-lg border border-border bg-card">
                    {g.items.map((it) => (
                      <li key={it.callId}>
                        <NoteRow item={it} />
                      </li>
                    ))}
                  </ul>
                </section>
              ))}
              {hasNextPage && (
                <div className="text-center">
                  <Button variant="outline" size="sm" onClick={() => fetchNextPage()} disabled={isFetchingNextPage}>
                    {isFetchingNextPage && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                    Older meetings
                  </Button>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function NoteRow({ item }: { item: MeetingNotesItem }) {
  const status = STATUS[item.status];
  return (
    <Link
      to={`/calls/${item.callId}/minutes`}
      className="block p-4 transition-colors hover:bg-accent focus-visible:bg-accent focus-visible:outline-none"
      data-testid="meeting-note"
    >
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="truncate font-medium">{item.title}</span>
            {status && <span className={cn('rounded px-1.5 py-0.5 text-xs font-medium', status.tone)}>{status.label}</span>}
            {!item.attended && (
              <Badge variant="outline" className="text-xs font-normal">
                You were not in it
              </Badge>
            )}
          </div>
          {item.summary ? (
            <p className="mt-1 line-clamp-2 text-sm text-muted-foreground">{item.summary}</p>
          ) : item.status === 'failed' ? (
            <p className="mt-1 flex items-start gap-1 text-sm text-muted-foreground">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-destructive" aria-hidden="true" />
              The transcript is kept; open it to write the minutes again.
            </p>
          ) : null}
          <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
            {item.attendees.length > 0 && (
              <span className="flex min-w-0 items-center gap-1">
                <Users className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                <span className="truncate">{item.attendees.join(', ')}</span>
              </span>
            )}
            {item.actionItems > 0 && (
              <span className="flex items-center gap-1">
                <ListChecks className="h-3.5 w-3.5" aria-hidden="true" />
                {item.actionItems === 1 ? '1 action item' : `${item.actionItems} action items`}
              </span>
            )}
          </div>
        </div>
        <time dateTime={item.date} className="shrink-0 text-sm tabular-nums text-muted-foreground">
          {format(new Date(item.date), 'HH:mm')}
        </time>
      </div>
    </Link>
  );
}
