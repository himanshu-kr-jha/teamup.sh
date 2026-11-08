import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { addHours, addMinutes, startOfDay } from 'date-fns';
import { AlertTriangle, Loader2, Search, X } from 'lucide-react';
import { FormDialog } from '@/shared/components/FormDialog';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { calendarApi, meetingsApi } from '@/shared/api/client';
import { useToast } from '@/shared/hooks/useToast';
import { cn } from '@/shared/utils/cn';
import { useAuth } from '@/features/auth/AuthProvider';
import {
  availabilityAt,
  conflictsAt,
  durationChoices,
  durationLabel,
  initialDuration,
  fromInputValue,
  meetingProblem,
  timelineSpans,
  timelineWindow,
  toInputValue,
  type Availability,
  type AvailabilityPerson,
  type CalendarItem,
} from './layout';

const STATE_LABEL: Record<Availability, string> = {
  free: 'Free',
  open: 'Open for meetings',
  focus: 'Focus time',
  busy: 'In a meeting',
};

const STATE_STYLE: Record<Availability, string> = {
  free: 'text-muted-foreground',
  open: 'text-emerald-700 dark:text-emerald-400',
  focus: 'text-amber-700 dark:text-amber-400',
  busy: 'text-destructive',
};

const SPAN_STYLE = {
  busy: 'bg-primary/60',
  focus: 'bg-amber-500/80',
  open: 'bg-emerald-500/40',
};

/** Scheduling a meeting: when, who, and whether they can make it. Conflicts warn and never refuse. */
export function ScheduleDialog({
  open,
  item,
  slot,
  people,
  startWith,
  onOpenChange,
}: {
  open: boolean;
  /** Editing this one; otherwise a new one. */
  item: CalendarItem | null;
  slot: { start: Date; end: Date } | null;
  people: { id: string; name: string }[];
  startWith: string[];
  onOpenChange: (open: boolean) => void;
}) {
  const { user } = useAuth();
  const queryClient = useQueryClient();
  const toast = useToast();
  const [title, setTitle] = useState('');
  const [start, setStart] = useState('');
  const [duration, setDuration] = useState(30);
  const [invited, setInvited] = useState<string[]>([]);
  const [search, setSearch] = useState('');

  useEffect(() => {
    if (!open || !slot) return;
    setTitle(item?.title ?? '');
    setStart(toInputValue(slot.start));
    setDuration(initialDuration(slot.start, slot.end, !!item));
    setInvited((item?.inviteeIds ?? startWith).filter((id) => id !== (item?.organizerId ?? user?.id)));
    setSearch('');
    // startWith is a new array on every render; it matters only on opening.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, item, slot]);

  const startDate = fromInputValue(start);
  const endDate = startDate ? addMinutes(startDate, duration) : null;
  const problem = meetingProblem(title, startDate, endDate, invited.length);
  const timesOk = !!startDate && !!endDate && endDate > startDate;

  const everyone = useMemo(() => (user ? [user.id, ...invited] : invited), [user, invited]);
  const dayKey = startDate ? startOfDay(startDate).toISOString() : '';
  const { data, isFetching, error } = useQuery({
    queryKey: ['availability', everyone.join(','), dayKey, endDate?.toISOString()],
    enabled: open && timesOk && everyone.length > 0,
    queryFn: () =>
      calendarApi
        .availability({
          userIds: everyone.join(','),
          from: startOfDay(startDate!).toISOString(),
          // The bars show the working day, so ask for all of it.
          to: addHours(startOfDay(endDate!), 24).toISOString(),
        })
        .then((res) => res.data),
    staleTime: 10_000,
  });
  const byPerson = useMemo(() => new Map((data ?? []).map((p) => [p.userId, p])), [data]);

  // A meeting being edited is on everybody's calendar already, at its old time.
  const ignore = item ? { startsAt: item.startsAt, endsAt: item.endsAt } : undefined;
  const conflicts = timesOk && data ? conflictsAt(data, startDate!, endDate!, ignore) : [];
  const nameOf = (id: string) => (id === user?.id ? 'You' : people.find((p) => p.id === id)?.name ?? 'Somebody');
  const hours = timesOk ? timelineWindow(startDate!, endDate!) : null;

  const filter = search.trim().toLowerCase();
  const choices = people.filter(
    (p) => p.id !== user?.id && !invited.includes(p.id) && (!filter || (p.name ?? '').toLowerCase().includes(filter))
  );

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={item ? 'Edit meeting' : 'Schedule a meeting'}
      description={
        item
          ? 'Anybody whose time changes, or who is added or taken off, will be told.'
          : "It goes straight onto everybody's calendar, and they are reminded 5 minutes before."
      }
      submitLabel={item ? 'Save' : conflicts.length > 0 ? 'Schedule anyway' : 'Schedule'}
      disabledReason={problem ?? undefined}
      onSubmit={async () => {
        const body = {
          title: title.trim(),
          startsAt: startDate!.toISOString(),
          endsAt: endDate!.toISOString(),
          inviteeIds: invited,
        };
        const { data: result } = item?.callId
          ? await meetingsApi.update(item.callId, body)
          : await meetingsApi.create(body);
        await queryClient.invalidateQueries({ queryKey: ['calendar'] });
        await queryClient.invalidateQueries({ queryKey: ['calls'] });
        const clashes = (result.conflicts ?? []).map((c) => nameOf(c.userId));
        const unique = [...new Set(clashes)];
        toast.success(
          unique.length > 0
            ? `Heads up: ${unique.join(', ')} ${unique.length === 1 && unique[0] !== 'You' ? 'has' : 'have'} something else then.`
            : 'Everybody invited has been told.',
          item ? 'Meeting saved' : 'Meeting scheduled'
        );
      }}
    >
      <div className="space-y-2">
        <Label htmlFor="meetingTitle">Name</Label>
        <Input id="meetingTitle" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Sprint planning" autoFocus />
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="min-w-0 space-y-2">
          <Label htmlFor="meetingStart">Starts</Label>
          <Input id="meetingStart" type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} />
        </div>
        <div className="min-w-0 space-y-2">
          <Label htmlFor="meetingDuration">Duration</Label>
          <Select value={String(duration)} onValueChange={(value) => setDuration(Number(value))}>
            <SelectTrigger id="meetingDuration">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {durationChoices(item && slot ? initialDuration(slot.start, slot.end, true) : duration).map((minutes) => (
                <SelectItem key={minutes} value={String(minutes)}>
                  {durationLabel(minutes)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>
      {problem && title.trim() !== '' && (
        <p className="text-sm text-destructive" role="alert">
          {problem}
        </p>
      )}

      <div className="space-y-2">
        <Label htmlFor="meetingPeople">People</Label>
        <div className="relative">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <Input
            id="meetingPeople"
            className="pl-9"
            placeholder="Add somebody by name"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        {(filter !== '' || invited.length === 0) && (
          <ul className="max-h-32 space-y-0.5 overflow-y-auto rounded-md border border-border p-1">
            {choices.length === 0 ? (
              <li className="px-2 py-1 text-sm text-muted-foreground">Nobody to add.</li>
            ) : (
              choices.map((person) => (
                <li key={person.id}>
                  <button
                    type="button"
                    className="w-full rounded px-2 py-1 text-left text-sm hover:bg-accent"
                    onClick={() => {
                      setInvited((current) => [...current, person.id]);
                      setSearch('');
                    }}
                  >
                    {person.name}
                  </button>
                </li>
              ))
            )}
          </ul>
        )}
      </div>

      <div className="space-y-1" aria-label="Who can make it">
        <div className="flex items-center gap-2 text-sm font-medium">
          Can they make it?
          {isFetching && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" aria-label="Checking" />}
        </div>
        {error ? (
          <p className="text-sm text-muted-foreground">Could not check right now. You can still schedule it.</p>
        ) : !timesOk ? (
          <p className="text-sm text-muted-foreground">Pick a start time to see.</p>
        ) : (
          <ul className="space-y-1.5">
            {everyone.map((id) => (
              <PersonRow
                key={id}
                name={nameOf(id)}
                person={byPerson.get(id)}
                start={startDate!}
                end={endDate!}
                hours={hours!}
                ignore={ignore}
                onRemove={id === user?.id ? undefined : () => setInvited((current) => current.filter((x) => x !== id))}
              />
            ))}
          </ul>
        )}
      </div>

      {conflicts.length > 0 && (
        <div role="status" className="flex items-start gap-2 rounded-md border border-amber-500/50 bg-amber-50 p-2 text-sm text-amber-950 dark:bg-amber-950/40 dark:text-amber-100">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <p className="min-w-0">
            {conflicts
              .map((c) => `${nameOf(c.userId)}: ${c.state === 'focus' ? 'focus time' : 'another meeting'}`)
              .join('; ')}
            . You can still go ahead.
          </p>
        </div>
      )}
    </FormDialog>
  );
}

function PersonRow({
  name,
  person,
  start,
  end,
  hours,
  ignore,
  onRemove,
}: {
  name: string;
  person?: AvailabilityPerson;
  start: Date;
  end: Date;
  hours: { from: Date; to: Date };
  ignore?: { startsAt: string; endsAt: string };
  onRemove?: () => void;
}) {
  const state = person ? availabilityAt(person.items, start, end, ignore) : null;
  const spans = person ? timelineSpans(person.items, hours.from, hours.to) : [];
  const [chosen] = timelineSpans(
    [{ kind: 'busy', startsAt: start.toISOString(), endsAt: end.toISOString() }],
    hours.from,
    hours.to
  );

  return (
    <li className="min-w-0 text-sm">
      <div className="flex items-center justify-between gap-2">
        <span className="min-w-0 truncate">{name}</span>
        <span className="flex shrink-0 items-center gap-1">
          <span className={cn('text-xs', state && STATE_STYLE[state])}>{state ? STATE_LABEL[state] : ''}</span>
          {onRemove && (
            <button
              type="button"
              aria-label={`Remove ${name}`}
              title={`Remove ${name}`}
              className="rounded p-0.5 text-muted-foreground hover:bg-accent"
              onClick={onRemove}
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </span>
      </div>
      {/* The day at a glance, with the time you picked outlined. */}
      <div className="relative mt-0.5 h-2 overflow-hidden rounded-sm bg-muted" aria-hidden="true">
        {spans.map((span, index) => (
          <span
            key={index}
            className={cn('absolute inset-y-0', SPAN_STYLE[span.kind])}
            style={{ left: `${span.left}%`, width: `${span.width}%` }}
          />
        ))}
        {chosen && (
          <span
            className="absolute inset-y-0 rounded-sm ring-1 ring-inset ring-foreground"
            style={{ left: `${chosen.left}%`, width: `${chosen.width}%` }}
          />
        )}
      </div>
    </li>
  );
}
