import { useCallback, useEffect, useMemo, useState } from 'react';
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { addDays, format, isSameDay, isToday, parse, isValid } from 'date-fns';
import { AlertTriangle, CalendarPlus, ChevronLeft, ChevronRight, Loader2, Lock, TimerOff } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { calendarApi, orgApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { cn } from '@/shared/utils/cn';
import { useAuth } from '@/features/auth/AuthProvider';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { BlockDialog } from './BlockDialog';
import { CalendarGrid } from './CalendarGrid';
import { MonthGrid } from './MonthGrid';
import { EventDialog } from './EventDialog';
import { ScheduleDialog } from './ScheduleDialog';
import {
  dayLabelKey,
  defaultSlot,
  withLiveEnds,
  rangeForDays,
  shiftDays,
  visibleDays,
  viewFrom,
  CALENDAR_VIEWS,
  type CalendarItem,
  type CalendarView,
} from './layout';

function useIsWide(): boolean {
  const [wide, setWide] = useState(() => window.matchMedia('(min-width: 768px)').matches);
  useEffect(() => {
    const query = window.matchMedia('(min-width: 768px)');
    const change = () => setWide(query.matches);
    query.addEventListener('change', change);
    return () => query.removeEventListener('change', change);
  }, []);
  return wide;
}

function readDate(value: string | null): Date {
  if (value) {
    const parsed = parse(value, 'yyyy-MM-dd', new Date());
    if (isValid(parsed)) return parsed;
  }
  return new Date();
}

function rangeTitle(days: Date[], view: CalendarView, anchor: Date): string {
  if (view === 'month') return format(anchor, 'MMMM yyyy');
  const first = days[0];
  const last = days[days.length - 1];
  if (days.length === 1) return format(first, 'EEE, MMM d, yyyy');
  if (first.getMonth() === last.getMonth()) return `${format(first, 'MMM d')} - ${format(last, 'd, yyyy')}`;
  if (first.getFullYear() === last.getFullYear()) return `${format(first, 'MMM d')} - ${format(last, 'MMM d, yyyy')}`;
  return `${format(first, 'MMM d, yyyy')} - ${format(last, 'MMM d, yyyy')}`;
}

type Slot = { start: Date; end: Date };

export function CalendarPage() {
  const { user } = useAuth();
  const { on } = useWebSocket();
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const wide = useIsWide();
  const view: CalendarView = viewFrom(params.get('view'), wide);

  // What is on screen lives in the address, so a notification can link to a day
  // and a reload stays where it was.
  const anchor = useMemo(() => readDate(params.get('date')), [params]);
  const viewedId = params.get('person') || user?.id || '';
  const own = viewedId === user?.id;

  const days = useMemo(() => visibleDays(anchor, view), [anchor, view]);
  const range = useMemo(() => rangeForDays(days), [days]);
  const timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone;

  const setParam = useCallback(
    (changes: Record<string, string | null>) =>
      setParams(
        (current) => {
          const next = new URLSearchParams(current);
          for (const [key, value] of Object.entries(changes)) {
            if (value) next.set(key, value);
            else next.delete(key);
          }
          return next;
        },
        { replace: true }
      ),
    [setParams]
  );

  const { data: usersResponse } = useQuery({ queryKey: ['users'], queryFn: () => orgApi.listUsers() });
  const people = useMemo(
    () =>
      [...(usersResponse?.data ?? [])].sort((a: any, b: any) => (a.name ?? '').localeCompare(b.name ?? '')) as {
        id: string;
        name: string;
      }[],
    [usersResponse]
  );
  const viewedName = own ? 'your' : `${people.find((p) => p.id === viewedId)?.name ?? 'their'}'s`;

  const { data, error, isPending, isFetching, refetch } = useQuery({
    queryKey: ['calendar', viewedId, range.from, range.to],
    queryFn: () => calendarApi.get({ userId: viewedId, from: range.from, to: range.to }).then((res) => res.data),
    enabled: !!viewedId,
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    refetchInterval: 60_000,
  });
  const items: CalendarItem[] = useMemo(() => data ?? [], [data]);

  // Being invited or a meeting moving comes in as a notification. Nobody should
  // have to reload to see it on their calendar.
  useEffect(() => {
    const refresh = () => queryClient.invalidateQueries({ queryKey: ['calendar'] });
    const stops = [
      on('notification.new', (msg) => {
        const type = (msg.payload as { type?: string })?.type ?? '';
        if (type.startsWith('meeting.')) void refresh();
      }),
      on('call.ended', () => void refresh()),
    ];
    return () => stops.forEach((stop) => stop());
  }, [on, queryClient]);

  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = setInterval(() => setNow(new Date()), 30_000);
    return () => clearInterval(timer);
  }, []);

  const [selected, setSelected] = useState<CalendarItem | null>(null);
  const [blockDialog, setBlockDialog] = useState<{ item: CalendarItem | null; slot: Slot } | null>(null);
  const [scheduleDialog, setScheduleDialog] = useState<{ item: CalendarItem | null; slot: Slot } | null>(null);

  // A meeting still going keeps growing between reads of the calendar.
  const liveItems = useMemo(() => withLiveEnds(items, now), [items, now]);
  const newBlock = (slot: Slot = defaultSlot(now)) => setBlockDialog({ item: null, slot });
  const newMeeting = (slot: Slot = defaultSlot(now)) => setScheduleDialog({ item: null, slot });

  // The server sends only kind and times when you have no authority over somebody,
  // and that is the only thing this notice goes by.
  const limitedDetail = !own && items.length > 0 && items.every((item) => !item.source);

  return (
    <div className="flex h-full flex-col bg-background">
      <div className="shrink-0 space-y-2 border-b border-border px-3 py-2 md:px-6">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <h1 className="text-xl font-bold md:text-2xl">Calendar</h1>
          <div className="flex items-center gap-1">
            <Button
              variant="outline"
              size="icon"
              className="h-9 w-9"
              aria-label={`Previous ${view}`}
              onClick={() => setParam({ date: dayLabelKey(shiftDays(anchor, view, -1)) })}
            >
              <ChevronLeft className="h-4 w-4" />
            </Button>
            <Button variant="outline" size="sm" className="h-9" onClick={() => setParam({ date: null })}>
              Today
            </Button>
            <Button
              variant="outline"
              size="icon"
              className="h-9 w-9"
              aria-label={`Next ${view}`}
              onClick={() => setParam({ date: dayLabelKey(shiftDays(anchor, view, 1)) })}
            >
              <ChevronRight className="h-4 w-4" />
            </Button>
          </div>
          <ViewSwitch view={view} onChange={(next) => setParam({ view: next })} />
          <h2 className="text-sm font-medium tabular-nums md:text-base" aria-live="polite">
            {rangeTitle(days, view, anchor)}
          </h2>
          {isFetching && !isPending && (
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" aria-label="Refreshing" />
          )}
          <div className="flex w-full flex-wrap items-center gap-2 md:ml-auto md:w-auto">
            <Select value={viewedId} onValueChange={(id) => setParam({ person: id === user?.id ? null : id })}>
              <SelectTrigger className="h-9 min-w-0 flex-1 md:w-52 md:flex-none" aria-label="Whose calendar">
                <SelectValue placeholder="Pick a person" />
              </SelectTrigger>
              <SelectContent>
                {people.map((person) => (
                  <SelectItem key={person.id} value={person.id}>
                    {person.name}
                    {person.id === user?.id ? ' (you)' : ''}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button variant="outline" size="sm" className="h-9" onClick={() => newBlock()}>
              <TimerOff className="mr-1 h-4 w-4" aria-hidden="true" />
              Block time
            </Button>
            <Button size="sm" className="h-9" onClick={() => newMeeting()}>
              <CalendarPlus className="mr-1 h-4 w-4" aria-hidden="true" />
              Schedule
            </Button>
          </div>
        </div>

        {/* On a phone only one day shows, so the rest of the week is a tap away here. */}
        {view === 'day' && <DayStrip anchor={anchor} now={now} onPick={(day) => setParam({ date: dayLabelKey(day) })} />}

        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
          <span>Times in {timeZone.replace(/_/g, ' ')}</span>
          <Legend />
        </div>
      </div>

      {error && !data ? (
        <div role="alert" className="flex shrink-0 flex-wrap items-center justify-center gap-3 border-b border-border bg-muted/50 px-3 py-3 text-sm">
          <AlertTriangle className="h-4 w-4 text-destructive" aria-hidden="true" />
          {apiErrorMessage(error, 'Could not load this calendar.')}
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            Try again
          </Button>
        </div>
      ) : isPending ? (
        <div role="status" className="flex shrink-0 items-center justify-center gap-2 border-b border-border py-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
          Loading the calendar
        </div>
      ) : limitedDetail ? (
        <p className="flex shrink-0 items-center gap-2 border-b border-border bg-muted/40 px-3 py-1.5 text-xs text-muted-foreground md:px-6">
          <Lock className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
          You can see when {people.find((p) => p.id === viewedId)?.name ?? 'they'} is taken, but not what for.
        </p>
      ) : items.length === 0 ? (
        <p className="shrink-0 border-b border-border bg-muted/40 px-3 py-1.5 text-xs text-muted-foreground md:px-6">
          {own
            ? view === 'month'
              ? 'Nothing this month. Open a day to block time or open it for meetings.'
              : `Nothing on this ${view}. Click a time to block it or open it for meetings.`
            : `Nothing on ${viewedName} calendar ${view === 'month' ? 'this month' : `this ${view}`}.`}
        </p>
      ) : null}

      {view === 'month' ? (
        <MonthGrid
          days={days}
          anchor={anchor}
          items={liveItems}
          now={now}
          onSelect={setSelected}
          onOpenDay={(day) => setParam({ view: 'day', date: dayLabelKey(day) })}
        />
      ) : (
        <CalendarGrid
          days={days}
          items={liveItems}
          now={now}
          view={view}
          onSelect={setSelected}
          onEmptyClick={(slot) => (own ? newBlock(slot) : newMeeting(slot))}
          emptyClickLabel={own ? 'Block this time' : 'Schedule a meeting at this time'}
        />
      )}

      {selected && (
        <EventDialog
          item={selected}
          own={own}
          now={now}
          people={people}
          onClose={() => setSelected(null)}
          onEditBlock={(item) => {
            setSelected(null);
            setBlockDialog({ item, slot: { start: new Date(item.startsAt), end: new Date(item.endsAt) } });
          }}
          onEditMeeting={(item) => {
            setSelected(null);
            setScheduleDialog({ item, slot: { start: new Date(item.startsAt), end: new Date(item.endsAt) } });
          }}
        />
      )}

      <BlockDialog
        open={!!blockDialog}
        item={blockDialog?.item ?? null}
        slot={blockDialog?.slot ?? null}
        onOpenChange={(open) => !open && setBlockDialog(null)}
      />
      <ScheduleDialog
        open={!!scheduleDialog}
        item={scheduleDialog?.item ?? null}
        slot={scheduleDialog?.slot ?? null}
        people={people}
        startWith={own ? [] : [viewedId]}
        onOpenChange={(open) => !open && setScheduleDialog(null)}
      />
    </div>
  );
}

const VIEW_LABELS: Record<CalendarView, string> = { month: 'Month', week: 'Week', day: 'Day' };

/** Month, week or day; kept in the address with the date. */
function ViewSwitch({ view, onChange }: { view: CalendarView; onChange: (view: CalendarView) => void }) {
  return (
    <div className="flex rounded-md border border-input p-0.5" role="group" aria-label="View">
      {CALENDAR_VIEWS.map((v) => (
        <button
          key={v}
          type="button"
          aria-pressed={view === v}
          onClick={() => onChange(v)}
          className={cn(
            'h-8 rounded px-2.5 text-sm font-medium transition-colors',
            view === v ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-accent hover:text-foreground'
          )}
        >
          {VIEW_LABELS[v]}
        </button>
      ))}
    </div>
  );
}

function Legend() {
  return (
    <span className="flex flex-wrap items-center gap-x-3">
      <span className="flex items-center gap-1">
        <span className="h-2.5 w-2.5 rounded-sm bg-primary/70" aria-hidden="true" />
        Busy
      </span>
      <span className="flex items-center gap-1">
        <span className="h-2.5 w-2.5 rounded-sm bg-amber-500/80" aria-hidden="true" />
        Focus
      </span>
      <span className="flex items-center gap-1">
        <span className="h-2.5 w-2.5 rounded-sm bg-emerald-500/50" aria-hidden="true" />
        Open
      </span>
    </span>
  );
}

function DayStrip({ anchor, now, onPick }: { anchor: Date; now: Date; onPick: (day: Date) => void }) {
  const week = visibleDays(anchor, 'week');
  return (
    <div className="grid grid-cols-7 gap-1" role="group" aria-label="Days of this week">
      {week.map((day) => {
        const chosen = isSameDay(day, anchor);
        return (
          <button
            key={dayLabelKey(day)}
            type="button"
            aria-pressed={chosen}
            aria-label={format(addDays(day, 0), 'EEEE, MMMM d')}
            onClick={() => onPick(day)}
            className={cn(
              'flex flex-col items-center rounded-md py-1 text-xs transition-colors',
              chosen ? 'bg-primary text-primary-foreground' : 'hover:bg-accent',
              !chosen && isSameDay(day, now) && 'font-semibold text-primary',
              !chosen && !isToday(day) && 'text-muted-foreground'
            )}
          >
            <span className="text-[10px] uppercase">{format(day, 'EEEEE')}</span>
            <span className="tabular-nums">{format(day, 'd')}</span>
          </button>
        );
      })}
    </div>
  );
}
